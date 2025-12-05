package match

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"dx-service/internal/model"
	appErr "dx-service/pkg/errors"
	"dx-service/pkg/logger"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var errQueueMemberNotFound = errors.New("queue member not found")

type Config struct {
	QueueLockTTL        time.Duration
	QueueMemberTTL      time.Duration
	QueueTimeout        time.Duration
	MatchedLockTTL      time.Duration
	MatchedNotifyTTL    time.Duration
	MatcherInterval     time.Duration
	CandidateMultiplier int
}

func defaultConfig() Config {
	return Config{
		QueueLockTTL:        10 * time.Second,
		QueueMemberTTL:      3 * time.Minute,
		QueueTimeout:        3 * time.Minute,
		MatchedLockTTL:      1 * time.Minute,
		MatchedNotifyTTL:    5 * time.Minute,
		MatcherInterval:     500 * time.Millisecond,
		CandidateMultiplier: 3,
	}
}

type Service struct {
	db  *gorm.DB
	rdb *redis.Client
	cfg Config

	startOnce sync.Once
	startErr  error
}

func NewService(db *gorm.DB, rdb *redis.Client) *Service {
	return &Service{
		db:  db,
		rdb: rdb,
		cfg: defaultConfig(),
	}
}

func (s *Service) Start(ctx context.Context) error {
	s.startOnce.Do(func() {
		var scenes []model.Scene
		err := s.db.WithContext(ctx).Find(&scenes).Error
		if err != nil {
			s.startErr = err
			return
		}
		for _, scene := range scenes {
			sceneCopy := scene
			go s.runMatcher(ctx, sceneCopy)
		}
	})
	return s.startErr
}

func (s *Service) JoinQueue(ctx context.Context, req JoinQueueRequest) (string, error) {
	scene, err := s.loadScene(ctx, req.SceneID)
	if err != nil {
		return "", err
	}
	if scene == nil {
		return "", appErr.ErrSceneNotFound
	}

	if req.BuyIn < scene.MinIn || (scene.MaxIn > 0 && req.BuyIn > scene.MaxIn) {
		return "", appErr.ErrInvalidBuyIn
	}

	walletBalance, err := s.loadWalletBalance(ctx, req.UserID)
	if err != nil {
		return "", err
	}
	if walletBalance < req.BuyIn {
		return "", appErr.ErrInsufficientBalance
	}

	queueKey := buildQueueKey(scene.ID)
	memberID := strconv.FormatInt(req.UserID, 10)

	if _, err := s.rdb.ZScore(ctx, queueKey, memberID).Result(); err == nil {
		return "", appErr.ErrAlreadyInQueue
	} else if err != redis.Nil {
		return "", err
	}

	lockKey := buildQueueLockKey(req.UserID)
	gotLock, err := s.rdb.SetNX(ctx, lockKey, scene.ID, s.cfg.QueueLockTTL).Result()
	if err != nil {
		return "", err
	}
	if !gotLock {
		return "", appErr.ErrQueueProcessing
	}
	defer s.rdb.Del(ctx, lockKey)

	member := queueMember{
		UserID:          req.UserID,
		SceneID:         req.SceneID,
		BuyIn:           req.BuyIn,
		GPSLat:          req.GPSLat,
		GPSLng:          req.GPSLng,
		IP:              req.IP,
		BalanceSnapshot: walletBalance,
		JoinedAt:        time.Now(),
	}

	if err := s.saveQueueMember(ctx, member); err != nil {
		return "", err
	}

	score := float64(time.Now().UnixMilli())
	if err := s.rdb.ZAdd(ctx, queueKey, redis.Z{
		Score:  score,
		Member: memberID,
	}).Err(); err != nil {
		s.removeQueueMember(ctx, member.SceneID, member.UserID)
		return "", err
	}

	logger.Log.Info("user joined queue",
		zap.Int64("userID", req.UserID),
		zap.Int64("sceneID", req.SceneID),
		zap.Float64("score", score),
	)

	return memberID, nil
}

func (s *Service) CancelQueue(ctx context.Context, req CancelQueueRequest) error {
	queueKey := buildQueueKey(req.SceneID)
	memberID := strconv.FormatInt(req.UserID, 10)
	_, err := s.rdb.ZRem(ctx, queueKey, memberID).Result()
	if err != nil && err != redis.Nil {
		return err
	}

	s.removeQueueMember(ctx, req.SceneID, req.UserID)
	s.rdb.Del(ctx, buildMatchNotifyKey(req.UserID))

	reason := req.Reason
	if reason == "" {
		reason = "user"
	}
	logger.Log.Info("queue cancelled",
		zap.Int64("userID", req.UserID),
		zap.Int64("sceneID", req.SceneID),
		zap.String("reason", reason),
	)
	return nil
}

func (s *Service) GetStatus(ctx context.Context, userID, sceneID int64) (*StatusResult, error) {
	notifyKey := buildMatchNotifyKey(userID)
	payloadStr, err := s.rdb.Get(ctx, notifyKey).Result()
	if err == nil {
		var payload matchNotifyPayload
		if jsonErr := json.Unmarshal([]byte(payloadStr), &payload); jsonErr == nil {
			return &StatusResult{
				Status:  QueueStatusMatched,
				SceneID: payload.SceneID,
				TableID: &payload.TableID,
				MatchID: &payload.MatchID,
			}, nil
		}
	} else if err != redis.Nil {
		return nil, err
	}

	queueKey := buildQueueKey(sceneID)
	memberID := strconv.FormatInt(userID, 10)
	if _, err := s.rdb.ZScore(ctx, queueKey, memberID).Result(); err == nil {
		var joinedAt *time.Time
		if member, err := s.loadQueueMember(ctx, sceneID, userID); err == nil {
			joined := member.JoinedAt
			joinedAt = &joined
		}
		return &StatusResult{
			Status:   QueueStatusQueued,
			SceneID:  sceneID,
			JoinedAt: joinedAt,
		}, nil
	} else if err != redis.Nil {
		return nil, err
	}

	return &StatusResult{
		Status:  QueueStatusIdle,
		SceneID: sceneID,
	}, nil
}

func (s *Service) saveQueueMember(ctx context.Context, member queueMember) error {
	data, err := json.Marshal(member)
	if err != nil {
		return err
	}
	key := buildQueueMemberKey(member.SceneID, member.UserID)
	return s.rdb.Set(ctx, key, data, s.cfg.QueueMemberTTL).Err()
}

func (s *Service) loadQueueMember(ctx context.Context, sceneID, userID int64) (queueMember, error) {
	var member queueMember
	key := buildQueueMemberKey(sceneID, userID)
	data, err := s.rdb.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return member, errQueueMemberNotFound
		}
		return member, err
	}
	if err := json.Unmarshal([]byte(data), &member); err != nil {
		return member, err
	}
	return member, nil
}

func (s *Service) removeQueueMember(ctx context.Context, sceneID, userID int64) {
	key := buildQueueMemberKey(sceneID, userID)
	s.rdb.Del(ctx, key)
}

func (s *Service) cleanupExpiredQueue(ctx context.Context, sceneID int64) error {
	if s.cfg.QueueTimeout <= 0 {
		return nil
	}
	queueKey := buildQueueKey(sceneID)
	deadline := time.Now().Add(-s.cfg.QueueTimeout).UnixMilli()
	maxScore := strconv.FormatFloat(float64(deadline), 'f', 0, 64)

	members, err := s.rdb.ZRangeByScore(ctx, queueKey, &redis.ZRangeBy{
		Min: "-inf",
		Max: maxScore,
	}).Result()
	if err != nil {
		if err == redis.Nil {
			return nil
		}
		return err
	}

	for _, member := range members {
		userID, err := strconv.ParseInt(member, 10, 64)
		if err != nil {
			continue
		}
		if err := s.CancelQueue(ctx, CancelQueueRequest{
			UserID:  userID,
			SceneID: sceneID,
			Reason:  "timeout",
		}); err != nil {
			logger.Log.Warn("queue timeout cancel failed",
				zap.Int64("userID", userID),
				zap.Int64("sceneID", sceneID),
				zap.Error(err),
			)
			continue
		}
		logger.Log.Info("queue timeout cancelled",
			zap.Int64("userID", userID),
			zap.Int64("sceneID", sceneID),
		)
	}

	return nil
}

func (s *Service) candidateLimit(scene model.Scene) int {
	if s.cfg.CandidateMultiplier <= 0 {
		return scene.SeatCount * 2
	}
	return scene.SeatCount * s.cfg.CandidateMultiplier
}

func (s *Service) loadScene(ctx context.Context, sceneID int64) (*model.Scene, error) {
	var scene model.Scene
	err := s.db.WithContext(ctx).First(&scene, sceneID).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &scene, nil
}

func (s *Service) loadWalletBalance(ctx context.Context, userID int64) (int64, error) {
	var wallet model.Wallet
	err := s.db.WithContext(ctx).Where("user_id = ?", userID).First(&wallet).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return 0, nil
		}
		return 0, err
	}
	return wallet.BalanceAvailable, nil
}

func (s *Service) ValidateTableAccess(ctx context.Context, userID, tableID int64) error {
	if userID == 0 {
		return appErr.ErrUnauthorized
	}
	if tableID == 0 {
		return appErr.ErrTableNotFound
	}

	var table model.Table
	if err := s.db.WithContext(ctx).First(&table, tableID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return appErr.ErrTableNotFound
		}
		return err
	}

	if len(table.PlayersJSON) == 0 {
		return appErr.ErrTableAccessDenied
	}

	var players map[string]struct {
		UserID int64 `json:"userId"`
	}
	if err := json.Unmarshal(table.PlayersJSON, &players); err != nil {
		return err
	}
	for _, player := range players {
		if player.UserID == userID {
			return nil
		}
	}
	return appErr.ErrTableAccessDenied
}

func buildQueueKey(sceneID int64) string {
	return fmt.Sprintf("queue:%d", sceneID)
}

func buildQueueMemberKey(sceneID, userID int64) string {
	return fmt.Sprintf("queue:member:%d:%d", sceneID, userID)
}

func buildQueueLockKey(userID int64) string {
	return fmt.Sprintf("queue:lock:%d", userID)
}

func buildMatchNotifyKey(userID int64) string {
	return fmt.Sprintf("match:pending:%d", userID)
}

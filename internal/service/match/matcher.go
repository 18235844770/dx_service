package match

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"dx-service/internal/config"
	"dx-service/internal/model"
	"dx-service/pkg/logger"
	"dx-service/pkg/utils/geo"
	netutil "dx-service/pkg/utils/net"

	"go.uber.org/zap"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func (s *Service) runMatcher(ctx context.Context, scene model.Scene) {
	logger.Log.Info("matcher started",
		zap.Int64("sceneID", scene.ID),
		zap.String("sceneName", scene.Name),
		zap.Int("seatCount", scene.SeatCount),
	)

	ticker := time.NewTicker(s.cfg.MatcherInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Log.Info("matcher stopped", zap.Int64("sceneID", scene.ID))
			return
		case <-ticker.C:
			if err := s.tryCompose(ctx, scene); err != nil {
				logger.Log.Warn("matcher compose error",
					zap.Int64("sceneID", scene.ID),
					zap.Error(err),
				)
			}
		}
	}
}

func (s *Service) tryCompose(ctx context.Context, scene model.Scene) error {
	if err := s.cleanupExpiredQueue(ctx, scene.ID); err != nil {
		logger.Log.Warn("queue cleanup error",
			zap.Int64("sceneID", scene.ID),
			zap.Error(err),
		)
	}

	queueKey := buildQueueKey(scene.ID)
	rangeEnd := int64(s.candidateLimit(scene) - 1)
	if rangeEnd < 0 {
		return nil
	}
	members, err := s.rdb.ZRange(ctx, queueKey, 0, rangeEnd).Result()
	if err != nil {
		return err
	}
	if len(members) < scene.SeatCount {
		return nil
	}

	candidates := make([]queueMember, 0, len(members))
	for _, member := range members {
		userID, err := strconv.ParseInt(member, 10, 64)
		if err != nil {
			continue
		}
		qm, err := s.loadQueueMember(ctx, scene.ID, userID)
		if err != nil {
			if err == errQueueMemberNotFound {
				continue
			}
			return err
		}
		candidates = append(candidates, qm)
	}

	selected := s.selectPlayers(scene, candidates)
	if len(selected) < scene.SeatCount {
		return nil
	}

	return s.composeTable(ctx, scene, selected)
}

func (s *Service) selectPlayers(scene model.Scene, candidates []queueMember) []queueMember {
	required := scene.SeatCount
	selected := make([]queueMember, 0, required)

	for _, candidate := range candidates {
		if len(selected) >= required {
			break
		}
		if candidate.BalanceSnapshot < scene.MinIn {
			continue
		}
		if s.shouldEnforceLocation(scene) && !hasValidLocation(candidate) {
			continue
		}
		if !s.passesDistance(scene, selected, candidate) {
			continue
		}
		if !passesNetwork(selected, candidate) {
			continue
		}
		selected = append(selected, candidate)
	}
	return selected
}

func hasValidLocation(member queueMember) bool {
	return member.GPSLat != 0 && member.GPSLng != 0
}

func (s *Service) passesDistance(scene model.Scene, selected []queueMember, candidate queueMember) bool {
	if !s.shouldEnforceLocation(scene) {
		return true
	}
	for _, existing := range selected {
		if !hasValidLocation(existing) || !hasValidLocation(candidate) {
			return false
		}
		distance := geo.HaversineDistance(existing.GPSLat, existing.GPSLng, candidate.GPSLat, candidate.GPSLng)
		if distance < float64(scene.DistanceThresholdM) {
			return false
		}
	}
	return true
}

func passesNetwork(selected []queueMember, candidate queueMember) bool {
	for _, existing := range selected {
		if netutil.SameSubnet24(existing.IP, candidate.IP) {
			return false
		}
	}
	return true
}

func (s *Service) shouldEnforceLocation(scene model.Scene) bool {
	if scene.DistanceThresholdM <= 0 {
		return false
	}
	if config.GlobalConfig != nil && config.GlobalConfig.Features.SkipLocationValidation {
		return false
	}
	return true
}

func (s *Service) composeTable(ctx context.Context, scene model.Scene, players []queueMember) error {
	queueKey := buildQueueKey(scene.ID)
	for _, player := range players {
		memberID := strconv.FormatInt(player.UserID, 10)
		removed, err := s.rdb.ZRem(ctx, queueKey, memberID).Result()
		if err != nil {
			return err
		}
		if removed == 0 {
			return nil
		}
		s.removeQueueMember(ctx, scene.ID, player.UserID)
		s.rdb.Set(ctx, buildQueueLockKey(player.UserID), scene.ID, s.cfg.MatchedLockTTL)
	}

	tableID, matchID, err := s.createTableAndMatch(ctx, scene, players)
	if err != nil {
		return err
	}

	payload := matchNotifyPayload{
		SceneID: scene.ID,
		TableID: tableID,
		MatchID: matchID,
	}
	data, _ := json.Marshal(payload)
	for _, player := range players {
		s.rdb.Set(ctx, buildMatchNotifyKey(player.UserID), data, s.cfg.MatchedNotifyTTL)
	}

	logger.Log.Info("match composed",
		zap.Int64("sceneID", scene.ID),
		zap.Int64("tableID", tableID),
		zap.Int64("matchID", matchID),
		zap.Int("players", len(players)),
	)
	return nil
}

func (s *Service) createTableAndMatch(ctx context.Context, scene model.Scene, players []queueMember) (int64, int64, error) {
	var (
		tableID int64
		matchID int64
	)
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		playerMap := make(map[string]map[string]interface{})
		for idx, player := range players {
			seat := idx + 1
			playerMap[strconv.Itoa(seat)] = map[string]interface{}{
				"userId": player.UserID,
				"alias":  fmt.Sprintf("玩家%d", seat),
				"status": "waiting",
			}
		}
		playerBytes, err := json.Marshal(playerMap)
		if err != nil {
			return err
		}

		table := model.Table{
			SceneID:     scene.ID,
			Status:      "waiting",
			SeatCount:   scene.SeatCount,
			MangoStreak: 0,
			PlayersJSON: datatypes.JSON(playerBytes),
		}
		if err := tx.Create(&table).Error; err != nil {
			return err
		}
		tableID = table.ID

		match := model.Match{
			TableID: table.ID,
			SceneID: scene.ID,
		}
		if err := tx.Create(&match).Error; err != nil {
			return err
		}
		matchID = match.ID

		return nil
	})

	return tableID, matchID, err
}

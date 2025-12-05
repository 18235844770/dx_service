package auth

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"math/big"
	"strings"
	"time"

	"dx-service/internal/config"
	"dx-service/internal/model"
	pkgAuth "dx-service/pkg/auth"
	appErr "dx-service/pkg/errors"
	"dx-service/pkg/logger"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type Service struct {
	db      *gorm.DB
	rdb     *redis.Client
	codeTTL time.Duration
}

type LoginResult struct {
	Token    string     `json:"token"`
	ExpireAt time.Time  `json:"expireAt"`
	User     model.User `json:"user"`
}

func NewService(db *gorm.DB, rdb *redis.Client) *Service {
	return &Service{
		db:      db,
		rdb:     rdb,
		codeTTL: 5 * time.Minute,
	}
}

const testOTPCode = "123456"

func (s *Service) SendSMS(ctx context.Context, phone string) error {
	if !isValidPhone(phone) {
		return appErr.ErrInvalidPhone
	}
	code := ""
	if strings.EqualFold(config.GlobalConfig.Server.Mode, "debug") {
		code = testOTPCode
	} else {
		var err error
		code, err = generateOTP()
		if err != nil {
			return err
		}
	}

	key := buildSMSKey(phone)
	if err := s.rdb.Set(ctx, key, code, s.codeTTL).Err(); err != nil {
		return err
	}
	logger.Log.Info("otp generated",
		zap.String("phone", maskPhone(phone)),
		zap.Bool("testCode", strings.EqualFold(config.GlobalConfig.Server.Mode, "debug")),
	)
	return nil
}

func (s *Service) Login(ctx context.Context, phone, code, inviteCode string) (*LoginResult, error) {
	if strings.TrimSpace(phone) == "" || strings.TrimSpace(code) == "" {
		return nil, appErr.ErrInvalidPhone
	}

	key := buildSMSKey(phone)
	stored, err := s.rdb.Get(ctx, key).Result()
	if err != nil {
	if err == redis.Nil {
		return nil, appErr.ErrSMSCodeExpired
	}
		return nil, err
	}
	if stored != code {
		return nil, appErr.ErrInvalidSMSCode
	}
	s.rdb.Del(ctx, key)

	var user model.User
	err = s.db.WithContext(ctx).Where("phone = ?", phone).First(&user).Error
	if err != nil {
		if err != gorm.ErrRecordNotFound {
			return nil, err
		}
		user, err = s.createUser(ctx, phone)
		if err != nil {
			return nil, err
		}
	}

	if err := s.ensureInviteCode(ctx, &user); err != nil {
		return nil, err
	}
	if strings.EqualFold(user.Status, "banned") {
		return nil, appErr.ErrUserBanned
	}
	if err := s.bindAgentIfNeeded(ctx, &user, inviteCode); err != nil {
		return nil, err
	}

	token, err := pkgAuth.GenerateToken(user.ID)
	if err != nil {
		return nil, err
	}

	expireAt := time.Now().Add(time.Duration(config.GlobalConfig.JWT.Expire) * time.Hour)
	return &LoginResult{
		Token:    token,
		ExpireAt: expireAt,
		User:     user,
	}, nil
}

func (s *Service) createUser(ctx context.Context, phone string) (model.User, error) {
	inviteCode := generateInviteCode()
	user := model.User{
			Phone:      phone,
			Status:     "normal",
		InviteCode: inviteCode,
	}
	if err := s.db.WithContext(ctx).Create(&user).Error; err != nil {
		return model.User{}, err
		}
	return user, nil
}

func (s *Service) ensureInviteCode(ctx context.Context, user *model.User) error {
	if user.InviteCode != "" {
		return nil
		}
	code := generateInviteCode()
	if err := s.db.WithContext(ctx).Model(user).Update("invite_code", code).Error; err != nil {
			return err
	}
	user.InviteCode = code
	return nil
}

func (s *Service) bindAgentIfNeeded(ctx context.Context, user *model.User, inviteCode string) error {
	if inviteCode == "" || user.BindAgentID != nil {
		if inviteCode != "" && user.BindAgentID != nil {
			return appErr.ErrAlreadyBoundAgent
		}
		return nil
	}
	var agent model.User
	err := s.db.WithContext(ctx).Where("invite_code = ?", inviteCode).First(&agent).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return appErr.ErrInviteCodeNotFound
		}
		return err
	}

	agentPath := agent.AgentPath
	if agentPath != "" {
		agentPath += ">"
	}
	agentPath += fmt.Sprintf("%d", agent.ID)

	update := map[string]interface{}{
		"bind_agent_id": agent.ID,
		"agent_path":    agentPath,
	}
	if err := s.db.WithContext(ctx).Model(user).Updates(update).Error; err != nil {
		return err
	}
	user.BindAgentID = &agent.ID
	user.AgentPath = agentPath

	agentModel := model.Agent{ID: agent.ID}
	s.db.WithContext(ctx).FirstOrCreate(&agentModel, model.Agent{ID: agent.ID})
	return nil
}

func generateOTP() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func generateInviteCode() string {
	b := make([]byte, 5)
	rand.Read(b)
	return strings.ToUpper(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
}

func buildSMSKey(phone string) string {
	return fmt.Sprintf("sms:otp:%s", phone)
}
func isValidPhone(phone string) bool {
	return len(strings.TrimSpace(phone)) >= 6
}

func maskPhone(phone string) string {
	if len(phone) < 7 {
		return phone
	}
	return phone[:3] + "****" + phone[len(phone)-3:]
}

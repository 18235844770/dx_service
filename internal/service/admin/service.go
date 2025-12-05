package admin

import (
	"context"
	"strings"
	"time"

	"dx-service/internal/config"
	"dx-service/internal/model"
	pkgAuth "dx-service/pkg/auth"
	appErr "dx-service/pkg/errors"
	"dx-service/pkg/logger"

	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type Service struct {
	db *gorm.DB
}

type LoginResult struct {
	Token    string    `json:"token"`
	ExpireAt time.Time `json:"expireAt"`
	Admin    AdminInfo `json:"admin"`
}

type AdminInfo struct {
	ID          int64      `json:"id"`
	Username    string     `json:"username"`
	DisplayName string     `json:"displayName"`
	Status      string     `json:"status"`
	LastLoginAt *time.Time `json:"lastLoginAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db}
}

func (s *Service) Login(ctx context.Context, username, password string) (*LoginResult, error) {
	username = strings.TrimSpace(username)
	password = strings.TrimSpace(password)
	if username == "" || password == "" {
		return nil, appErr.ErrInvalidAdminPassword
	}

	var admin model.Admin
	if err := s.db.WithContext(ctx).Where("username = ?", username).First(&admin).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, appErr.ErrAdminNotFound
		}
		return nil, err
	}
	if !strings.EqualFold(admin.Status, "active") {
		return nil, appErr.ErrAdminDisabled
	}
	if err := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(password)); err != nil {
		return nil, appErr.ErrInvalidAdminPassword
	}

	token, err := pkgAuth.GenerateAdminToken(admin.ID)
	if err != nil {
		return nil, err
	}
	expireAt := time.Now().Add(time.Duration(config.GlobalConfig.JWT.Expire) * time.Hour)

	now := time.Now()
	if err := s.db.WithContext(ctx).
		Model(&admin).
		Updates(map[string]interface{}{
			"last_login_at": now,
			"updated_at":    now,
		}).Error; err != nil {
		return nil, err
	}

	return &LoginResult{
		Token:    token,
		ExpireAt: expireAt,
		Admin:    sanitizeAdmin(admin),
	}, nil
}

func (s *Service) EnsureDefaultAdmin(ctx context.Context) error {
	cfg := config.GlobalConfig.Admin
	if cfg.DefaultUsername == "" || cfg.DefaultPassword == "" {
		logger.Log.Warn("default admin credentials not configured; skipping bootstrap")
		return nil
	}

	var exists int64
	if err := s.db.WithContext(ctx).
		Model(&model.Admin{}).
		Where("username = ?", cfg.DefaultUsername).
		Count(&exists).Error; err != nil {
		return err
	}
	if exists > 0 {
		return nil
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(cfg.DefaultPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	admin := model.Admin{
		Username:     cfg.DefaultUsername,
		PasswordHash: string(hash),
		DisplayName:  cfg.DefaultUsername,
		Status:       "active",
	}
	if err := s.db.WithContext(ctx).Create(&admin).Error; err != nil {
		return err
	}
	logger.Log.Info("default admin account created",
		zap.String("username", cfg.DefaultUsername))
	return nil
}

func sanitizeAdmin(admin model.Admin) AdminInfo {
	return AdminInfo{
		ID:          admin.ID,
		Username:    admin.Username,
		DisplayName: admin.DisplayName,
		Status:      admin.Status,
		LastLoginAt: admin.LastLoginAt,
		CreatedAt:   admin.CreatedAt,
	}
}

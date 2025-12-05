package user

import (
	"context"
	"strings"
	"time"

	"dx-service/internal/model"
	appErr "dx-service/pkg/errors"
	"dx-service/pkg/logger"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

const (
	defaultAdminUserPageSize = 20
	maxAdminUserPageSize     = 100
)

type Service struct {
	db *gorm.DB
}

type UpdateProfileRequest struct {
	Nickname     *string
	Avatar       *string
	LocationCity *string
	GPSLat       *float64
	GPSLng       *float64
}

type AdminListUsersFilter struct {
	Page         int
	Size         int
	Status       string
	PhoneKeyword string
	InviteCode   string
	AgentID      *int64
}

type AdminListUsersResult struct {
	Items []model.User
	Total int64
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db}
}

func (f *AdminListUsersFilter) sanitize() {
	if f.Page <= 0 {
		f.Page = 1
	}
	if f.Size <= 0 {
		f.Size = defaultAdminUserPageSize
	}
	if f.Size > maxAdminUserPageSize {
		f.Size = maxAdminUserPageSize
	}
	f.Status = strings.ToLower(strings.TrimSpace(f.Status))
	f.PhoneKeyword = strings.TrimSpace(f.PhoneKeyword)
	f.InviteCode = strings.TrimSpace(f.InviteCode)
}

func applyAdminUserFilters(db *gorm.DB, filter AdminListUsersFilter) *gorm.DB {
	if filter.Status != "" {
		db = db.Where("LOWER(status) = ?", filter.Status)
	}
	if filter.PhoneKeyword != "" {
		like := "%" + filter.PhoneKeyword + "%"
		db = db.Where("phone LIKE ?", like)
	}
	if filter.InviteCode != "" {
		like := "%" + filter.InviteCode + "%"
		db = db.Where("invite_code LIKE ?", like)
	}
	if filter.AgentID != nil {
		db = db.Where("bind_agent_id = ?", *filter.AgentID)
	}
	return db
}

func (s *Service) GetProfile(ctx context.Context, userID int64) (*model.User, error) {
	var user model.User
	if err := s.db.WithContext(ctx).First(&user, userID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

func (s *Service) UpdateProfile(ctx context.Context, userID int64, req UpdateProfileRequest) (*model.User, error) {
	updates := map[string]interface{}{}
	if req.Nickname != nil {
		updates["nickname"] = *req.Nickname
	}
	if req.Avatar != nil {
		updates["avatar"] = *req.Avatar
	}
	if req.LocationCity != nil {
		updates["location_city"] = *req.LocationCity
	}
	if req.GPSLat != nil {
		updates["gps_lat"] = *req.GPSLat
	}
	if req.GPSLng != nil {
		updates["gps_lng"] = *req.GPSLng
	}

	if len(updates) > 0 {
		if err := s.db.WithContext(ctx).Model(&model.User{}).Where("id = ?", userID).Updates(updates).Error; err != nil {
			return nil, err
		}
	}

	return s.GetProfile(ctx, userID)
}

func (s *Service) AdminListUsers(ctx context.Context, filter AdminListUsersFilter) (*AdminListUsersResult, error) {
	filter.sanitize()

	countQuery := applyAdminUserFilters(s.db.WithContext(ctx).Model(&model.User{}), filter)
	var total int64
	if err := countQuery.Count(&total).Error; err != nil {
		return nil, err
	}

	result := &AdminListUsersResult{
		Items: make([]model.User, 0),
		Total: total,
	}
	if total == 0 {
		return result, nil
	}

	dataQuery := applyAdminUserFilters(s.db.WithContext(ctx).Model(&model.User{}), filter)
	if err := dataQuery.
		Order("id DESC").
		Limit(filter.Size).
		Offset((filter.Page - 1) * filter.Size).
		Find(&result.Items).Error; err != nil {
		return nil, err
	}

	return result, nil
}

func (s *Service) AdminGetUser(ctx context.Context, userID int64) (*model.User, error) {
	var user model.User
	if err := s.db.WithContext(ctx).First(&user, userID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, appErr.ErrUserNotFound
		}
		return nil, err
	}
	return &user, nil
}

func (s *Service) AdminUpdateUserStatus(ctx context.Context, userID int64, status, reason string) (*model.User, error) {
	status = strings.ToLower(strings.TrimSpace(status))
	if status != "normal" && status != "banned" {
		return nil, appErr.ErrInvalidUserStatus
	}
	reason = strings.TrimSpace(reason)

	now := time.Now()
	res := s.db.WithContext(ctx).Model(&model.User{}).
		Where("id = ?", userID).
		Updates(map[string]interface{}{
			"status":     status,
			"updated_at": now,
		})
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, appErr.ErrUserNotFound
	}

	logger.Log.Info("admin updated user status",
		zap.Int64("userID", userID),
		zap.String("status", status),
		zap.String("reason", reason))

	return s.AdminGetUser(ctx, userID)
}

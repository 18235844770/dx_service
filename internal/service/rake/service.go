package rake

import (
	"context"
	"strings"
	"time"

	"dx-service/internal/model"
	appErr "dx-service/pkg/errors"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type Service struct {
	db *gorm.DB
}

type ListResult struct {
	Items []model.RakeRule
	Total int64
}

type MutationParams struct {
	ID          int64
	Name        string
	Type        string
	Remark      string
	Status      string
	ConfigJSON  []byte
	EffectiveAt *time.Time
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db}
}

func (s *Service) List(ctx context.Context, page, size int) (*ListResult, error) {
	if page < 1 {
		page = 1
	}
	if size <= 0 {
		size = 20
	}
	if size > 100 {
		size = 100
	}

	var total int64
	if err := s.db.WithContext(ctx).
		Model(&model.RakeRule{}).
		Count(&total).Error; err != nil {
		return nil, err
	}

	var items []model.RakeRule
	if total > 0 {
		offset := (page - 1) * size
		if err := s.db.WithContext(ctx).
			Model(&model.RakeRule{}).
			Order("id DESC").
			Limit(size).
			Offset(offset).
			Find(&items).Error; err != nil {
			return nil, err
		}
	}

	return &ListResult{Items: items, Total: total}, nil
}

func (s *Service) Create(ctx context.Context, params MutationParams) (*model.RakeRule, error) {
	rule := model.RakeRule{
		Name:        strings.TrimSpace(params.Name),
		Type:        strings.ToLower(params.Type),
		Remark:      strings.TrimSpace(params.Remark),
		Status:      params.Status,
		ConfigJSON:  datatypes.JSON(params.ConfigJSON),
		EffectiveAt: params.EffectiveAt,
	}
	if err := s.db.WithContext(ctx).Create(&rule).Error; err != nil {
		return nil, err
	}
	return &rule, nil
}

func (s *Service) Update(ctx context.Context, id int64, params MutationParams) (*model.RakeRule, error) {
	updates := map[string]interface{}{
		"name":         strings.TrimSpace(params.Name),
		"type":         strings.ToLower(params.Type),
		"remark":       strings.TrimSpace(params.Remark),
		"status":       params.Status,
		"config_json":  datatypes.JSON(params.ConfigJSON),
		"effective_at": params.EffectiveAt,
	}

	result := s.db.WithContext(ctx).
		Model(&model.RakeRule{}).
		Where("id = ?", id).
		Updates(updates)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, appErr.ErrRakeRuleNotFound
	}

	var rule model.RakeRule
	if err := s.db.WithContext(ctx).First(&rule, id).Error; err != nil {
		return nil, err
	}
	return &rule, nil
}

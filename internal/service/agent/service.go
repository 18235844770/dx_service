package agent

import (
	"context"
	"fmt"

	"dx-service/internal/model"
	appErr "dx-service/pkg/errors"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

const (
	defaultPageSize = 20
	maxPageSize     = 100
)

type Service struct {
	db *gorm.DB
}

type ListResult struct {
	Items []model.AgentRule
	Total int64
}

type MutationParams struct {
	MaxLevel          int
	LevelRatiosJSON   []byte
	BasePlatformRatio float64
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db}
}

func normalizePagination(page, size int) (int, int) {
	if page < 1 {
		page = 1
	}
	if size <= 0 {
		size = defaultPageSize
	}
	if size > maxPageSize {
		size = maxPageSize
	}
	return page, size
}

func (s *Service) List(ctx context.Context, page, size int) (*ListResult, error) {
	page, size = normalizePagination(page, size)

	var total int64
	if err := s.db.WithContext(ctx).Model(&model.AgentRule{}).Count(&total).Error; err != nil {
		return nil, err
	}

	result := &ListResult{
		Items: make([]model.AgentRule, 0),
		Total: total,
	}
	if total == 0 {
		return result, nil
	}

	offset := (page - 1) * size
	if err := s.db.WithContext(ctx).
		Model(&model.AgentRule{}).
		Order("id DESC").
		Limit(size).
		Offset(offset).
		Find(&result.Items).Error; err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) Create(ctx context.Context, params MutationParams) (*model.AgentRule, error) {
	if err := validateMutationParams(params); err != nil {
		return nil, err
	}

	rule := model.AgentRule{
		MaxLevel:          params.MaxLevel,
		LevelRatiosJSON:   datatypes.JSON(params.LevelRatiosJSON),
		BasePlatformRatio: params.BasePlatformRatio,
	}
	if err := s.db.WithContext(ctx).Create(&rule).Error; err != nil {
		return nil, err
	}
	return &rule, nil
}

func (s *Service) Update(ctx context.Context, id int64, params MutationParams) (*model.AgentRule, error) {
	if err := validateMutationParams(params); err != nil {
		return nil, err
	}

	result := s.db.WithContext(ctx).
		Model(&model.AgentRule{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"max_level":            params.MaxLevel,
			"level_ratios_json":    datatypes.JSON(params.LevelRatiosJSON),
			"base_platform_ratio":  params.BasePlatformRatio,
		})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, appErr.ErrAgentRuleNotFound
	}

	var rule model.AgentRule
	if err := s.db.WithContext(ctx).First(&rule, id).Error; err != nil {
		return nil, err
	}
	return &rule, nil
}

func validateMutationParams(params MutationParams) error {
	if params.MaxLevel <= 0 {
		return fmt.Errorf("%w: maxLevel must be greater than zero", appErr.ErrInvalidAgentRule)
	}
	if params.BasePlatformRatio < 0 || params.BasePlatformRatio > 1 {
		return fmt.Errorf("%w: basePlatformRatio must be between 0 and 1", appErr.ErrInvalidAgentRule)
	}
	if len(params.LevelRatiosJSON) == 0 {
		return fmt.Errorf("%w: levelRatiosJson is required", appErr.ErrInvalidAgentRule)
	}
	return nil
}


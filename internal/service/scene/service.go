package scene

import (
	"context"

	"dx-service/internal/model"
	appErr "dx-service/pkg/errors"
	"dx-service/pkg/logger"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

type Service struct {
	db *gorm.DB
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db}
}

type SceneListResult struct {
	Items []model.Scene
	Total int64
}

type SceneMutationParams struct {
	Name               string
	SeatCount          int
	MinIn              int64
	MaxIn              int64
	BasePi             int64
	MinUnitPi          int64
	MangoEnabled       bool
	BoboEnabled        bool
	DistanceThresholdM int
	Status             string
	RakeRuleID         int64
}

func (s *Service) ListScenes(ctx context.Context) ([]model.Scene, error) {
	var scenes []model.Scene
	if err := s.db.WithContext(ctx).Find(&scenes).Error; err != nil {
		return nil, err
	}
	return scenes, nil
}

func (s *Service) AdminListScenes(ctx context.Context, page, size int) (*SceneListResult, error) {
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
		Model(&model.Scene{}).
		Count(&total).Error; err != nil {
		return nil, err
	}

	var scenes []model.Scene
	if total > 0 {
		offset := (page - 1) * size
		if err := s.db.WithContext(ctx).
			Model(&model.Scene{}).
			Order("id DESC").
			Limit(size).
			Offset(offset).
			Find(&scenes).Error; err != nil {
			return nil, err
		}
	}

	return &SceneListResult{
		Items: scenes,
		Total: total,
	}, nil
}

func (s *Service) CreateScene(ctx context.Context, params SceneMutationParams) (*model.Scene, error) {
	scene := model.Scene{
		Name:               params.Name,
		SeatCount:          params.SeatCount,
		MinIn:              params.MinIn,
		MaxIn:              params.MaxIn,
		BasePi:             params.BasePi,
		MinUnitPi:          params.MinUnitPi,
		MangoEnabled:       params.MangoEnabled,
		BoboEnabled:        params.BoboEnabled,
		DistanceThresholdM: params.DistanceThresholdM,
		Status:             params.Status,
		RakeRuleID:         params.RakeRuleID,
	}
	if err := s.db.WithContext(ctx).Create(&scene).Error; err != nil {
		return nil, err
	}
	return &scene, nil
}

func (s *Service) UpdateScene(ctx context.Context, id int64, params SceneMutationParams) (*model.Scene, error) {
	updates := map[string]interface{}{
		"name":                 params.Name,
		"seat_count":           params.SeatCount,
		"min_in":               params.MinIn,
		"max_in":               params.MaxIn,
		"base_pi":              params.BasePi,
		"min_unit_pi":          params.MinUnitPi,
		"mango_enabled":        params.MangoEnabled,
		"bobo_enabled":         params.BoboEnabled,
		"distance_threshold_m": params.DistanceThresholdM,
		"status":               params.Status,
		"rake_rule_id":         params.RakeRuleID,
	}

	result := s.db.WithContext(ctx).
		Model(&model.Scene{}).
		Where("id = ?", id).
		Updates(updates)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, appErr.ErrSceneNotFound
	}

	var scene model.Scene
	if err := s.db.WithContext(ctx).First(&scene, id).Error; err != nil {
		return nil, err
	}
	return &scene, nil
}

func (s *Service) GetScene(ctx context.Context, id int64) (*model.Scene, error) {
	var scene model.Scene
	if err := s.db.WithContext(ctx).First(&scene, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		logger.Log.Error("failed to load scene", zap.Error(err))
		return nil, err
	}
	return &scene, nil
}

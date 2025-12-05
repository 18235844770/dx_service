package scene_test

import (
	"context"
	"testing"

	"dx-service/internal/model"
	"dx-service/internal/service/scene"
	appErr "dx-service/pkg/errors"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newSceneService(t *testing.T) (*gorm.DB, *scene.Service) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.Scene{}); err != nil {
		t.Fatalf("failed to migrate scene model: %v", err)
	}

	return db, scene.NewService(db)
}

func TestCreateScene(t *testing.T) {
	ctx := context.Background()
	_, svc := newSceneService(t)

	created, err := svc.CreateScene(ctx, scene.SceneMutationParams{
		Name:               "测试场",
		SeatCount:          6,
		MinIn:              1000,
		MaxIn:              5000,
		BasePi:             100,
		MinUnitPi:          20,
		MangoEnabled:       true,
		BoboEnabled:        false,
		DistanceThresholdM: 1000,
		RakeRuleID:         1,
	})
	if err != nil {
		t.Fatalf("create scene failed: %v", err)
	}
	if created.ID == 0 || created.Name != "测试场" {
		t.Fatalf("unexpected scene result: %+v", created)
	}
}

func TestAdminListScenes(t *testing.T) {
	ctx := context.Background()
	db, svc := newSceneService(t)

	scenes := []model.Scene{
		{Name: "A", SeatCount: 6, MinIn: 100, MaxIn: 1000, BasePi: 10, MinUnitPi: 2, DistanceThresholdM: 100, RakeRuleID: 1},
		{Name: "B", SeatCount: 6, MinIn: 100, MaxIn: 1000, BasePi: 10, MinUnitPi: 2, DistanceThresholdM: 100, RakeRuleID: 1},
		{Name: "C", SeatCount: 6, MinIn: 100, MaxIn: 1000, BasePi: 10, MinUnitPi: 2, DistanceThresholdM: 100, RakeRuleID: 1},
	}
	if err := db.WithContext(ctx).Create(&scenes).Error; err != nil {
		t.Fatalf("seed scenes failed: %v", err)
	}

	result, err := svc.AdminListScenes(ctx, 1, 2)
	if err != nil {
		t.Fatalf("list scenes failed: %v", err)
	}
	if result.Total != 3 {
		t.Fatalf("expected total=3, got %d", result.Total)
	}
	if len(result.Items) != 2 {
		t.Fatalf("expected page size 2, got %d", len(result.Items))
	}
}

func TestUpdateSceneNotFound(t *testing.T) {
	ctx := context.Background()
	_, svc := newSceneService(t)

	_, err := svc.UpdateScene(ctx, 999, scene.SceneMutationParams{
		Name:               "missing",
		SeatCount:          6,
		MinIn:              100,
		MaxIn:              1000,
		BasePi:             10,
		MinUnitPi:          2,
		DistanceThresholdM: 100,
		RakeRuleID:         1,
	})
	if err == nil || err != appErr.ErrSceneNotFound {
		t.Fatalf("expected ErrSceneNotFound, got %v", err)
	}
}

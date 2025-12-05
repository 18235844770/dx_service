package rake_test

import (
	"context"
	"encoding/json"
	"testing"

	"dx-service/internal/model"
	"dx-service/internal/service/rake"
	appErr "dx-service/pkg/errors"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newService(t *testing.T) (*gorm.DB, *rake.Service) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.RakeRule{}); err != nil {
		t.Fatalf("failed to migrate rake rules: %v", err)
	}
	return db, rake.NewService(db)
}

func mustJSON(t *testing.T, v interface{}) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("failed to marshal json: %v", err)
	}
	return data
}

func TestCreateRakeRule(t *testing.T) {
	ctx := context.Background()
	_, svc := newService(t)

	payload := mustJSON(t, map[string]any{"ratio": 0.05, "cap": 1000})
	rule, err := svc.Create(ctx, rake.MutationParams{
		Type:       "ratio",
		ConfigJSON: payload,
	})
	if err != nil {
		t.Fatalf("create rake rule failed: %v", err)
	}
	if rule.ID == 0 || rule.Type != "ratio" {
		t.Fatalf("unexpected rule: %+v", rule)
	}
}

func TestListRakeRules(t *testing.T) {
	ctx := context.Background()
	db, svc := newService(t)

	rules := []model.RakeRule{
		{Type: "ratio", ConfigJSON: mustJSON(t, map[string]any{"ratio": 0.05})},
		{Type: "fixed", ConfigJSON: mustJSON(t, map[string]any{"amount": 100})},
	}
	if err := db.WithContext(ctx).Create(&rules).Error; err != nil {
		t.Fatalf("failed to seed rules: %v", err)
	}

	result, err := svc.List(ctx, 1, 1)
	if err != nil {
		t.Fatalf("list rake rules failed: %v", err)
	}
	if result.Total != 2 {
		t.Fatalf("expected total=2, got %d", result.Total)
	}
	if len(result.Items) != 1 {
		t.Fatalf("expected page size 1, got %d", len(result.Items))
	}
}

func TestUpdateRakeRuleNotFound(t *testing.T) {
	ctx := context.Background()
	_, svc := newService(t)

	_, err := svc.Update(ctx, 123, rake.MutationParams{
		Type:       "ratio",
		ConfigJSON: mustJSON(t, map[string]any{"ratio": 0.05}),
	})
	if err == nil || err != appErr.ErrRakeRuleNotFound {
		t.Fatalf("expected ErrRakeRuleNotFound, got %v", err)
	}
}

package admin_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"dx-service/internal/config"
	"dx-service/internal/model"
	adminsvc "dx-service/internal/service/admin"
	appErr "dx-service/pkg/errors"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newTestService(t *testing.T) (*gorm.DB, *adminsvc.Service) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	if err := db.AutoMigrate(&model.Admin{}); err != nil {
		t.Fatalf("failed to migrate admin model: %v", err)
	}

	config.GlobalConfig = &config.Config{
		JWT: config.JWTConfig{
			Secret: "test-secret",
			Expire: 1,
		},
		Admin: config.AdminSeedConfig{
			DefaultUsername: "bootstrap",
			DefaultPassword: "Bootstrap@123",
		},
	}

	return db, adminsvc.NewService(db)
}

func createAdmin(t *testing.T, db *gorm.DB, username, password, status string) *model.Admin {
	t.Helper()

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("failed to hash password: %v", err)
	}

	admin := &model.Admin{
		Username:     username,
		PasswordHash: string(hash),
		DisplayName:  "Tester",
		Status:       status,
	}
	if err := db.Create(admin).Error; err != nil {
		t.Fatalf("failed to insert admin: %v", err)
	}
	return admin
}

func TestLoginSuccess(t *testing.T) {
	db, svc := newTestService(t)
	record := createAdmin(t, db, "root", "Secret@123", "active")

	resp, err := svc.Login(context.Background(), "root", "Secret@123")
	if err != nil {
		t.Fatalf("expected login to succeed, got error: %v", err)
	}
	if resp.Token == "" {
		t.Fatalf("expected token in response")
	}
	if resp.Admin.ID != record.ID {
		t.Fatalf("expected admin id %d, got %d", record.ID, resp.Admin.ID)
	}

	var stored model.Admin
	if err := db.First(&stored, record.ID).Error; err != nil {
		t.Fatalf("failed to reload admin: %v", err)
	}
	if stored.LastLoginAt == nil {
		t.Fatalf("expected last_login_at to be updated")
	}
	if stored.LastLoginAt.Before(time.Now().Add(-5 * time.Minute)) {
		t.Fatalf("unexpected last login timestamp: %v", stored.LastLoginAt)
	}
}

func TestLoginInvalidPassword(t *testing.T) {
	db, svc := newTestService(t)
	createAdmin(t, db, "root", "Secret@123", "active")

	_, err := svc.Login(context.Background(), "root", "wrong-password")
	if !errors.Is(err, appErr.ErrInvalidAdminPassword) {
		t.Fatalf("expected invalid password error, got: %v", err)
	}
}

func TestLoginDisabledAdmin(t *testing.T) {
	db, svc := newTestService(t)
	createAdmin(t, db, "root", "Secret@123", "disabled")

	_, err := svc.Login(context.Background(), "root", "Secret@123")
	if !errors.Is(err, appErr.ErrAdminDisabled) {
		t.Fatalf("expected disabled error, got: %v", err)
	}
}

func TestLoginAdminNotFound(t *testing.T) {
	_, svc := newTestService(t)

	_, err := svc.Login(context.Background(), "ghost", "whatever")
	if !errors.Is(err, appErr.ErrAdminNotFound) {
		t.Fatalf("expected not found error, got: %v", err)
	}
}

func TestEnsureDefaultAdmin(t *testing.T) {
	db, svc := newTestService(t)

	ctx := context.Background()
	if err := svc.EnsureDefaultAdmin(ctx); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}

	var count int64
	if err := db.Model(&model.Admin{}).
		Where("username = ?", config.GlobalConfig.Admin.DefaultUsername).
		Count(&count).Error; err != nil {
		t.Fatalf("failed to count admins: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 default admin, got %d", count)
	}

	// Running bootstrap again should be idempotent.
	if err := svc.EnsureDefaultAdmin(ctx); err != nil {
		t.Fatalf("second bootstrap failed: %v", err)
	}
	if err := db.Model(&model.Admin{}).
		Where("username = ?", config.GlobalConfig.Admin.DefaultUsername).
		Count(&count).Error; err != nil {
		t.Fatalf("failed to count admins: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected idempotent bootstrap, got %d admins", count)
	}
}

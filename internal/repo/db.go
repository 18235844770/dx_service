package repo

import (
	"log"
	"os"

	"dx-service/internal/config"
	"dx-service/internal/model"
	"dx-service/pkg/logger"

	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var DB *gorm.DB

func InitDB() {
	dsn := config.GlobalConfig.Database.DSN
	var err error
	DB, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		logger.Log.Fatal("Failed to connect to database",
			zap.Error(err),
		)
	}

	models := []interface{}{
		&model.Admin{},
		&model.Agent{},
		&model.AgentProfitLog{},
		&model.Wallet{},
		&model.RechargeOrder{},
		&model.BillingLog{},
		&model.Scene{},
		&model.RakeRule{},
		&model.AgentRule{},
		&model.Table{},
		&model.Match{},
		&model.MatchRoundLog{},
	}

	if os.Getenv("SKIP_USER_MIGRATE") != "1" {
		models = append(models, &model.User{})
	}

	err = DB.AutoMigrate(models...)
	if err != nil {
		log.Fatalf("Failed to migrate database: %v", err)
	}
}

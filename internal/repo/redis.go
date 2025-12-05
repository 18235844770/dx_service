package repo

import (
	"context"
	"dx-service/internal/config"
	"dx-service/pkg/logger"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

var RDB *redis.Client

func InitRedis() {
	conf := config.GlobalConfig.Redis
	RDB = redis.NewClient(&redis.Options{
		Addr:     conf.Addr,
		Password: conf.Password,
		DB:       conf.DB,
	})

	_, err := RDB.Ping(context.Background()).Result()
	if err != nil {
		logger.Log.Fatal("Failed to connect to Redis", zap.Error(err))
	}
}

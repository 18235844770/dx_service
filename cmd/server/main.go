package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"time"

	"dx-service/internal/api"
	"dx-service/internal/config"
	"dx-service/internal/repo"
	"dx-service/internal/service"
	"dx-service/pkg/logger"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "config.yaml", "path to config file")
	flag.Parse()

	rand.Seed(time.Now().UnixNano())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Load Config
	config.LoadConfig(configPath)

	// 2. Init Logger
	logger.InitLogger(config.GlobalConfig.Server.Mode)
	defer logger.Log.Sync()

	logger.Log.Info("Starting server...", zap.String("mode", config.GlobalConfig.Server.Mode))

	// 3. Init DB & Redis
	repo.InitDB()
	repo.InitRedis()

	// 3.5 Init Services
	services := service.NewContainer(repo.DB, repo.RDB)
	if err := services.Start(ctx); err != nil {
		logger.Log.Fatal("failed to start services", zap.Error(err))
	}

	// 4. Init Router
	if config.GlobalConfig.Server.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.Default()

	// Register Routes
	api.RegisterRoutes(r, services)

	// 5. Start Server
	addr := fmt.Sprintf(":%s", config.GlobalConfig.Server.Port)
	logger.Log.Info("Server listening", zap.String("addr", addr))
	if err := r.Run(addr); err != nil {
		logger.Log.Fatal("Server failed to start", zap.Error(err))
	}
}

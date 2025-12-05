package logger

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"os"
)

var Log *zap.Logger

func InitLogger(mode string) {
	var config zap.Config

	if mode == "release" {
		config = zap.NewProductionConfig()
	} else {
		config = zap.NewDevelopmentConfig()
		config.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}

	config.OutputPaths = []string{"stdout"}
	// Ensure atomic level is handled if we want dynamic level changing, but simple for now
	var err error
	Log, err = config.Build()
	if err != nil {
		os.Exit(1)
	}
	zap.ReplaceGlobals(Log)
}

package config

import (
	"log"

	"github.com/spf13/viper"
)

type Config struct {
	Server   ServerConfig    `mapstructure:"server"`
	Database DatabaseConfig  `mapstructure:"database"`
	Redis    RedisConfig     `mapstructure:"redis"`
	JWT      JWTConfig       `mapstructure:"jwt"`
	Features FeatureConfig   `mapstructure:"features"`
	Admin    AdminSeedConfig `mapstructure:"admin"`
}

type ServerConfig struct {
	Port string `mapstructure:"port"`
	Mode string `mapstructure:"mode"` // debug, release
}

type DatabaseConfig struct {
	DSN string `mapstructure:"dsn"`
}

type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

type JWTConfig struct {
	Secret string `mapstructure:"secret"`
	Expire int    `mapstructure:"expire"` // hours
}

type FeatureConfig struct {
	SkipLocationValidation bool `mapstructure:"skipLocationValidation"`
}

type AdminSeedConfig struct {
	DefaultUsername string `mapstructure:"defaultUsername"`
	DefaultPassword string `mapstructure:"defaultPassword"`
}

var GlobalConfig *Config

func LoadConfig(path string) {
	viper.SetConfigFile(path)
	viper.SetConfigType("yaml")

	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Error reading config file, %s", err)
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		log.Fatalf("Unable to decode into struct, %v", err)
	}
	GlobalConfig = &cfg
}

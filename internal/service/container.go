package service

import (
	"context"

	"dx-service/internal/service/admin"
	"dx-service/internal/service/agent"
	"dx-service/internal/service/auth"
	"dx-service/internal/service/game"
	"dx-service/internal/service/match"
	"dx-service/internal/service/rake"
	"dx-service/internal/service/scene"
	"dx-service/internal/service/user"
	"dx-service/internal/service/wallet"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type Container struct {
	Match  *match.Service
	Game   *game.Service
	Scene  *scene.Service
	Rake   *rake.Service
	Agent  *agent.Service
	Auth   *auth.Service
	User   *user.Service
	Wallet *wallet.Service
	Admin  *admin.Service
}

func NewContainer(db *gorm.DB, rdb *redis.Client) *Container {
	return &Container{
		Admin:  admin.NewService(db),
		Agent:  agent.NewService(db),
		Auth:   auth.NewService(db, rdb),
		Match:  match.NewService(db, rdb),
		Game:   game.NewService(db),
		Rake:   rake.NewService(db),
		Scene:  scene.NewService(db),
		User:   user.NewService(db),
		Wallet: wallet.NewService(db),
	}
}

func (c *Container) Start(ctx context.Context) error {
	if err := c.Admin.EnsureDefaultAdmin(ctx); err != nil {
		return err
	}
	return c.Match.Start(ctx)
}

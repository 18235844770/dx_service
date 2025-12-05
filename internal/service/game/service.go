package game

import "gorm.io/gorm"

// Service encapsulates game-specific workflows such as settlement.
type Service struct {
	db *gorm.DB
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db}
}

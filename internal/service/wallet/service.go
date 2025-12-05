package wallet

import (
	"context"
	"fmt"
	"time"

	"dx-service/internal/model"
	appErr "dx-service/pkg/errors"

	"gorm.io/gorm"
)

type Service struct {
	db *gorm.DB
}

type AdminSetWalletRequest struct {
	BalanceAvailable *int64
	BalanceFrozen    *int64
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db}
}

func (s *Service) GetWallet(ctx context.Context, userID int64) (*model.Wallet, error) {
	var wallet model.Wallet
	err := s.db.WithContext(ctx).Where("user_id = ?", userID).First(&wallet).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return &model.Wallet{UserID: userID}, nil
		}
		return nil, err
	}
	return &wallet, nil
}

func (s *Service) AdminSetWallet(ctx context.Context, userID int64, req AdminSetWalletRequest) (*model.Wallet, error) {
	if req.BalanceAvailable == nil && req.BalanceFrozen == nil {
		return nil, fmt.Errorf("%w: balanceAvailable or balanceFrozen is required", appErr.ErrInvalidWalletPayload)
	}

	var wallet model.Wallet
	if err := s.db.WithContext(ctx).Where("user_id = ?", userID).FirstOrCreate(&wallet, model.Wallet{UserID: userID}).Error; err != nil {
		return nil, err
	}

	if req.BalanceAvailable != nil {
		if *req.BalanceAvailable < 0 {
			return nil, fmt.Errorf("%w: balanceAvailable must be >= 0", appErr.ErrInvalidWalletPayload)
		}
		wallet.BalanceAvailable = *req.BalanceAvailable
	}
	if req.BalanceFrozen != nil {
		if *req.BalanceFrozen < 0 {
			return nil, fmt.Errorf("%w: balanceFrozen must be >= 0", appErr.ErrInvalidWalletPayload)
		}
		wallet.BalanceFrozen = *req.BalanceFrozen
	}
	wallet.BalanceTotal = wallet.BalanceAvailable + wallet.BalanceFrozen
	wallet.UpdatedAt = time.Now()

	if err := s.db.WithContext(ctx).Save(&wallet).Error; err != nil {
		return nil, err
	}
	return &wallet, nil
}

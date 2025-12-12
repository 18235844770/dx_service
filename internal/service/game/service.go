package game

import (
	"context"
	"sync"

	"dx-service/internal/model"
	appErr "dx-service/pkg/errors"

	"gorm.io/gorm"
)

// Service encapsulates game-specific workflows such as settlement and live table runtime.
type Service struct {
	db       *gorm.DB
	runtimes sync.Map // tableID -> *TableRuntime
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db}
}

// FinalizeMatch is a helper to settle by matchID and update DB/table status.
func (s *Service) FinalizeMatch(ctx context.Context, matchID int64, results SettlementRequest) error {
	if matchID == 0 {
		return appErr.ErrMatchNotFound
	}
	results.MatchID = matchID
	return s.SettleMatch(ctx, results)
}

func (s *Service) handleRuntimeFinish(rt *TableRuntime) {
	ctx := context.Background()

	match, err := s.loadActiveMatch(ctx, rt.tableID)
	if err != nil || match == nil {
		return
	}

	playerIDs := rt.playersSnapshot()
	results := make([]PlayerResult, 0, len(playerIDs))

	if len(rt.SettlementResults) > 0 {
		results = rt.SettlementResults
	} else {
		// Fallback for empty/aborted games
		for _, id := range playerIDs {
			results = append(results, PlayerResult{
				UserID:    id,
				NetPoints: 0,
				Meta: map[string]interface{}{
					"reason": "auto_settle_no_scores",
				},
			})
		}
	}

	req := SettlementRequest{
		MatchID: match.ID,
		SceneID: match.SceneID,
		Results: results,
	}
	if err := s.SettleMatch(ctx, req); err != nil {
		return
	}
	// Update table streak for next match
	_ = s.db.WithContext(ctx).
		Model(&model.Table{}).
		Where("id = ?", rt.tableID).
		Update("mango_streak", rt.mangoStreak).Error
}

func (s *Service) loadActiveMatch(ctx context.Context, tableID int64) (*model.Match, error) {
	var matches []model.Match
	err := s.db.WithContext(ctx).
		Where("table_id = ? AND ended_at IS NULL", tableID).
		Order("id DESC").
		Limit(1).
		Find(&matches).Error
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, nil
	}
	return &matches[0], nil
}

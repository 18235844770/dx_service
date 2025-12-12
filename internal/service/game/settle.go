package game

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"dx-service/internal/model"
	appErr "dx-service/pkg/errors"

	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type SettlementRequest struct {
	MatchID int64
	SceneID int64
	Results []PlayerResult
}

type PlayerResult struct {
	UserID    int64
	NetPoints int64
	Meta      map[string]interface{}
}

type playerResultRecord struct {
	UserID    int64                  `json:"userId"`
	NetPoints int64                  `json:"netPoints"`
	Rake      int64                  `json:"rake"`
	Meta      map[string]interface{} `json:"meta,omitempty"`
}

type agentShareRecord struct {
	AgentID int64 `json:"agentId"`
	Level   int   `json:"level"`
	Amount  int64 `json:"amount"`
}

type rakeSummary struct {
	Total    int64              `json:"total"`
	Platform int64              `json:"platform"`
	Agents   []agentShareRecord `json:"agents"`
}

func (s *Service) SettleMatch(ctx context.Context, req SettlementRequest) error {
	if req.MatchID == 0 || len(req.Results) == 0 {
		return appErr.ErrSettlementValidation
	}

	var balanceSum int64
	for _, r := range req.Results {
		if r.UserID == 0 {
			return appErr.ErrSettlementValidation
		}
		balanceSum += r.NetPoints
	}
	if balanceSum != 0 {
		return fmt.Errorf("%w: net points must sum to zero", appErr.ErrSettlementValidation)
	}

	now := time.Now()

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var match model.Match
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&match, req.MatchID).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return appErr.ErrMatchNotFound
			}
			return err
		}

		if match.EndedAt != nil {
			return appErr.ErrMatchAlreadySettled
		}
		if req.SceneID != 0 && match.SceneID != req.SceneID {
			return fmt.Errorf("scene mismatch: %w", appErr.ErrSceneNotFound)
		}

		var scene model.Scene
		if err := tx.First(&scene, match.SceneID).Error; err != nil {
			return err
		}

		var rakeRule *model.RakeRule
		if scene.RakeRuleID != 0 {
			var rule model.RakeRule
			if err := tx.First(&rule, scene.RakeRuleID).Error; err != nil {
				return err
			}
			rakeRule = &rule
		}

		agentRule, err := s.loadAgentRule(tx)
		if err != nil {
			return err
		}

		wallets := newWalletBook(tx)
		billingLogs := make([]model.BillingLog, 0, len(req.Results)*3)
		agentLogs := make([]model.AgentProfitLog, 0)
		resultRecords := make([]playerResultRecord, 0, len(req.Results))
		agentShareRecords := make([]agentShareRecord, 0)

		var totalRake int64
		var platformIncome int64

		for _, res := range req.Results {
			wallet, err := wallets.Ensure(res.UserID)
			if err != nil {
				return err
			}

			if res.NetPoints > 0 {
				rake := calculateRake(rakeRule, res.NetPoints)
				totalRake += rake
				netWin := res.NetPoints - rake

				wallet.BalanceAvailable += netWin
				wallet.BalanceTotal += netWin
				wallet.TotalWin += netWin
				wallet.TotalRake += rake

				winMeta := map[string]interface{}{
					"matchId": match.ID,
					"sceneId": scene.ID,
					"rawWin":  res.NetPoints,
				}
				billingLogs = append(billingLogs, model.BillingLog{
					UserID:       res.UserID,
					Type:         "win",
					Delta:        netWin,
					BalanceAfter: wallet.BalanceAvailable,
					MatchID:      &match.ID,
					MetaJSON:     mustJSON(winMeta),
					CreatedAt:    now,
				})
				if rake > 0 {
					billingLogs = append(billingLogs, model.BillingLog{
						UserID:       res.UserID,
						Type:         "rake",
						Delta:        -rake,
						BalanceAfter: wallet.BalanceAvailable,
						MatchID:      &match.ID,
						MetaJSON:     mustJSON(winMeta),
						CreatedAt:    now,
					})

					shareResult, shareLogs, profitLogs, platformShare, err := s.distributeAgentShare(tx, wallets, res.UserID, rake, agentRule, match, scene, now)
					if err != nil {
						return err
					}
					agentShareRecords = append(agentShareRecords, shareResult...)
					billingLogs = append(billingLogs, shareLogs...)
					agentLogs = append(agentLogs, profitLogs...)
					if platformShare > 0 {
						platformIncome += platformShare
						billingLogs = append(billingLogs, model.BillingLog{
							UserID:       0,
							Type:         "platform_income",
							Delta:        platformShare,
							BalanceAfter: 0,
							MatchID:      &match.ID,
							MetaJSON:     mustJSON(winMeta),
							CreatedAt:    now,
						})
					}
				}

				resultRecords = append(resultRecords, playerResultRecord{
					UserID:    res.UserID,
					NetPoints: netWin,
					Rake:      rake,
					Meta:      res.Meta,
				})
			} else {
				loss := res.NetPoints
				wallet.BalanceAvailable += loss
				wallet.BalanceTotal += loss
				wallet.TotalConsume += -loss

				lossMeta := map[string]interface{}{
					"matchId": match.ID,
					"sceneId": scene.ID,
				}
				billingLogs = append(billingLogs, model.BillingLog{
					UserID:       res.UserID,
					Type:         "lose",
					Delta:        loss,
					BalanceAfter: wallet.BalanceAvailable,
					MatchID:      &match.ID,
					MetaJSON:     mustJSON(lossMeta),
					CreatedAt:    now,
				})

				resultRecords = append(resultRecords, playerResultRecord{
					UserID:    res.UserID,
					NetPoints: loss,
					Rake:      0,
					Meta:      res.Meta,
				})
			}
		}

		if err := wallets.SaveAll(now); err != nil {
			return err
		}

		if len(billingLogs) > 0 {
			if err := tx.Create(&billingLogs).Error; err != nil {
				return err
			}
		}

		if len(agentLogs) > 0 {
			if err := tx.Create(&agentLogs).Error; err != nil {
				return err
			}
		}

		match.ResultJSON = mustJSON(resultRecords)
		match.RakeJSON = mustJSON(rakeSummary{
			Total:    totalRake,
			Platform: platformIncome,
			Agents:   agentShareRecords,
		})
		match.EndedAt = &now

		if err := tx.Save(&match).Error; err != nil {
			return err
		}

		if err := tx.Model(&model.Table{}).
			Where("id = ?", match.TableID).
			Update("status", "ended").Error; err != nil {
			return err
		}

		return nil
	})
}

func (s *Service) loadAgentRule(tx *gorm.DB) (*model.AgentRule, error) {
	var rule model.AgentRule
	// Use Find instead of First to avoid GORM RecordNotFound log when table is empty
	err := tx.Order("id DESC").Limit(1).Find(&rule).Error
	if err != nil {
		return nil, err
	}
	if rule.ID == 0 {
		return nil, nil
	}
	return &rule, nil
}

func calculateRake(rule *model.RakeRule, win int64) int64 {
	if rule == nil || win <= 0 {
		return 0
	}

	switch strings.ToLower(rule.Type) {
	case "ratio":
		var cfg struct {
			Ratio float64 `json:"ratio"`
			Cap   int64   `json:"cap"`
		}
		if err := json.Unmarshal(rule.ConfigJSON, &cfg); err != nil {
			return 0
		}
		return clampRake(int64(math.Round(float64(win)*cfg.Ratio)), win, cfg.Cap)
	case "fixed":
		var cfg struct {
			Amount int64 `json:"amount"`
		}
		if err := json.Unmarshal(rule.ConfigJSON, &cfg); err != nil {
			return 0
		}
		return clampRake(cfg.Amount, win, 0)
	case "ladder":
		type ladderStep struct {
			Min   int64   `json:"min"`
			Max   int64   `json:"max"`
			Ratio float64 `json:"ratio"`
			Value int64   `json:"value"`
		}
		var steps []ladderStep
		if err := json.Unmarshal(rule.ConfigJSON, &steps); err != nil {
			return 0
		}
		for _, step := range steps {
			inRange := (step.Min == 0 || win >= step.Min) &&
				(step.Max == 0 || win <= step.Max)
			if !inRange {
				continue
			}
			if step.Ratio > 0 {
				return clampRake(int64(math.Round(float64(win)*step.Ratio)), win, 0)
			}
			if step.Value > 0 {
				return clampRake(step.Value, win, 0)
			}
		}
	}
	return 0
}

func clampRake(value, win, cap int64) int64 {
	if value < 0 {
		value = 0
	}
	if cap > 0 && value > cap {
		value = cap
	}
	if value > win {
		return win
	}
	return value
}

func (s *Service) distributeAgentShare(
	tx *gorm.DB,
	wallets *walletBook,
	winnerID int64,
	rake int64,
	agentRule *model.AgentRule,
	match model.Match,
	scene model.Scene,
	now time.Time,
) ([]agentShareRecord, []model.BillingLog, []model.AgentProfitLog, int64, error) {
	if rake <= 0 {
		return nil, nil, nil, 0, nil
	}
	if agentRule == nil {
		return nil, nil, nil, rake, nil
	}

	user := &model.User{}
	if err := tx.First(user, winnerID).Error; err != nil {
		if err != gorm.ErrRecordNotFound {
			return nil, nil, nil, 0, err
		}
		user = &model.User{}
	}

	levelRatios := parseAgentRatios(agentRule)
	chain := resolveAgentChain(user)
	if len(chain) == 0 {
		return nil, nil, nil, rake, nil
	}

	remaining := rake
	records := make([]agentShareRecord, 0, len(chain))
	billingLogs := make([]model.BillingLog, 0, len(chain))
	profitLogs := make([]model.AgentProfitLog, 0, len(chain))
	totalAgentShare := int64(0)

	for idx, agentID := range chain {
		level := idx + 1
		ratio := levelRatios[level]
		if ratio <= 0 {
			continue
		}
		share := int64(math.Round(float64(rake) * ratio))
		if share <= 0 {
			continue
		}
		if share > remaining {
			share = remaining
		}
		if share == 0 {
			continue
		}
		remaining -= share
		totalAgentShare += share

		agentWallet, err := wallets.Ensure(agentID)
		if err != nil {
			return nil, nil, nil, 0, err
		}
		agentWallet.BalanceAvailable += share
		agentWallet.BalanceTotal += share
		agentWallet.TotalWin += share

		meta := map[string]interface{}{
			"matchId":    match.ID,
			"sceneId":    scene.ID,
			"fromUserId": winnerID,
			"level":      level,
		}
		billingLogs = append(billingLogs, model.BillingLog{
			UserID:       agentID,
			Type:         "agent_share",
			Delta:        share,
			BalanceAfter: agentWallet.BalanceAvailable,
			MatchID:      &match.ID,
			MetaJSON:     mustJSON(meta),
			CreatedAt:    now,
		})
		profitLogs = append(profitLogs, model.AgentProfitLog{
			AgentID:      agentID,
			FromUserID:   winnerID,
			MatchID:      match.ID,
			Level:        level,
			RakeAmount:   rake,
			ProfitAmount: share,
			CreatedAt:    now,
		})
		records = append(records, agentShareRecord{
			AgentID: agentID,
			Level:   level,
			Amount:  share,
		})
	}

	platformShare := rake - totalAgentShare
	if platformShare < 0 {
		platformShare = 0
	}

	if len(records) > 0 {
		if err := s.bumpAgentTotals(tx, records); err != nil {
			return nil, nil, nil, 0, err
		}
	}

	return records, billingLogs, profitLogs, platformShare, nil
}

func parseAgentRatios(rule *model.AgentRule) map[int]float64 {
	ratios := make(map[int]float64)
	if rule == nil || len(rule.LevelRatiosJSON) == 0 {
		return ratios
	}

	var raw map[string]float64
	if err := json.Unmarshal(rule.LevelRatiosJSON, &raw); err != nil {
		return ratios
	}

	for key, val := range raw {
		level, err := strconv.Atoi(strings.TrimPrefix(strings.ToUpper(key), "L"))
		if err != nil || level <= 0 {
			continue
		}
		ratios[level] = val
	}
	return ratios
}

func resolveAgentChain(user *model.User) []int64 {
	if user == nil {
		return nil
	}
	path := parseAgentPath(user.AgentPath)
	if len(path) == 0 && user.BindAgentID == nil {
		return nil
	}

	result := make([]int64, 0, len(path)+1)
	if len(path) > 0 {
		for i := len(path) - 1; i >= 0; i-- {
			id := path[i]
			if id == 0 {
				continue
			}
			result = append(result, id)
		}
	} else if user.BindAgentID != nil {
		result = append(result, *user.BindAgentID)
	}

	return deduplicate(result)
}

func parseAgentPath(path string) []int64 {
	if path == "" {
		return nil
	}
	parts := strings.Split(path, ">")
	ids := make([]int64, 0, len(parts))
	for _, part := range parts {
		id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

func deduplicate(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	result := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func (s *Service) bumpAgentTotals(tx *gorm.DB, shares []agentShareRecord) error {
	delta := make(map[int64]int64)
	for _, share := range shares {
		delta[share.AgentID] += share.Amount
	}

	for agentID, amount := range delta {
		if err := tx.Model(&model.Agent{}).
			Where("id = ?", agentID).
			UpdateColumn("total_profit", gorm.Expr("total_profit + ?", amount)).Error; err != nil {
			return err
		}
	}
	return nil
}

func mustJSON(v interface{}) datatypes.JSON {
	if v == nil {
		return datatypes.JSON("{}")
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return datatypes.JSON("{}")
	}
	return datatypes.JSON(raw)
}

type walletBook struct {
	tx      *gorm.DB
	entries map[int64]*walletEntry
}

type walletEntry struct {
	wallet *model.Wallet
	exists bool
	dirty  bool
}

func newWalletBook(tx *gorm.DB) *walletBook {
	return &walletBook{
		tx:      tx,
		entries: make(map[int64]*walletEntry),
	}
}

func (wb *walletBook) Ensure(userID int64) (*model.Wallet, error) {
	if entry, ok := wb.entries[userID]; ok {
		entry.dirty = true
		return entry.wallet, nil
	}

	wallet := &model.Wallet{}
	err := wb.tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("user_id = ?", userID).
		First(wallet).Error
	if err != nil {
		if err != gorm.ErrRecordNotFound {
			return nil, err
		}
		wallet = &model.Wallet{UserID: userID}
	}

	entry := &walletEntry{
		wallet: wallet,
		exists: err == nil,
		dirty:  true,
	}
	wb.entries[userID] = entry
	return wallet, nil
}

func (wb *walletBook) SaveAll(now time.Time) error {
	for _, entry := range wb.entries {
		if !entry.dirty {
			continue
		}
		entry.wallet.UpdatedAt = now
		var err error
		if entry.exists {
			err = wb.tx.Save(entry.wallet).Error
		} else {
			err = wb.tx.Create(entry.wallet).Error
			if err == nil {
				entry.exists = true
			}
		}
		if err != nil {
			return err
		}
		entry.dirty = false
	}
	return nil
}

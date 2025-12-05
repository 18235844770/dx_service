package game

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"dx-service/internal/model"
	appErr "dx-service/pkg/errors"
	"dx-service/pkg/logger"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

type Phase string

const (
	PhaseWaiting  Phase = "waiting"
	PhasePlaying  Phase = "playing"
	PhaseSettling Phase = "settling"
	PhaseEnded    Phase = "ended"
)

const (
	defaultTurnSeconds   = 15
	defaultCountdownUnit = time.Second
)

type SeatState struct {
	SeatIndex int    `json:"seatIndex"`
	UserID    int64  `json:"userId,string"`
	Alias     string `json:"alias"`
	Chips     int64  `json:"chips"`
	Avatar    string `json:"avatar,omitempty"`
	Status    string `json:"status"` // waiting/playing/folded/eliminated
	Ready     bool   `json:"-"`
}

type LogItem struct {
	ID        string `json:"id"`
	Timestamp int64  `json:"timestamp"`
	Content   string `json:"content"`
}

type TableState struct {
	TableID        int64       `json:"tableId,string"`
	Phase          Phase       `json:"phase"`
	Round          int         `json:"round"`
	TurnSeat       int         `json:"turnSeat"`
	LastRaise      int64       `json:"lastRaise"`
	MangoStreak    int         `json:"mangoStreak"`
	Countdown      int         `json:"countdown"`
	AllowedActions []string    `json:"allowedActions"`
	Seats          []SeatState `json:"seats"`
	MyCards        []string    `json:"myCards"`
	Logs           []LogItem   `json:"logs"`
	Result         interface{} `json:"result,omitempty"`
}

type OutgoingMessage struct {
	Type string      `json:"type"`
	Seq  int64       `json:"seq"`
	Data interface{} `json:"data"`
}

type TableRuntime struct {
	tableID     int64
	matchID     int64
	phase       Phase
	round       int
	turnSeat    int
	lastRaise   int64
	mangoStreak int

	seats      []SeatState
	seatByUser map[int64]int
	logs       []LogItem
	seq        int64

	subscribers  map[int64]chan OutgoingMessage
	timer        *time.Timer
	turnDeadline time.Time

	mu sync.Mutex

	onFinish func(*TableRuntime)
}

func newTableRuntime(table model.Table, matchID int64, onFinish func(*TableRuntime)) (*TableRuntime, error) {
	seats, seatByUser, err := parsePlayersJSON(table.PlayersJSON)
	if err != nil {
		return nil, err
	}
	return &TableRuntime{
		tableID:     table.ID,
		matchID:     matchID,
		phase:       PhaseWaiting,
		round:       1,
		turnSeat:    0,
		mangoStreak: table.MangoStreak,
		seats:       seats,
		seatByUser:  seatByUser,
		logs:        []LogItem{},
		subscribers: make(map[int64]chan OutgoingMessage),
		onFinish:    onFinish,
	}, nil
}

func parsePlayersJSON(raw json.RawMessage) ([]SeatState, map[int64]int, error) {
	seats := make([]SeatState, 0)
	seatByUser := make(map[int64]int)

	if len(raw) == 0 {
		return seats, seatByUser, nil
	}

	var payload map[string]map[string]interface{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, nil, err
	}
	for seatStr, data := range payload {
		seatIdx, err := strconv.Atoi(seatStr)
		if err != nil {
			continue
		}
		userVal, ok := data["userId"]
		if !ok {
			continue
		}
		userID, err := toInt64(userVal)
		if err != nil || userID == 0 {
			continue
		}
		alias := fmt.Sprintf("玩家%d", seatIdx)
		if v, ok := data["alias"].(string); ok && v != "" {
			alias = v
		}
		avatar := ""
		if v, ok := data["avatar"].(string); ok {
			avatar = v
		}
		chips := int64(0)
		if v, ok := data["chips"]; ok {
			chips, _ = toInt64(v)
		}
		seat := SeatState{
			SeatIndex: seatIdx,
			UserID:    userID,
			Alias:     alias,
			Avatar:    avatar,
			Chips:     chips,
			Status:    "waiting",
		}
		seats = append(seats, seat)
		seatByUser[userID] = seatIdx
	}
	return seats, seatByUser, nil
}

func toInt64(v interface{}) (int64, error) {
	switch val := v.(type) {
	case float64:
		return int64(val), nil
	case int64:
		return val, nil
	case int:
		return int64(val), nil
	case string:
		return strconv.ParseInt(val, 10, 64)
	default:
		return 0, fmt.Errorf("unsupported type")
	}
}

func (rt *TableRuntime) Subscribe(userID int64) chan OutgoingMessage {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	ch := make(chan OutgoingMessage, 8)
	rt.subscribers[userID] = ch
	rt.pushStateLocked(userID)
	return ch
}

func (rt *TableRuntime) Unsubscribe(userID int64) {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	if ch, ok := rt.subscribers[userID]; ok {
		delete(rt.subscribers, userID)
		close(ch)
	}
}

func (rt *TableRuntime) HandleAction(userID int64, action string, data json.RawMessage) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	seatIdx, ok := rt.seatByUser[userID]
	if !ok {
		return appErr.ErrTableAccessDenied
	}

	switch action {
	case "ready":
		return rt.handleReadyLocked(seatIdx, userID)
	case "pass", "call", "raise", "fold", "knock_bobo":
		return rt.handleTurnActionLocked(action, seatIdx, data)
	case "rejoin":
		rt.pushStateLocked(userID)
		return nil
	case "ping":
		rt.pushMessageLocked(userID, OutgoingMessage{Type: "pong", Seq: rt.nextSeqLocked(), Data: ginH{"message": "pong"}})
		return nil
	default:
		return fmt.Errorf("unsupported action")
	}
}

func (rt *TableRuntime) handleReadyLocked(seatIdx int, userID int64) error {
	if rt.phase != PhaseWaiting && rt.phase != PhasePlaying {
		return fmt.Errorf("invalid phase")
	}

	for i := range rt.seats {
		if rt.seats[i].SeatIndex == seatIdx {
			if rt.seats[i].Ready {
				return nil
			}
			rt.seats[i].Ready = true
			rt.appendLogLocked("ready", userID)
			break
		}
	}

	if rt.allReadyLocked() {
		rt.startRoundLocked()
	}
	rt.broadcastStateLocked()
	return nil
}

func (rt *TableRuntime) handleTurnActionLocked(action string, seatIdx int, data json.RawMessage) error {
	if rt.phase != PhasePlaying {
		return fmt.Errorf("not in playing phase")
	}
	if rt.turnSeat != seatIdx {
		return fmt.Errorf("not your turn")
	}
	if rt.isTurnExpiredLocked() {
		return fmt.Errorf("turn timeout")
	}

	seat := rt.findSeatLocked(seatIdx)
	if seat == nil || seat.Status == "folded" || seat.Status == "eliminated" {
		return fmt.Errorf("invalid seat status")
	}

	switch action {
	case "fold":
		rt.markSeatStatusLocked(seatIdx, "folded")
		rt.appendLogLocked("fold", rt.seats[rt.findSeatIdxLocked(seatIdx)].UserID)
	case "pass", "call":
		rt.appendLogLocked(action, rt.seats[rt.findSeatIdxLocked(seatIdx)].UserID)
	case "raise":
		var payload struct {
			Amount int64 `json:"amount"`
		}
		if len(data) > 0 {
			_ = json.Unmarshal(data, &payload)
		}
		if payload.Amount <= 0 {
			return fmt.Errorf("amount required")
		}
		if payload.Amount < rt.lastRaise {
			return fmt.Errorf("amount too low")
		}
		if seat.Chips > 0 && payload.Amount > seat.Chips {
			return fmt.Errorf("insufficient chips")
		}
		if payload.Amount > 0 {
			rt.lastRaise = payload.Amount
		}
		rt.appendLogLocked("raise", rt.seats[rt.findSeatIdxLocked(seatIdx)].UserID)
	case "knock_bobo":
		rt.appendLogLocked("knock_bobo", rt.seats[rt.findSeatIdxLocked(seatIdx)].UserID)
	default:
		return fmt.Errorf("unsupported action")
	}

	if rt.shouldSettleLocked() {
		rt.phase = PhaseSettling
		rt.finishLocked()
		return nil
	}

	rt.moveToNextTurnLocked()
	rt.broadcastStateLocked()
	return nil
}

func (rt *TableRuntime) pushStateLocked(userID int64) {
	state := rt.exportStateLocked(userID)
	rt.pushMessageLocked(userID, OutgoingMessage{
		Type: "state",
		Seq:  rt.nextSeqLocked(),
		Data: state,
	})
}

func (rt *TableRuntime) broadcastStateLocked() {
	stateSeq := rt.nextSeqLocked()
	for uid, ch := range rt.subscribers {
		state := rt.exportStateLocked(uid)
		msg := OutgoingMessage{
			Type: "state",
			Seq:  stateSeq,
			Data: state,
		}
		select {
		case ch <- msg:
		default:
			logger.Log.Warn("ws subscriber channel full", zap.Int64("userID", uid), zap.Int64("tableID", rt.tableID))
		}
	}
}

func (rt *TableRuntime) pushMessageLocked(userID int64, msg OutgoingMessage) {
	if ch, ok := rt.subscribers[userID]; ok {
		select {
		case ch <- msg:
		default:
			logger.Log.Warn("ws subscriber channel full", zap.Int64("userID", userID), zap.Int64("tableID", rt.tableID))
		}
	}
}

func (rt *TableRuntime) nextSeqLocked() int64 {
	rt.seq++
	return rt.seq
}

func (rt *TableRuntime) exportStateLocked(userID int64) TableState {
	allowed := rt.allowedActionsLocked(userID)
	countdown := rt.countdownSecondsLocked()
	state := TableState{
		TableID:        rt.tableID,
		Phase:          rt.phase,
		Round:          rt.round,
		TurnSeat:       rt.turnSeat,
		LastRaise:      rt.lastRaise,
		MangoStreak:    rt.mangoStreak,
		Countdown:      countdown,
		AllowedActions: allowed,
		Seats:          append([]SeatState(nil), rt.seats...),
		MyCards:        []string{}, // Cards not implemented yet
		Logs:           append([]LogItem(nil), rt.logs...),
	}
	return state
}

func (rt *TableRuntime) allowedActionsLocked(userID int64) []string {
	seatIdx, ok := rt.seatByUser[userID]
	if !ok {
		return nil
	}

	switch rt.phase {
	case PhaseWaiting:
		if rt.isSeatReadyLocked(seatIdx) {
			return nil
		}
		return []string{"ready"}
	case PhasePlaying:
		if rt.turnSeat == seatIdx {
			return []string{"pass", "call", "raise", "fold", "knock_bobo"}
		}
		return nil
	case PhaseSettling, PhaseEnded:
		return nil
	default:
		return nil
	}
}

func (rt *TableRuntime) isSeatReadyLocked(seatIdx int) bool {
	for _, s := range rt.seats {
		if s.SeatIndex == seatIdx {
			return s.Ready
		}
	}
	return false
}

func (rt *TableRuntime) allReadyLocked() bool {
	if len(rt.seats) == 0 {
		return false
	}
	for _, seat := range rt.seats {
		if !seat.Ready {
			return false
		}
	}
	return true
}

func (rt *TableRuntime) startRoundLocked() {
	rt.phase = PhasePlaying
	rt.round = 1
	rt.turnSeat = rt.findFirstActiveSeatLocked()
	rt.resetTurnTimerLocked()
	rt.appendLogLocked("round_start", 0)
}

func (rt *TableRuntime) findSeatIdxLocked(seatIdx int) int {
	for idx, seat := range rt.seats {
		if seat.SeatIndex == seatIdx {
			return idx
		}
	}
	return -1
}

func (rt *TableRuntime) markSeatStatusLocked(seatIdx int, status string) {
	for i := range rt.seats {
		if rt.seats[i].SeatIndex == seatIdx {
			rt.seats[i].Status = status
			return
		}
	}
}

func (rt *TableRuntime) findSeatLocked(seatIdx int) *SeatState {
	for i := range rt.seats {
		if rt.seats[i].SeatIndex == seatIdx {
			return &rt.seats[i]
		}
	}
	return nil
}

func (rt *TableRuntime) findFirstActiveSeatLocked() int {
	for _, seat := range rt.seats {
		if seat.Status != "folded" && seat.Status != "eliminated" {
			return seat.SeatIndex
		}
	}
	return 0
}

func (rt *TableRuntime) moveToNextTurnLocked() {
	activeSeats := rt.activeSeatsLocked()
	if len(activeSeats) == 0 {
		return
	}

	nextSeat := activeSeats[0]
	for i, s := range activeSeats {
		if s == rt.turnSeat {
			nextSeat = activeSeats[(i+1)%len(activeSeats)]
			break
		}
	}
	rt.turnSeat = nextSeat
	rt.resetTurnTimerLocked()
}

func (rt *TableRuntime) activeSeatsLocked() []int {
	result := make([]int, 0)
	for _, seat := range rt.seats {
		if seat.Status != "folded" && seat.Status != "eliminated" {
			result = append(result, seat.SeatIndex)
		}
	}
	return result
}

func (rt *TableRuntime) shouldSettleLocked() bool {
	active := rt.activeSeatsLocked()
	return len(active) <= 1
}

func (rt *TableRuntime) playersSnapshot() []int64 {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	ids := make([]int64, 0, len(rt.seats))
	for _, seat := range rt.seats {
		if seat.UserID != 0 {
			ids = append(ids, seat.UserID)
		}
	}
	return ids
}

func (rt *TableRuntime) finishLocked() {
	rt.phase = PhaseEnded
	rt.turnSeat = 0
	rt.cancelTimerLocked()
	rt.broadcastStateLocked()
	if rt.onFinish != nil {
		go rt.onFinish(rt)
	}
}

func (rt *TableRuntime) appendLogLocked(action string, userID int64) {
	rt.logs = append(rt.logs, LogItem{
		ID:        fmt.Sprintf("%d-%d", time.Now().UnixNano(), len(rt.logs)+1),
		Timestamp: time.Now().UnixMilli(),
		Content:   fmt.Sprintf("%s by %d", action, userID),
	})
}

func (rt *TableRuntime) resetTurnTimerLocked() {
	rt.cancelTimerLocked()
	rt.turnDeadline = time.Now().Add(defaultTurnSeconds * time.Second)
	rt.timer = time.AfterFunc(defaultTurnSeconds*time.Second, func() {
		rt.onTurnTimeout()
	})
}

func (rt *TableRuntime) onTurnTimeout() {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	if rt.phase != PhasePlaying {
		return
	}

	logger.Log.Warn("turn timeout auto-fold",
		zap.Int64("tableID", rt.tableID),
		zap.Int("seat", rt.turnSeat),
	)
	rt.markSeatStatusLocked(rt.turnSeat, "folded")
	rt.appendLogLocked("auto_fold", 0)

	if rt.shouldSettleLocked() {
		rt.phase = PhaseSettling
		rt.finishLocked()
		return
	}

	rt.moveToNextTurnLocked()
	rt.broadcastStateLocked()
}

func (rt *TableRuntime) cancelTimerLocked() {
	if rt.timer != nil {
		rt.timer.Stop()
		rt.timer = nil
	}
}

func (rt *TableRuntime) countdownSecondsLocked() int {
	if rt.turnDeadline.IsZero() {
		return 0
	}
	diff := time.Until(rt.turnDeadline)
	if diff <= 0 {
		return 0
	}
	return int(diff / defaultCountdownUnit)
}

func (rt *TableRuntime) isTurnExpiredLocked() bool {
	if rt.turnDeadline.IsZero() {
		return false
	}
	return time.Now().After(rt.turnDeadline)
}

// Service manages per-table runtimes.
func (s *Service) GetRuntime(ctx context.Context, tableID int64) (*TableRuntime, error) {
	if v, ok := s.runtimes.Load(tableID); ok {
		return v.(*TableRuntime), nil
	}

	var table model.Table
	if err := s.db.WithContext(ctx).First(&table, tableID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, appErr.ErrTableNotFound
		}
		return nil, err
	}

	match, _ := s.loadActiveMatch(ctx, tableID)
	matchID := int64(0)
	if match != nil {
		matchID = match.ID
	}

	rt, err := newTableRuntime(table, matchID, s.handleRuntimeFinish)
	if err != nil {
		return nil, err
	}
	s.runtimes.Store(tableID, rt)
	return rt, nil
}

// ginH is a tiny helper to avoid importing gin in this file.
type ginH map[string]interface{}

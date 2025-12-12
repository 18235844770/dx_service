package game

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	mrand "math/rand"
	"sort"
	"strconv"
	"strings"
	"time"

	"dx-service/internal/model"
	appErr "dx-service/pkg/errors"
	"dx-service/pkg/logger"

	"go.uber.org/zap"
	"gorm.io/datatypes"
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
	Bet       int64  `json:"bet"`
	Avatar    string `json:"avatar,omitempty"`
	Status    string `json:"status"` // waiting/playing/folded/eliminated
	Ready     bool   `json:"-"`
	cards     []string

	// Chexuan specific split result (exposed during settle/end)
	Split *SplitView `json:"split,omitempty"`
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
	Pot            int64       `json:"pot"`
	MangoStreak    int         `json:"mangoStreak"`
	Countdown      int         `json:"countdown"`
	AllowedActions []string    `json:"allowedActions"`
	Seats          []SeatState `json:"seats"`
	MyCards        []string    `json:"myCards"`
	Logs           []LogItem   `json:"logs"`
	Result         interface{} `json:"result,omitempty"`

	// Internal field to pass results to callback
	SettlementResults []PlayerResult
}

type SplitView struct {
	Head     []string `json:"head"`
	Tail     []string `json:"tail"`
	HeadRank string   `json:"headRank"` // e.g. "TianGang"
	TailRank string   `json:"tailRank"` // e.g. "9Points"
	IsDaoba  bool     `json:"isDaoba"`
}

type OutgoingMessage struct {
	Type string      `json:"type"`
	Seq  int64       `json:"seq"`
	Data interface{} `json:"data"`
}

type loopCommand struct {
	kind   string
	userID int64
	action string
	data   json.RawMessage
	resp   chan error
	subCh  chan OutgoingMessage
}

type TableRuntime struct {
	tableID     int64
	matchID     int64
	sceneID     int64
	basePi      int64
	minUnitPi   int64
	boboEnabled bool
	chexuanMode bool
	db          *gorm.DB
	phase       Phase
	round       int
	turnSeat    int
	lastRaise   int64
	pot         int64
	mangoStreak int
	bankerSeat  int

	round1Bet   bool
	round2Bet   bool
	round2Knock bool
	lastAggSeat int
	tailBigWin  bool

	seats      []SeatState
	seatByUser map[int64]int
	roundActed map[int]bool

	firstRaiseDone bool
	raisedRound1   bool
	raisedRound2   bool
	logs           []LogItem
	seq            int64
	deck           []string

	subscribers  map[int64]chan OutgoingMessage
	timer        *time.Timer
	timerC       <-chan time.Time
	turnDeadline time.Time
	cmdCh        chan loopCommand
	quitCh       chan struct{}

	onFinish func(*TableRuntime)

	// Result cache for service callback
	SettlementResults []PlayerResult
}

func newTableRuntime(db *gorm.DB, table model.Table, scene model.Scene, matchID int64, onFinish func(*TableRuntime)) (*TableRuntime, error) {
	seats, seatByUser, err := parsePlayersJSON(json.RawMessage(table.PlayersJSON))
	if err != nil {
		return nil, err
	}
	sceneName := strings.ToLower(scene.Name)
	chexuanMode := scene.BoboEnabled || scene.MangoEnabled || strings.Contains(sceneName, "扯旋") || strings.Contains(sceneName, "chexuan")
	bankerSeat := 0
	if len(seats) > 0 {
		bankerSeat = seats[0].SeatIndex
	}
	rt := &TableRuntime{
		tableID:     table.ID,
		matchID:     matchID,
		sceneID:     scene.ID,
		db:          db,
		basePi:      scene.BasePi,
		minUnitPi:   scene.MinUnitPi,
		boboEnabled: scene.BoboEnabled,
		chexuanMode: chexuanMode,
		phase:       PhaseWaiting,
		round:       0,
		turnSeat:    0,
		mangoStreak: table.MangoStreak,
		seats:       seats,
		seatByUser:  seatByUser,
		bankerSeat:  bankerSeat,
		roundActed:  make(map[int]bool),
		logs:        []LogItem{},
		subscribers: make(map[int64]chan OutgoingMessage),
		cmdCh:       make(chan loopCommand, 16),
		quitCh:      make(chan struct{}),
		onFinish:    onFinish,
	}
	rt.startLoop()
	return rt, nil
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
		// If chips are 0 in PlayersJSON, it might be missed during creation.
		// However, MatchService now populates it from BuyIn.

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
	sort.Slice(seats, func(i, j int) bool {
		return seats[i].SeatIndex < seats[j].SeatIndex
	})
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

func (rt *TableRuntime) startLoop() {
	go func() {
		for {
			select {
			case cmd := <-rt.cmdCh:
				rt.handleCommand(cmd)
			case <-rt.timerC:
				rt.handleTurnTimeoutLocked()
			case <-rt.quitCh:
				return
			}
		}
	}()
}

func (rt *TableRuntime) handleCommand(cmd loopCommand) {
	switch cmd.kind {
	case "subscribe":
		rt.subscribers[cmd.userID] = cmd.subCh
		rt.pushStateLocked(cmd.userID)
		if cmd.resp != nil {
			cmd.resp <- nil
		}
	case "unsubscribe":
		if ch, ok := rt.subscribers[cmd.userID]; ok {
			delete(rt.subscribers, cmd.userID)
			close(ch)
		}
		if cmd.resp != nil {
			cmd.resp <- nil
		}
	case "action":
		err := rt.handleActionLocked(cmd.userID, cmd.action, cmd.data)
		if cmd.resp != nil {
			cmd.resp <- err
		}
	}
}

func (rt *TableRuntime) handleActionLocked(userID int64, action string, data json.RawMessage) error {
	seatIdx, ok := rt.seatByUser[userID]
	if !ok && action != "rejoin" {
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

func (rt *TableRuntime) Subscribe(userID int64) chan OutgoingMessage {
	ch := make(chan OutgoingMessage, 8)
	resp := make(chan error, 1)
	rt.cmdCh <- loopCommand{kind: "subscribe", userID: userID, subCh: ch, resp: resp}
	if err := <-resp; err != nil {
		close(ch)
		return nil
	}
	return ch
}

func (rt *TableRuntime) Unsubscribe(userID int64) {
	resp := make(chan error, 1)
	rt.cmdCh <- loopCommand{kind: "unsubscribe", userID: userID, resp: resp}
	<-resp
}

func (rt *TableRuntime) HandleAction(userID int64, action string, data json.RawMessage) error {
	resp := make(chan error, 1)
	rt.cmdCh <- loopCommand{kind: "action", userID: userID, action: action, data: data, resp: resp}
	return <-resp
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
		rt.markActedLocked(seatIdx)
		rt.appendLogLocked("fold", seat.UserID)
		rt.persistRoundLogLocked(actionEntry{Action: "fold", Seat: seatIdx})
	case "pass":
		if !rt.canPassLocked(seatIdx) {
			return fmt.Errorf("cannot pass, must call or fold")
		}
		rt.markActedLocked(seatIdx)
		rt.appendLogLocked("pass", seat.UserID)
		rt.persistRoundLogLocked(actionEntry{Action: "pass", Seat: seatIdx})
	case "call":
		if err := rt.handleCallLocked(seatIdx); err != nil {
			return err
		}
		rt.persistRoundLogLocked(actionEntry{Action: "call", Seat: seatIdx})
	case "raise":
		if err := rt.handleRaiseLocked(seatIdx, data); err != nil {
			return err
		}
		var payload struct {
			Amount int64 `json:"amount"`
		}
		_ = json.Unmarshal(data, &payload)
		rt.persistRoundLogLocked(actionEntry{Action: "raise", Seat: seatIdx, Amount: payload.Amount})
	case "knock_bobo":
		return rt.handleKnockBoboLocked(seatIdx, "manual_knock")
	default:
		return fmt.Errorf("unsupported action")
	}

	if rt.shouldSettleLocked() {
		if rt.round == 2 && rt.round2Bet {
			rt.tailBigWin = true
		}
		rt.phase = PhaseSettling
		rt.determineWinnersAndSettleLocked()
		return nil
	}

	if rt.shouldAdvanceRoundLocked() {
		rt.advanceRoundLocked()
		if rt.phase != PhaseSettling {
			rt.broadcastStateLocked()
		}
		return nil
	}

	rt.moveToNextTurnLocked()
	if rt.phase == PhasePlaying {
		rt.broadcastStateLocked()
	}
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

	myCards := []string{}
	// Show my cards always if I have them
	if seatIdx, ok := rt.seatByUser[userID]; ok {
		for _, s := range rt.seats {
			if s.SeatIndex == seatIdx {
				if rt.chexuanMode {
					myCards = make([]string, len(s.cards))
					for k, c := range s.cards {
						myCards[k] = ToPokerCode(c)
					}
				} else {
					myCards = s.cards
				}
				break
			}
		}
	}

	// In Settling/Ended phase, expose ALL cards?
	// For now, let's keep private unless we implement Showdown event explicitly.
	// Better: If PhaseEnded, we can populate all seats' cards in the Seats array?
	// The current SeatState struct has 'cards' as private. We need to expose them if needed.
	// Let's stick to 'MyCards' for now.

	// Create a copy of seats to potentially expose cards
	displaySeats := make([]SeatState, len(rt.seats))
	for i, s := range rt.seats {
		ds := s
		// If ended, we could theoretically expose cards here if we added a PublicCards field.
		// For now, we rely on 'MyCards' only.

		// In Chexuan mode, during Settle/Ended, we expose split details if available
		// Need to store split details in seat state or compute them here?
		// Better to store them in rt.seats during settle.
		// Since rt.seats[i].Split is a pointer, we just copy it.
		// We'll populate it in settleChexuanLocked.

		if s.Split != nil && rt.chexuanMode {
			newSplit := *s.Split
			newSplit.Head = make([]string, len(s.Split.Head))
			for k, c := range s.Split.Head {
				newSplit.Head[k] = ToPokerCode(c)
			}
			newSplit.Tail = make([]string, len(s.Split.Tail))
			for k, c := range s.Split.Tail {
				newSplit.Tail[k] = ToPokerCode(c)
			}
			ds.Split = &newSplit
		}

		displaySeats[i] = ds
	}

	state := TableState{
		TableID:        rt.tableID,
		Phase:          rt.phase,
		Round:          rt.round,
		TurnSeat:       rt.turnSeat,
		LastRaise:      rt.lastRaise,
		Pot:            rt.pot,
		MangoStreak:    rt.mangoStreak,
		Countdown:      countdown,
		AllowedActions: allowed,
		Seats:          displaySeats,
		MyCards:        myCards,
		Logs:           append([]LogItem(nil), rt.logs...),
	}
	if rt.phase == PhaseEnded && len(rt.SettlementResults) > 0 {
		state.Result = rt.SettlementResults
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
		if rt.turnSeat != seatIdx {
			return nil
		}
		seat := rt.findSeatLocked(seatIdx)
		if seat == nil || seat.Status == "folded" || seat.Status == "eliminated" {
			return nil
		}
		if rt.round >= 3 {
			return []string{"fold"}
		}

		actions := []string{"fold"}
		if rt.round2Knock {
			return []string{"fold", "call"}
		}
		if rt.canPassLocked(seatIdx) {
			actions = append(actions, "pass")
		} else {
			actions = append(actions, "call")
		}

		firstActor := rt.round == 1 && len(rt.roundActed) == 0 && seatIdx == rt.firstActorSeatLocked()
		if rt.round == 1 && seat.Chips > 0 && !firstActor {
			actions = append(actions, "raise")
		}
		if rt.round == 2 {
			if rt.boboEnabled {
				actions = append(actions, "knock_bobo")
			} else if seat.Chips > 0 {
				actions = append(actions, "raise")
			}
		} else if rt.round == 1 && rt.boboEnabled {
			actions = append(actions, "knock_bobo")
		}
		return actions
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
	rt.round = 0
	rt.pot = 0
	rt.lastRaise = 0
	rt.roundActed = make(map[int]bool)
	rt.firstRaiseDone = false
	rt.raisedRound1 = false
	rt.raisedRound2 = false
	rt.round1Bet = false
	rt.round2Bet = false
	rt.round2Knock = false
	rt.lastAggSeat = 0
	rt.tailBigWin = false
	for i := range rt.seats {
		rt.seats[i].Bet = 0
		if rt.seats[i].Status != "eliminated" {
			rt.seats[i].Status = "playing"
		}
	}
	rt.initDeckLocked()
	rt.applyAntesLocked()
	if rt.bankerSeat == 0 {
		rt.bankerSeat = rt.findFirstActiveSeatLocked()
	}
	rt.appendLogLocked("round0_start", 0)
	rt.advanceRoundLocked()
}

func (rt *TableRuntime) applyAntesLocked() {
	if rt.basePi <= 0 {
		return
	}
	for i := range rt.seats {
		if rt.seats[i].Status == "folded" || rt.seats[i].Status == "eliminated" {
			continue
		}
		ante := rt.basePi
		if ante > rt.seats[i].Chips {
			ante = rt.seats[i].Chips
		}
		if ante <= 0 {
			continue
		}
		rt.seats[i].Chips -= ante
		rt.seats[i].Bet += ante
		rt.pot += ante
	}
	if rt.lastRaise < rt.basePi {
		rt.lastRaise = rt.basePi
	}
}

func (rt *TableRuntime) advanceRoundLocked() {
	if rt.phase == PhasePlaying && rt.shouldDealThisStageLocked() {
		rt.dealCardsLocked()
	}
	rt.round++
	rt.roundActed = make(map[int]bool)
	if rt.phase != PhasePlaying {
		return
	}

	// Chexuan "Mango" / "LiuJu" detection logic
	if rt.chexuanMode {
		// Round 1 -> Round 2 transition
		// We already track rt.round1Bet in handleCall/Raise.

		// Round 2 -> Round 3 transition
		if rt.round == 3 {
			// Rule: If R1 had action (Bet/Raise), but R2 was all checks (no Raise) -> Mango (LiuJu).
			// Rule: If R2 had action (Raise) but no one called (except folder?) -> Tail big eats skin (handled in shouldSettle?).
			// Actually, if R2 had Raise and everyone else Folded, shouldSettleLocked() returns true before we get here.
			// So if we are here, it means either:
			// A) R2 was all PASS/CHECK (no bets added in R2).
			// B) R2 had bets but multiple players are still active (Callers).

			if rt.round1Bet && !rt.round2Bet {
				// Condition A: R1 active, R2 quiet -> Mango.
				// No showdown.
				rt.phase = PhaseSettling
				rt.settleChexuanMangoLocked()
				return
			}
		}
	}

	if rt.round >= 3 {
		rt.phase = PhaseSettling
		rt.turnSeat = 0
		rt.determineWinnersAndSettleLocked()
		return
	}
	rt.turnSeat = rt.firstActorSeatLocked()
	if rt.turnSeat == 0 {
		rt.phase = PhaseSettling
		rt.determineWinnersAndSettleLocked()
		return
	}
	if rt.round == 1 && rt.lastRaise == 0 && rt.basePi > 0 {
		rt.lastRaise = rt.basePi
	}
	rt.persistRoundLogLocked(actionEntry{Action: fmt.Sprintf("round%d_start", rt.round), Seat: rt.turnSeat}, true)
	rt.resetTurnTimerLocked()
}

func (rt *TableRuntime) shouldDealThisStageLocked() bool {
	if rt.round == 0 {
		return true
	}
	if rt.chexuanMode && (rt.round == 1 || rt.round == 2) {
		return true
	}
	return false
}

func (rt *TableRuntime) firstActorSeatLocked() int {
	start := rt.bankerSeat
	if start == 0 {
		start = rt.findFirstActiveSeatLocked()
	}
	return rt.nextActiveAfterLocked(start)
}

func (rt *TableRuntime) nextActiveAfterLocked(seatIdx int) int {
	active := rt.activeSeatsLocked()
	if len(active) == 0 {
		return 0
	}
	if seatIdx == 0 {
		return active[0]
	}
	for i, s := range active {
		if s == seatIdx {
			return active[(i+1)%len(active)]
		}
	}
	return active[0]
}

func (rt *TableRuntime) initDeckLocked() {
	if rt.chexuanMode {
		rt.deck = NewChexuanDeck()
		return
	}
	suits := []string{"s", "h", "d", "c"}
	ranks := []string{"2", "3", "4", "5", "6", "7", "8", "9", "T", "J", "Q", "K", "A"}
	rt.deck = make([]string, 0, 52)
	for _, s := range suits {
		for _, r := range ranks {
			rt.deck = append(rt.deck, r+s)
		}
	}
	mrand.Shuffle(len(rt.deck), func(i, j int) {
		rt.deck[i], rt.deck[j] = rt.deck[j], rt.deck[i]
	})
}

func (rt *TableRuntime) dealCardsLocked() {
	count := 0
	if rt.round == 0 {
		count = 2
		for i := range rt.seats {
			rt.seats[i].cards = nil
		}
	} else if rt.chexuanMode && (rt.round == 1 || rt.round == 2) {
		count = 1
	}
	if count == 0 {
		return
	}
	cardsPerPlayer := count
	activeSeats := rt.activeSeatsLocked()

	// Simple dealing: 1 card at a time to each player
	for i := 0; i < cardsPerPlayer; i++ {
		for _, seatIdx := range activeSeats {
			if len(rt.deck) == 0 {
				break
			}
			card := rt.deck[0]
			rt.deck = rt.deck[1:]

			// Find seat and append
			for k := range rt.seats {
				if rt.seats[k].SeatIndex == seatIdx {
					rt.seats[k].cards = append(rt.seats[k].cards, card)
					break
				}
			}
		}
	}
}

func (rt *TableRuntime) markActedLocked(seatIdx int) {
	if rt.roundActed == nil {
		rt.roundActed = make(map[int]bool)
	}
	rt.roundActed[seatIdx] = true
}

func (rt *TableRuntime) resetRoundActedLocked(seatIdx int) {
	rt.roundActed = make(map[int]bool)
	if seatIdx != 0 {
		rt.roundActed[seatIdx] = true
	}
}

func (rt *TableRuntime) canPassLocked(seatIdx int) bool {
	seat := rt.findSeatLocked(seatIdx)
	if seat == nil {
		return false
	}
	if rt.round >= 3 {
		return false
	}
	if seat.Bet >= rt.lastRaise || seat.Chips == 0 {
		return true
	}
	return false
}

func (rt *TableRuntime) requiredCallAmountLocked(seatIdx int) int64 {
	amount := rt.lastRaise
	if rt.round == 1 && len(rt.roundActed) == 0 && seatIdx == rt.firstActorSeatLocked() {
		twoBase := rt.basePi * 2
		if twoBase > amount {
			amount = twoBase
		}
	}
	return amount
}

func (rt *TableRuntime) minRaiseAmountLocked() int64 {
	minAmount := rt.lastRaise
	threshold := rt.minUnitPi * 5
	if threshold == 0 {
		threshold = rt.basePi * 5
	}
	if rt.round == 1 && !rt.firstRaiseDone && threshold > minAmount {
		minAmount = threshold
	}
	if rt.minUnitPi > 0 && minAmount < rt.minUnitPi {
		minAmount = rt.minUnitPi
	}
	return minAmount
}

func (rt *TableRuntime) handleCallLocked(seatIdx int) error {
	seat := rt.findSeatLocked(seatIdx)
	if seat == nil {
		return fmt.Errorf("seat not found")
	}
	target := rt.requiredCallAmountLocked(seatIdx)
	if target < rt.lastRaise {
		target = rt.lastRaise
	}
	diff := target - seat.Bet
	if diff < 0 {
		diff = 0
	}
	if diff > seat.Chips {
		diff = seat.Chips
	}
	if diff > 0 {
		if rt.round == 1 {
			rt.round1Bet = true
		} else if rt.round == 2 {
			rt.round2Bet = true
		}
	}
	seat.Chips -= diff
	seat.Bet += diff
	rt.pot += diff
	if seat.Bet > rt.lastRaise {
		rt.lastRaise = seat.Bet
	}
	rt.markActedLocked(seatIdx)
	rt.appendLogLocked("call", seat.UserID)
	return nil
}

func (rt *TableRuntime) handleRaiseLocked(seatIdx int, data json.RawMessage) error {
	if rt.round == 2 && rt.boboEnabled && !rt.round2Knock {
		return rt.handleKnockBoboLocked(seatIdx, "raise_in_round2")
	}
	var payload struct {
		Amount int64 `json:"amount"`
	}
	if len(data) > 0 {
		_ = json.Unmarshal(data, &payload)
	}
	if payload.Amount <= 0 {
		if rt.boboEnabled {
			return rt.handleKnockBoboLocked(seatIdx, "invalid_raise")
		}
		return fmt.Errorf("amount required")
	}
	if rt.round == 1 {
		threshold := rt.minUnitPi * 5
		if threshold == 0 {
			threshold = rt.basePi * 5
		}
		if payload.Amount < threshold {
			return fmt.Errorf("round1 raise below minimum")
		}
		if len(rt.roundActed) == 0 && seatIdx == rt.firstActorSeatLocked() {
			expect := rt.basePi * 2
			if expect > 0 && payload.Amount != expect {
				return fmt.Errorf("first bet in round1 must be 2*basePi")
			}
		}
	}
	minAmount := rt.minRaiseAmountLocked()
	if payload.Amount < minAmount {
		if rt.boboEnabled {
			return rt.handleKnockBoboLocked(seatIdx, "invalid_raise")
		}
		return fmt.Errorf("amount too low")
	}
	seat := rt.findSeatLocked(seatIdx)
	if seat == nil {
		return fmt.Errorf("seat not found")
	}
	diff := payload.Amount - seat.Bet
	if diff <= 0 {
		if rt.boboEnabled {
			return rt.handleKnockBoboLocked(seatIdx, "invalid_raise")
		}
		return fmt.Errorf("raise must increase bet")
	}
	if seat.Chips < diff {
		return fmt.Errorf("insufficient chips")
	}
	seat.Chips -= diff
	seat.Bet = payload.Amount
	rt.pot += diff
	rt.lastRaise = payload.Amount
	rt.lastAggSeat = seatIdx
	rt.firstRaiseDone = true
	if rt.round == 1 {
		rt.raisedRound1 = true
		rt.round1Bet = true
	}
	if rt.round == 2 {
		rt.raisedRound2 = true
		rt.round2Bet = true
	}
	rt.resetRoundActedLocked(seatIdx)
	rt.appendLogLocked("raise", seat.UserID)
	return nil
}

func (rt *TableRuntime) handleKnockBoboLocked(seatIdx int, reason string) error {
	if !rt.boboEnabled {
		return fmt.Errorf("knock_bobo disabled")
	}
	seat := rt.findSeatLocked(seatIdx)
	if seat == nil {
		return fmt.Errorf("seat not found")
	}
	action := "knock_bobo"
	if reason != "" {
		action = fmt.Sprintf("knock_bobo:%s", reason)
	}
	raiseTo := seat.Bet + seat.Chips
	diff := raiseTo - seat.Bet
	if diff > 0 {
		seat.Chips -= diff
		seat.Bet += diff
		rt.pot += diff
	}
	rt.lastRaise = seat.Bet
	rt.lastAggSeat = seatIdx
	rt.round2Knock = true
	rt.round2Bet = true
	rt.raisedRound2 = true
	rt.resetRoundActedLocked(seatIdx)
	rt.appendLogLocked(action, seat.UserID)
	rt.persistRoundLogLocked(actionEntry{Action: "knock_bobo", Seat: seatIdx, Meta: map[string]interface{}{"reason": reason}})
	return nil
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
	next := rt.nextActiveAfterLocked(rt.turnSeat)
	if next == 0 {
		return
	}
	rt.turnSeat = next
	rt.resetTurnTimerLocked()
}

func (rt *TableRuntime) activeSeatsLocked() []int {
	result := make([]int, 0)
	for _, seat := range rt.seats {
		if seat.Status != "folded" && seat.Status != "eliminated" {
			result = append(result, seat.SeatIndex)
		}
	}
	sort.Ints(result)
	return result
}

func (rt *TableRuntime) shouldAdvanceRoundLocked() bool {
	if rt.phase != PhasePlaying {
		return false
	}
	if rt.round >= 3 {
		return true
	}
	active := rt.activeSeatsLocked()
	if len(active) <= 1 {
		return true
	}
	for _, seatIdx := range active {
		seat := rt.findSeatLocked(seatIdx)
		if seat == nil || seat.Status == "folded" || seat.Status == "eliminated" {
			continue
		}
		if seat.Bet < rt.lastRaise && seat.Chips > 0 {
			return false
		}
		if !rt.roundActed[seatIdx] {
			return false
		}
	}
	return true
}

func (rt *TableRuntime) shouldSettleLocked() bool {
	return len(rt.activeSeatsLocked()) == 1
}

func (rt *TableRuntime) determineWinnersAndSettleLocked() {
	if rt.chexuanMode {
		rt.settleChexuanLocked()
		return
	}
	activeSeats := rt.activeSeatsLocked()
	if len(activeSeats) == 0 {
		rt.finishLocked()
		return
	}

	showdown := len(activeSeats) > 1

	// 1. If only 1 active player (others folded), they win the pot
	if len(activeSeats) == 1 {
		winnerIdx := activeSeats[0]
		winnerSeat := rt.findSeatLocked(winnerIdx)

		results := make([]PlayerResult, 0)

		// Winner gets Pot - their own Contribution?
		// Actually Pot includes everyone's bets.
		// NetPoints for winner = Pot - their_bets_this_round (already in Pot) + returned_bets...
		// Simplified: NetPoints = Pot - TotalBet
		// But SettleMatch expects NetPoints sum to 0.
		// So Winner gets +X, Losers get -Y.

		// We need to track how much each player put in to calculate net win/loss correctly?
		// SeatState has `Bet` which is CURRENT round bet.
		// Real poker needs cumulative pot tracking per player for side pots.
		// Simplified Model:
		// Losers lose what they bet. Winner wins the rest.

		// Calculate losers first
		winAmount := int64(0)
		for _, seat := range rt.seats {
			if seat.SeatIndex == winnerIdx {
				continue
			}
			// Assuming `Bet` is what they put in THIS round/hand total?
			// rt.pot should be sum of all seat.Bet if we reset Bet each round?
			// Wait, rt.pot accumulates. seat.Bet is usually per-street.
			// If we simplify: seat.Bet is total contribution this hand.
			// We need to persist total contribution if we clear seat.Bet between rounds.
			// Current implementation: startRound clears Bet. call/raise adds to Bet and Pot.
			// So seat.Bet is valid for this round.
			// If multiple rounds, we need cumulative.
			// Let's assume single round for "Mango" / "Bobo".

			loss := seat.Bet
			if loss > 0 {
				results = append(results, PlayerResult{
					UserID:    seat.UserID,
					NetPoints: -loss,
				})
				winAmount += loss
			}
		}

		results = append(results, PlayerResult{
			UserID:    winnerSeat.UserID,
			NetPoints: winAmount,
			Meta:      map[string]interface{}{"winType": "fold_win"},
		})

		rt.applyMangoSettlementLocked(&results, showdown)
		rt.finishWithResultsLocked(results)
		return
	}

	// 2. Showdown: Compare cards
	// Evaluate hands
	type contender struct {
		SeatIdx int
		UserID  int64
		Score   int64
		Bet     int64
	}
	candidates := make([]contender, 0)

	for _, idx := range activeSeats {
		seat := rt.findSeatLocked(idx)
		score := EvaluateHand(seat.cards)
		candidates = append(candidates, contender{
			SeatIdx: idx,
			UserID:  seat.UserID,
			Score:   score,
			Bet:     seat.Bet,
		})
	}

	// Sort by Score Descending
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})

	winner := candidates[0]
	// Handle split pot? MVP: Single winner

	results := make([]PlayerResult, 0)
	winAmount := int64(0)

	for _, c := range candidates {
		if c.SeatIdx == winner.SeatIdx {
			continue
		}
		loss := c.Bet
		results = append(results, PlayerResult{
			UserID:    c.UserID,
			NetPoints: -loss,
			Meta:      map[string]interface{}{"score": c.Score},
		})
		winAmount += loss
	}

	// Add folded players losses
	for _, seat := range rt.seats {
		if seat.Status == "folded" && seat.Bet > 0 {
			results = append(results, PlayerResult{
				UserID:    seat.UserID,
				NetPoints: -seat.Bet,
			})
			winAmount += seat.Bet
		}
	}

	results = append(results, PlayerResult{
		UserID:    winner.UserID,
		NetPoints: winAmount,
		Meta:      map[string]interface{}{"score": winner.Score, "winType": "showdown"},
	})

	rt.applyMangoSettlementLocked(&results, showdown)
	rt.finishWithResultsLocked(results)
}

// settleChexuanMangoLocked handles "LiuJu" (Mango) where R1 had action but R2 was quiet.
func (rt *TableRuntime) settleChexuanMangoLocked() {
	results := make([]PlayerResult, 0)
	for _, seat := range rt.seats {
		if seat.UserID == 0 {
			continue
		}
		if seat.Bet > 0 {
			results = append(results, PlayerResult{
				UserID:    seat.UserID,
				NetPoints: 0, // Refund: No profit/loss recorded in DB (or strictly 0).
				// But we rely on applyChipUpdatesLocked to return the bet to seat.Chips.
				// applyChipUpdatesLocked: returned = seat.Bet (100) + NetPoints (0) = 100.
				// seat.Chips += 100. (Restore balance). Correct.
				Meta: map[string]interface{}{"reason": "mango_refund"},
			})
		} else {
			results = append(results, PlayerResult{
				UserID:    seat.UserID,
				NetPoints: 0,
			})
		}
	}

	// Increment Mango Streak
	if rt.mangoStreak < 3 {
		rt.mangoStreak++
	} else {
		rt.mangoStreak = 3
	}

	// Attach Mango info to first result for logging
	if len(results) > 0 {
		if results[0].Meta == nil {
			results[0].Meta = make(map[string]interface{})
		}
		results[0].Meta["mangoStreak"] = rt.mangoStreak
		results[0].Meta["mangoEvent"] = "liuju"
	}

	rt.applyChipUpdatesLocked(results)
	rt.finishWithResultsLocked(results)
}

type chexuanPlayer struct {
	SeatIdx   int
	UserID    int64
	Bet       int64
	Head      []string
	Tail      []string
	HeadScore int64
	TailScore int64
	HeadMax   int
	Folded    bool
	Invalid   bool // Daoba
	IsSanHua  bool // SanHuaTen or SanHuaSix
}

func (rt *TableRuntime) settleChexuanLocked() {
	participants := make([]chexuanPlayer, 0, len(rt.seats))
	for i, seat := range rt.seats {
		if seat.Status == "eliminated" || seat.UserID == 0 {
			continue
		}
		p := chexuanPlayer{
			SeatIdx: seat.SeatIndex,
			UserID:  seat.UserID,
			Bet:     seat.Bet,
		}
		if seat.Status == "folded" {
			p.Folded = true
			p.HeadScore = -1
			p.TailScore = -1
		} else {
			// Check SanHua (Auto-Tie)
			if checkSanHua(seat.cards) {
				p.IsSanHua = true
				p.Invalid = false
			} else {
				head, tail, _, isValid := BestSplit(seat.cards)
				p.Head = head
				p.Tail = tail
				p.HeadScore = evaluatePairScore(head)
				p.TailScore = evaluatePairScore(tail)
				p.HeadMax = chexuanHeadMaxRank(head)
				p.Invalid = !isValid

				// Update seat with split view for frontend
				rt.seats[i].Split = &SplitView{
					Head:    head,
					Tail:    tail,
					IsDaoba: !isValid,
				}
			}
		}
		participants = append(participants, p)
	}

	if len(participants) == 0 {
		rt.finishLocked()
		return
	}

	// Sort logic: Valid > Invalid. Then HeadScore desc.
	// Note: SanHua players are valid but scores are irrelevant as they always tie.
	// We can keep them in the list.
	sort.Slice(participants, func(i, j int) bool {
		// Folded always last
		if participants[i].Folded != participants[j].Folded {
			return !participants[i].Folded
		}
		if participants[i].Folded {
			return false
		}

		// SanHua treatment in sort? Doesn't matter much as they tie.
		// Let's sort normally for others.

		if participants[i].Invalid != participants[j].Invalid {
			return !participants[i].Invalid
		}
		if participants[i].HeadScore != participants[j].HeadScore {
			return participants[i].HeadScore > participants[j].HeadScore
		}
		if participants[i].HeadMax != participants[j].HeadMax {
			return participants[i].HeadMax > participants[j].HeadMax
		}
		return participants[i].TailScore > participants[j].TailScore
	})

	ledger := make(map[int64]int64, len(participants))
	for _, p := range participants {
		ledger[p.UserID] = 0
	}

	// Tail big eats skin: winner is last aggressor directly taking others' bets.
	// Only applies if everyone else folded/passed-timidly?
	// Logic: If tailBigWin is true, we skip comparison.
	if rt.tailBigWin && rt.lastAggSeat != 0 {
		winner := rt.findSeatLocked(rt.lastAggSeat)
		if winner != nil {
			winTotal := int64(0)
			for _, seat := range rt.seats {
				if seat.UserID == winner.UserID || seat.UserID == 0 {
					continue
				}
				ledger[seat.UserID] = -seat.Bet
				winTotal += seat.Bet
			}
			ledger[winner.UserID] = winTotal
		}
		res := buildResultsFromLedger(ledger)
		rt.applyMangoSettlementLocked(res, len(participants) > 1)
		rt.finishWithResultsLocked(*res)
		return
	}

	// Pairwise settle
	for i := 0; i < len(participants); i++ {
		for j := i + 1; j < len(participants); j++ {
			a := participants[i]
			b := participants[j]

			outcome := compareChexuanSplit(a, b)
			if outcome == 0 {
				continue
			}
			amount := minInt64(a.Bet, b.Bet)
			if amount <= 0 {
				continue
			}
			if outcome > 0 {
				ledger[a.UserID] += amount
				ledger[b.UserID] -= amount
			} else {
				ledger[b.UserID] += amount
				ledger[a.UserID] -= amount
			}
		}
	}

	// Head-big protection
	// Only the top player (by sort order) gets protection?
	// Document says "Head Big (Largest Head Card) player".
	// Our sort puts largest HeadScore first. So participants[0] is Head Big.
	top := participants[0]
	// Check if top really is Head Big (could be tied with others).
	// Protection applies if they lost more than cap.
	if !top.Folded && !top.Invalid {
		net := ledger[top.UserID]
		lossCap := -(int64(rt.mangoStreak)*2*rt.basePi + rt.basePi)
		if net < lossCap {
			diff := lossCap - net
			ledger[top.UserID] = lossCap
			rt.shiftLedgerDiff(ledger, top.UserID, diff)
		}
	}

	results := buildResultsFromLedger(ledger)
	showdown := len(participants) > 1
	rt.applyMangoSettlementLocked(results, showdown)
	rt.applyChipUpdatesLocked(*results)
	rt.finishWithResultsLocked(*results)
}

func (rt *TableRuntime) applyChipUpdatesLocked(results []PlayerResult) {
	for _, res := range results {
		if res.UserID == 0 {
			continue
		}
		seatIdx, ok := rt.seatByUser[res.UserID]
		if !ok {
			continue
		}
		seat := &rt.seats[seatIdx-1]
		// For winners, we add back their bet + net profit.
		// For losers (net < 0), we add back (bet - loss).
		// Since net = win - bet (usually), or net is pure profit/loss.
		// Let's assume NetPoints is change in wealth relative to start of hand.
		// If I bet 100 and win pot of 300 (my 100 + opp 100 + opp 100). Net is +200.
		// Returned = 100 (my bet) + 200 (net) = 300. Correct.
		// If I bet 100 and lose. Net is -100.
		// Returned = 100 + (-100) = 0. Correct.
		returned := seat.Bet + res.NetPoints
		if returned > 0 {
			seat.Chips += returned
		}
	}
}

func buildResultsFromLedger(ledger map[int64]int64) *[]PlayerResult {
	results := make([]PlayerResult, 0, len(ledger))
	for uid, net := range ledger {
		results = append(results, PlayerResult{
			UserID:    uid,
			NetPoints: net,
		})
	}
	return &results
}

func (rt *TableRuntime) shiftLedgerDiff(ledger map[int64]int64, excludeUID int64, diff int64) {
	if diff == 0 {
		return
	}
	for uid := range ledger {
		if uid == excludeUID {
			continue
		}
		if ledger[uid] <= 0 {
			continue
		}
		available := ledger[uid]
		if available >= diff {
			ledger[uid] -= diff
			return
		}
		ledger[uid] = 0
		diff -= available
		if diff <= 0 {
			return
		}
	}
}

func checkSanHua(cards []string) bool {
	if len(cards) < 3 {
		return false
	}
	counts := make(map[string]bool)
	for _, c := range cards {
		counts[c] = true
	}
	// SanHuaTen: B10 + R10 + BJ + *
	if counts["B10"] && counts["R10"] && counts["BJ"] {
		return true
	}
	// SanHuaSix: B6 + R6 + BK + *
	if counts["B6"] && counts["R6"] && counts["BK"] {
		return true
	}
	return false
}

func compareChexuanSplit(a, b chexuanPlayer) int {
	if a.IsSanHua || b.IsSanHua {
		return 0
	}
	if a.Folded && b.Folded {
		return 0
	}
	if a.Folded {
		return -1
	}
	if b.Folded {
		return 1
	}

	// Invalid (Daoba) check
	if a.Invalid && b.Invalid {
		return 0
	}
	if a.Invalid {
		return -1
	}
	if b.Invalid {
		return 1
	}

	headCmp := compareScore(a.HeadScore, b.HeadScore)
	tailCmp := compareScore(a.TailScore, b.TailScore)

	// Win: Head >= 0 AND Tail >= 0 AND (Head > 0 OR Tail > 0)
	if headCmp >= 0 && tailCmp >= 0 && (headCmp > 0 || tailCmp > 0) {
		return 1
	}
	// Lose: Head <= 0 AND Tail <= 0 AND (Head < 0 OR Tail < 0)
	if headCmp <= 0 && tailCmp <= 0 && (headCmp < 0 || tailCmp < 0) {
		return -1
	}

	return 0
}

func compareScore(a, b int64) int {
	switch {
	case a > b:
		return 1
	case a < b:
		return -1
	default:
		return 0
	}
}

func chexuanHeadMaxRank(cards []string) int {
	maxRank := 0
	for _, code := range cards {
		if c, ok := chexuanCardByCode(code); ok {
			if c.Rank > maxRank {
				maxRank = c.Rank
			}
		}
	}
	return maxRank
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func (rt *TableRuntime) applyMangoSettlementLocked(results *[]PlayerResult, showdown bool) {
	if rt.basePi <= 0 {
		return
	}

	round1Bet := rt.round1Bet || rt.raisedRound1
	round2Bet := rt.round2Bet || rt.raisedRound2 || rt.round2Knock
	newStreak := rt.mangoStreak
	if showdown || rt.round >= 3 {
		newStreak = 0
	} else if round2Bet {
		newStreak = 0
	} else if round1Bet {
		if newStreak < 3 {
			newStreak++
		}
	} else if newStreak == 3 {
		newStreak = 3
	} else {
		newStreak = 0
	}

	mangoValue := int64(newStreak) * 2 * rt.basePi
	if mangoValue > 0 && results != nil && len(*results) > 0 {
		// find winner (max NetPoints)
		winIdx := 0
		for i := 1; i < len(*results); i++ {
			if (*results)[i].NetPoints > (*results)[winIdx].NetPoints {
				winIdx = i
			}
		}
		loserIdx := make([]int, 0, len(*results)-1)
		for i := range *results {
			if i == winIdx {
				continue
			}
			loserIdx = append(loserIdx, i)
		}
		if len(loserIdx) > 0 {
			share := mangoValue / int64(len(loserIdx))
			remainder := mangoValue - share*int64(len(loserIdx))
			for i, idx := range loserIdx {
				(*results)[idx].NetPoints -= share
				if i == 0 && remainder > 0 {
					(*results)[idx].NetPoints -= remainder
				}
			}
			(*results)[winIdx].NetPoints += mangoValue
		}
		if (*results)[winIdx].Meta == nil {
			(*results)[winIdx].Meta = make(map[string]interface{})
		}
		(*results)[winIdx].Meta["mangoValue"] = mangoValue
		(*results)[winIdx].Meta["mangoStreak"] = newStreak
	}

	rt.mangoStreak = newStreak
}

func (rt *TableRuntime) finishWithResultsLocked(results []PlayerResult) {
	rt.phase = PhaseEnded
	rt.turnSeat = 0
	rt.cancelTimerLocked()
	rt.SettlementResults = results // Store for callback
	rt.broadcastStateLocked()

	if rt.onFinish != nil {
		go rt.onFinish(rt)
	}
}

// Temporary hook for internal use
func (rt *TableRuntime) onFinishWithResults(results []PlayerResult) {
	// No-op, logic moved to finishWithResultsLocked -> onFinish
}

type actionEntry struct {
	Seq    int64                  `json:"seq"`
	TS     int64                  `json:"ts"`
	Action string                 `json:"action"`
	Seat   int                    `json:"seat"`
	Amount int64                  `json:"amount,omitempty"`
	Meta   map[string]interface{} `json:"meta,omitempty"`
}

func (rt *TableRuntime) persistRoundLogLocked(entry actionEntry, includeCards ...bool) {
	if rt.db == nil || rt.matchID == 0 {
		return
	}
	entry.Seq = rt.nextSeqLocked()
	entry.TS = time.Now().UnixMilli()
	actions := []actionEntry{entry}

	payload := struct {
		MatchID   int64                  `json:"matchId"`
		RoundNo   int                    `json:"roundNo"`
		Actions   []actionEntry          `json:"actions"`
		CardsJSON map[string]string      `json:"cards,omitempty"`
		Meta      map[string]interface{} `json:"meta,omitempty"`
	}{
		MatchID: rt.matchID,
		RoundNo: rt.round,
		Actions: actions,
	}

	if len(includeCards) > 0 && includeCards[0] {
		payload.CardsJSON = rt.encryptCardsForLogLocked()
	}

	actionsRaw, _ := json.Marshal(payload.Actions)
	var cardsRaw datatypes.JSON
	if payload.CardsJSON != nil {
		if b, err := json.Marshal(payload.CardsJSON); err == nil {
			cardsRaw = datatypes.JSON(b)
		}
	}

	log := model.MatchRoundLog{
		MatchID:     rt.matchID,
		RoundNo:     rt.round,
		ActionsJSON: actionsRaw,
		CardsJSON:   cardsRaw,
		CreatedAt:   time.Now(),
	}

	go func(l model.MatchRoundLog) {
		_ = rt.db.Create(&l).Error
	}(log)
}

func (rt *TableRuntime) encryptCardsForLogLocked() map[string]string {
	result := make(map[string]string)
	for _, seat := range rt.seats {
		if len(seat.cards) == 0 || seat.UserID == 0 {
			continue
		}
		plain, _ := json.Marshal(seat.cards)
		enc, err := encryptForUser(seat.UserID, plain)
		if err != nil {
			continue
		}
		result[strconv.FormatInt(seat.UserID, 10)] = enc
	}
	return result
}

func encryptForUser(userID int64, data []byte) (string, error) {
	keyMaterial := sha256.Sum256([]byte(strconv.FormatInt(userID, 10)))
	block, err := aes.NewCipher(keyMaterial[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(crand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nil, nonce, data, nil)
	buf := bytes.NewBuffer(nonce)
	buf.Write(ciphertext)
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

func (rt *TableRuntime) playersSnapshot() []int64 {
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
	alias := fmt.Sprintf("玩家%d", userID)
	var seatPtr *SeatState
	if seatIdx, ok := rt.seatByUser[userID]; ok {
		seatPtr = rt.findSeatLocked(seatIdx)
		if seatPtr != nil && seatPtr.Alias != "" {
			alias = seatPtr.Alias
		}
	}
	content := fmt.Sprintf("%s %s", alias, rt.describeActionForLog(action, seatPtr))
	rt.logs = append(rt.logs, LogItem{
		ID:        fmt.Sprintf("%d-%d", time.Now().UnixNano(), len(rt.logs)+1),
		Timestamp: time.Now().UnixMilli(),
		Content:   content,
	})
}

func (rt *TableRuntime) describeActionForLog(action string, seat *SeatState) string {
	switch {
	case strings.HasPrefix(action, "auto_pass"):
		return "超时自动过牌"
	case strings.HasPrefix(action, "auto_fold"):
		return "超时自动弃牌"
	case strings.HasPrefix(action, "knock_bobo"):
		return "敲波波"
	case action == "fold":
		return "弃牌"
	case action == "pass":
		return "过牌"
	case action == "call":
		if seat != nil {
			return fmt.Sprintf("跟注至 %d", seat.Bet)
		}
		return "跟注"
	case action == "raise":
		if seat != nil {
			return fmt.Sprintf("加注至 %d", seat.Bet)
		}
		return "加注"
	case action == "ready":
		return "准备"
	default:
		return action
	}
}

func (rt *TableRuntime) resetTurnTimerLocked() {
	rt.cancelTimerLocked()
	rt.turnDeadline = time.Now().Add(defaultTurnSeconds * time.Second)
	rt.timer = time.NewTimer(defaultTurnSeconds * time.Second)
	rt.timerC = rt.timer.C
}

func (rt *TableRuntime) handleTurnTimeoutLocked() {
	if rt.phase != PhasePlaying {
		return
	}

	logger.Log.Warn("turn timeout auto-action",
		zap.Int64("tableID", rt.tableID),
		zap.Int("seat", rt.turnSeat),
	)
	if rt.canPassLocked(rt.turnSeat) {
		rt.markActedLocked(rt.turnSeat)
		rt.appendLogLocked("auto_pass", 0)
	} else {
		rt.markSeatStatusLocked(rt.turnSeat, "folded")
		rt.markActedLocked(rt.turnSeat)
		rt.appendLogLocked("auto_fold", 0)
	}

	if rt.shouldSettleLocked() {
		if rt.round == 2 && rt.round2Bet {
			rt.tailBigWin = true
		}
		rt.phase = PhaseSettling
		rt.determineWinnersAndSettleLocked()
		return
	}

	if rt.shouldAdvanceRoundLocked() {
		rt.advanceRoundLocked()
		if rt.phase != PhaseSettling {
			rt.broadcastStateLocked()
		}
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
	rt.timerC = nil
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

	var scene model.Scene
	if err := s.db.WithContext(ctx).First(&scene, table.SceneID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, appErr.ErrSceneNotFound
		}
		return nil, err
	}

	rt, err := newTableRuntime(s.db, table, scene, matchID, s.handleRuntimeFinish)
	if err != nil {
		return nil, err
	}
	s.runtimes.Store(tableID, rt)
	return rt, nil
}

// ginH is a tiny helper to avoid importing gin in this file.
type ginH map[string]interface{}

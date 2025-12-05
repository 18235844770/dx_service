package model

import (
	"time"

	"gorm.io/datatypes"
)

// 2.1 User & Agent

type User struct {
	ID           int64  `gorm:"primaryKey;autoIncrement"`
	Phone        string `gorm:"unique;not null"`
	Nickname     string
	Avatar       string
	LocationCity string
	GPSLat       float64
	GPSLng       float64
	InviteCode   string `gorm:"unique"`
	BindAgentID  *int64
	AgentPath    string // "A>B>C"
	Status       string `gorm:"default:normal;not null"` // normal/banned
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Admin struct {
	ID           int64  `gorm:"primaryKey;autoIncrement"`
	Username     string `gorm:"unique;not null"`
	PasswordHash string `gorm:"not null"`
	DisplayName  string
	Status       string `gorm:"default:active;not null"` // active/disabled
	LastLoginAt  *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Agent struct {
	ID           int64 `gorm:"primaryKey"` // Same as User.ID
	Level        int   `gorm:"default:1"`
	TotalInvited int   `gorm:"default:0"`
	TotalProfit  int64 `gorm:"default:0"`
	CreatedAt    time.Time
}

type AgentProfitLog struct {
	ID           int64 `gorm:"primaryKey;autoIncrement"`
	AgentID      int64
	FromUserID   int64
	MatchID      int64
	Level        int
	RakeAmount   int64
	ProfitAmount int64
	CreatedAt    time.Time
}

// 2.2 Wallet & Billing

type Wallet struct {
	UserID           int64 `gorm:"primaryKey"`
	BalanceTotal     int64
	BalanceAvailable int64
	BalanceFrozen    int64
	TotalRecharge    int64
	TotalWin         int64
	TotalConsume     int64
	TotalRake        int64
	UpdatedAt        time.Time
}

type RechargeOrder struct {
	ID         int64 `gorm:"primaryKey;autoIncrement"`
	UserID     int64
	AmountCNY  int
	Points     int64
	Status     string // pending/success/failed/refunded
	Channel    string
	CreatedAt  time.Time
	PaidAt     *time.Time
	OutTradeNo string `gorm:"unique"`
}

type BillingLog struct {
	ID           int64 `gorm:"primaryKey;autoIncrement"`
	UserID       int64
	Type         string // freeze/unfreeze/win/lose/rake/agent_share/platform_income/recharge/adjust
	Delta        int64
	BalanceAfter int64
	MatchID      *int64
	MetaJSON     datatypes.JSON `gorm:"type:jsonb"`
	CreatedAt    time.Time
}

// 2.3 Scene, Table, Match

type Scene struct {
	ID                 int64 `gorm:"primaryKey;autoIncrement"`
	Name               string
	SeatCount          int
	MinIn              int64
	MaxIn              int64
	BasePi             int64 // 皮
	MinUnitPi          int64 // 屁
	MangoEnabled       bool
	BoboEnabled        bool
	DistanceThresholdM int
	Status             string `gorm:"default:enabled"` // enabled/disabled
	RakeRuleID         int64
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type RakeRule struct {
	ID          int64          `gorm:"primaryKey;autoIncrement"`
	Name        string         `gorm:"size:128"`
	Type        string         // ratio/fixed/ladder
	Remark      string         `gorm:"size:255"`
	Status      string         `gorm:"default:enabled"` // enabled/disabled
	ConfigJSON  datatypes.JSON `gorm:"type:jsonb"`
	EffectiveAt *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type AgentRule struct {
	ID                int64 `gorm:"primaryKey;autoIncrement"`
	MaxLevel          int
	LevelRatiosJSON   datatypes.JSON `gorm:"type:jsonb"` // { "L1":0.4,"L2":0.1... }
	BasePlatformRatio float64        `gorm:"default:0.6"`
}

type Table struct {
	ID          int64 `gorm:"primaryKey;autoIncrement"`
	SceneID     int64
	Status      string // waiting/playing/ended
	SeatCount   int
	MangoStreak int            `gorm:"default:0"`
	PlayersJSON datatypes.JSON `gorm:"type:jsonb"` // seat->userId->alias
	CreatedAt   time.Time
}

type Match struct {
	ID         int64 `gorm:"primaryKey;autoIncrement"`
	TableID    int64
	SceneID    int64
	ResultJSON datatypes.JSON `gorm:"type:jsonb"`
	RakeJSON   datatypes.JSON `gorm:"type:jsonb"`
	CreatedAt  time.Time
	EndedAt    *time.Time
}

type MatchRoundLog struct {
	ID          int64 `gorm:"primaryKey;autoIncrement"`
	MatchID     int64
	RoundNo     int
	ActionsJSON datatypes.JSON `gorm:"type:jsonb"`
	CardsJSON   datatypes.JSON `gorm:"type:jsonb"`
	CreatedAt   time.Time
}

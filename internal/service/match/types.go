package match

import "time"

type JoinQueueRequest struct {
	UserID  int64
	SceneID int64
	BuyIn   int64
	GPSLat  float64
	GPSLng  float64
	IP      string
}

type CancelQueueRequest struct {
	UserID  int64
	SceneID int64
	Reason  string
}

type QueueStatus string

const (
	QueueStatusIdle    QueueStatus = "idle"
	QueueStatusQueued  QueueStatus = "queued"
	QueueStatusMatched QueueStatus = "matched"
)

type StatusResult struct {
	Status   QueueStatus `json:"status"`
	SceneID  int64       `json:"sceneId,omitempty"`
	TableID  *int64      `json:"tableId,omitempty"`
	MatchID  *int64      `json:"matchId,omitempty"`
	JoinedAt *time.Time  `json:"joinedAt,omitempty"`
}

type queueMember struct {
	UserID          int64     `json:"userId"`
	SceneID         int64     `json:"sceneId"`
	BuyIn           int64     `json:"buyIn"`
	GPSLat          float64   `json:"gpsLat"`
	GPSLng          float64   `json:"gpsLng"`
	IP              string    `json:"ip"`
	BalanceSnapshot int64     `json:"balanceSnapshot"`
	JoinedAt        time.Time `json:"joinedAt"`
}

type matchNotifyPayload struct {
	SceneID int64 `json:"sceneId"`
	TableID int64 `json:"tableId"`
	MatchID int64 `json:"matchId"`
}

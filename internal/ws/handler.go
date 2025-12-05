package ws

import (
	"dx-service/pkg/logger"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for dev
	},
}

func HandleTableWS(c *gin.Context) {
	tableID := c.Param("tableId")
	token := c.Query("token")

	// TODO: Validate token and get user
	userID := int64(123) // Dummy user ID

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		logger.Log.Error("Failed to upgrade websocket", zap.Error(err))
		return
	}

	logger.Log.Info("New WebSocket connection",
		zap.String("tableID", tableID),
		zap.String("token", token),
		zap.Int64("userID", userID),
	)

	// Handle connection (read/write loop)
	// For now just echo
	go func() {
		defer conn.Close()
		for {
			mt, message, err := conn.ReadMessage()
			if err != nil {
				logger.Log.Info("WS read error", zap.Error(err))
				break
			}
			logger.Log.Debug("Received message", zap.String("msg", string(message)))
			err = conn.WriteMessage(mt, message)
			if err != nil {
				break
			}
		}
	}()
}

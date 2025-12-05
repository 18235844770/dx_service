package ws

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dx-service/internal/service/game"
	"dx-service/internal/service/match"
	pkgAuth "dx-service/pkg/auth"
	appErr "dx-service/pkg/errors"
	"dx-service/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

type Handler struct {
	matchSvc *match.Service
	gameSvc  *game.Service
}

func NewHandler(matchSvc *match.Service, gameSvc *game.Service) *Handler {
	return &Handler{matchSvc: matchSvc, gameSvc: gameSvc}
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for dev
	},
}

func (h *Handler) HandleTableWS(c *gin.Context) {
	tableIDStr := c.Param("tableId")
	tableID, err := strconv.ParseInt(tableIDStr, 10, 64)
	if err != nil || tableID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid table id"})
		return
	}

	token, err := getTokenFromRequest(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	claims, err := pkgAuth.ParseUserToken(token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
		return
	}
	userID := claims.SubjectID

	if err := h.matchSvc.ValidateTableAccess(c.Request.Context(), userID, tableID); err != nil {
		switch {
		case errors.Is(err, appErr.ErrUnauthorized):
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		case errors.Is(err, appErr.ErrTableNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "table not found"})
		case errors.Is(err, appErr.ErrTableAccessDenied):
			c.JSON(http.StatusForbidden, gin.H{"error": "table access denied"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to validate table access"})
		}
		return
	}

	rt, err := h.gameSvc.GetRuntime(c.Request.Context(), tableID)
	if err != nil {
		if errors.Is(err, appErr.ErrTableNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "table not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load table"})
		return
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		logger.Log.Error("Failed to upgrade websocket", zap.Error(err))
		return
	}

	logger.Log.Info("New WebSocket connection",
		zap.Int64("tableID", tableID),
		zap.Int64("userID", userID),
	)

	client := newClient(conn, userID, tableID, rt)
	client.run()
}

func getTokenFromRequest(c *gin.Context) (string, error) {
	token := strings.TrimSpace(c.Query("token"))
	if token != "" {
		return token, nil
	}
	authHeader := strings.TrimSpace(c.GetHeader("Authorization"))
	if authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			token = strings.TrimSpace(parts[1])
			if token != "" {
				return token, nil
			}
		}
	}
	return "", errors.New("missing token")
}

type client struct {
	conn      *websocket.Conn
	userID    int64
	tableID   int64
	rt        *game.TableRuntime
	outbound  <-chan game.OutgoingMessage
	done      chan struct{}
	pingEvery time.Duration
}

func newClient(conn *websocket.Conn, userID, tableID int64, rt *game.TableRuntime) *client {
	conn.SetReadLimit(1 << 20)
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	return &client{
		conn:      conn,
		userID:    userID,
		tableID:   tableID,
		rt:        rt,
		outbound:  rt.Subscribe(userID),
		done:      make(chan struct{}),
		pingEvery: 25 * time.Second,
	}
}

func (c *client) run() {
	go c.writePump()
	c.readPump()
}

func (c *client) readPump() {
	defer func() {
		close(c.done)
		c.rt.Unsubscribe(c.userID)
		c.conn.Close()
	}()

	for {
		mt, message, err := c.conn.ReadMessage()
		if err != nil {
			logger.Log.Info("WS read error", zap.Error(err), zap.Int64("userID", c.userID), zap.Int64("tableID", c.tableID))
			return
		}
		if mt != websocket.TextMessage && mt != websocket.BinaryMessage {
			continue
		}

		var incoming struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(message, &incoming); err != nil {
			c.safeWrite(game.OutgoingMessage{
				Type: "error",
				Seq:  0,
				Data: gin.H{"message": "invalid payload"},
			})
			continue
		}
		if incoming.Type == "" {
			continue
		}

		if err := c.rt.HandleAction(c.userID, incoming.Type, incoming.Data); err != nil {
			c.safeWrite(game.OutgoingMessage{
				Type: "error",
				Seq:  0,
				Data: gin.H{"message": fmt.Sprintf("action failed: %v", err)},
			})
		}
	}
}

func (c *client) writePump() {
	ticker := time.NewTicker(c.pingEvery)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.outbound:
			if !ok {
				return
			}
			if err := c.conn.WriteJSON(msg); err != nil {
				logger.Log.Info("WS write error", zap.Error(err), zap.Int64("userID", c.userID), zap.Int64("tableID", c.tableID))
				return
			}
		case <-ticker.C:
			if err := c.conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(5*time.Second)); err != nil {
				return
			}
		case <-c.done:
			return
		}
	}
}

func (c *client) safeWrite(msg game.OutgoingMessage) {
	if err := c.conn.WriteJSON(msg); err != nil {
		logger.Log.Info("WS write error", zap.Error(err), zap.Int64("userID", c.userID), zap.Int64("tableID", c.tableID))
	}
}

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dx-service/internal/middleware"
	"dx-service/internal/service"
	agentSvc "dx-service/internal/service/agent"
	"dx-service/internal/service/match"
	rakeSvc "dx-service/internal/service/rake"
	sceneSvc "dx-service/internal/service/scene"
	usersvc "dx-service/internal/service/user"
	walletsvc "dx-service/internal/service/wallet"
	"dx-service/internal/ws"
	appErr "dx-service/pkg/errors"
	"dx-service/pkg/response"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Handler struct {
	services *service.Container
}

func RegisterRoutes(r *gin.Engine, services *service.Container) {
	handler := &Handler{services: services}
	wsHandler := ws.NewHandler(services.Match, services.Game)

	r.GET("/ping", func(c *gin.Context) {
		response.Success(c, gin.H{"message": "pong"})
	})

	v1 := r.Group("/dxService/v1")
	{
		authGroup := v1.Group("/auth")
		{
			authGroup.POST("/sms/send", handler.SendSMSCode)
			authGroup.POST("/sms/login", handler.SMSLogin)
		}

		userGroup := v1.Group("/user")
		userGroup.Use(middleware.AuthRequired())
		{
			userGroup.GET("/profile", handler.GetProfile)
			userGroup.PUT("/profile", handler.UpdateProfile)
		}

		v1.GET("/scenes", handler.ListScenes)
		v1.GET("/wallet", handler.GetWallet)

		matchGroup := v1.Group("/match")
		matchGroup.Use(middleware.AuthRequired())
		{
			matchGroup.POST("/join", handler.MatchJoin)
			matchGroup.POST("/cancel", handler.MatchCancel)
			matchGroup.GET("/status", handler.MatchStatus)
		}
	}

	adminGroup := r.Group("/admin")
	{
		adminGroup.POST("/auth/login", handler.AdminLogin)

		protected := adminGroup.Group("/")
		protected.Use(middleware.AdminAuthRequired())
		{
			protected.GET("/scenes", handler.AdminListScenes)
			protected.POST("/scenes", handler.AdminCreateScene)
			protected.PUT("/scenes/:id", handler.AdminUpdateScene)

			protected.GET("/rake_rules", handler.AdminListRakeRules)
			protected.POST("/rake_rules", handler.AdminCreateRakeRule)
			protected.PUT("/rake_rules/:id", handler.AdminUpdateRakeRule)

			protected.GET("/agent_rules", handler.AdminListAgentRules)
			protected.POST("/agent_rules", handler.AdminCreateAgentRule)
			protected.PUT("/agent_rules/:id", handler.AdminUpdateAgentRule)

			protected.GET("/users", handler.AdminListUsers)
			protected.GET("/users/:id", handler.AdminGetUser)
			protected.PUT("/users/:id/ban", handler.AdminBanUser)
			protected.PUT("/users/:id/wallet", handler.AdminSetUserWallet)
		}
	}

	r.GET("/ws/table/:tableId", wsHandler.HandleTableWS)
}

type smsSendBody struct {
	Phone string `json:"phone" binding:"required"`
}

type smsLoginBody struct {
	Phone      string `json:"phone" binding:"required"`
	Code       string `json:"code" binding:"required"`
	InviteCode string `json:"inviteCode"`
}

type matchJoinBody struct {
	SceneID int64   `json:"sceneId" binding:"required"`
	BuyIn   int64   `json:"buyIn" binding:"required,min=1"`
	GPSLat  float64 `json:"gpsLat"`
	GPSLng  float64 `json:"gpsLng"`
}

type matchCancelBody struct {
	SceneID int64 `json:"sceneId" binding:"required"`
}

type updateProfileBody struct {
	Nickname     *string  `json:"nickname"`
	Avatar       *string  `json:"avatar"`
	LocationCity *string  `json:"locationCity"`
	GPSLat       *float64 `json:"gpsLat"`
	GPSLng       *float64 `json:"gpsLng"`
}

type adminLoginBody struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type adminUserBanBody struct {
	Status string `json:"status" binding:"required"`
	Reason string `json:"reason"`
}

type adminSetWalletBody struct {
	BalanceAvailable *int64 `json:"balanceAvailable"`
	BalanceFrozen    *int64 `json:"balanceFrozen"`
}

type sceneMutationBody struct {
	Name               string `json:"name" binding:"required"`
	SeatCount          int    `json:"seatCount" binding:"required,min=2,max=9"`
	MinIn              int64  `json:"minIn" binding:"required,min=0"`
	MaxIn              int64  `json:"maxIn" binding:"required,min=0"`
	BasePi             int64  `json:"basePi" binding:"required,min=1"`
	MinUnitPi          int64  `json:"minUnitPi" binding:"required,min=1"`
	MangoEnabled       bool   `json:"mangoEnabled"`
	BoboEnabled        bool   `json:"boboEnabled"`
	DistanceThresholdM int    `json:"distanceThresholdM" binding:"min=0"`
	Status             string `json:"status" binding:"omitempty,oneof=enabled disabled"`
	RakeRuleID         int64  `json:"rakeRuleId" binding:"required,min=1"`
}

func (b sceneMutationBody) toParams() sceneSvc.SceneMutationParams {
	status := strings.ToLower(strings.TrimSpace(b.Status))
	if status == "" {
		status = "enabled"
	}
	return sceneSvc.SceneMutationParams{
		Name:               strings.TrimSpace(b.Name),
		SeatCount:          b.SeatCount,
		MinIn:              b.MinIn,
		MaxIn:              b.MaxIn,
		BasePi:             b.BasePi,
		MinUnitPi:          b.MinUnitPi,
		MangoEnabled:       b.MangoEnabled,
		BoboEnabled:        b.BoboEnabled,
		DistanceThresholdM: b.DistanceThresholdM,
		Status:             status,
		RakeRuleID:         b.RakeRuleID,
	}
}

type rakeRuleBody struct {
	Name        string          `json:"name" binding:"required"`
	Type        string          `json:"type" binding:"required"`
	Remark      string          `json:"remark"`
	ConfigJSON  json.RawMessage `json:"configJson" binding:"required"`
	Status      string          `json:"status" binding:"required"`
	EffectiveAt *string         `json:"effectiveAt"`
}

func (b rakeRuleBody) toParams() (rakeSvc.MutationParams, error) {
	status := strings.ToLower(strings.TrimSpace(b.Status))
	if status == "" {
		status = "enabled"
	}
	if status != "enabled" && status != "disabled" {
		return rakeSvc.MutationParams{}, fmt.Errorf("invalid status, must be enabled or disabled")
	}

	var effectiveAt *time.Time
	if b.EffectiveAt != nil && strings.TrimSpace(*b.EffectiveAt) != "" {
		ts, err := parseTimeWithLayouts(strings.TrimSpace(*b.EffectiveAt))
		if err != nil {
			return rakeSvc.MutationParams{}, err
		}
		effectiveAt = ts
	}

	return rakeSvc.MutationParams{
		Name:        strings.TrimSpace(b.Name),
		Type:        b.Type,
		Remark:      strings.TrimSpace(b.Remark),
		Status:      status,
		ConfigJSON:  b.ConfigJSON,
		EffectiveAt: effectiveAt,
	}, nil
}

type agentRuleBody struct {
	MaxLevel          int             `json:"maxLevel" binding:"required,min=1"`
	LevelRatiosJSON   json.RawMessage `json:"levelRatiosJson" binding:"required"`
	BasePlatformRatio float64         `json:"basePlatformRatio" binding:"required,gte=0,lte=1"`
}

func (b agentRuleBody) toParams() (agentSvc.MutationParams, error) {
	if strings.TrimSpace(string(b.LevelRatiosJSON)) == "" {
		return agentSvc.MutationParams{}, fmt.Errorf("levelRatiosJson is required")
	}
	if !json.Valid(b.LevelRatiosJSON) {
		return agentSvc.MutationParams{}, fmt.Errorf("levelRatiosJson must be valid JSON")
	}
	return agentSvc.MutationParams{
		MaxLevel:          b.MaxLevel,
		LevelRatiosJSON:   b.LevelRatiosJSON,
		BasePlatformRatio: b.BasePlatformRatio,
	}, nil
}

func (h *Handler) SendSMSCode(c *gin.Context) {
	var body smsSendBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.services.Auth.SendSMS(c.Request.Context(), body.Phone); err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	response.SuccessWithMsg(c, gin.H{}, "code sent")
}

func (h *Handler) SMSLogin(c *gin.Context) {
	var body smsLoginBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	resp, err := h.services.Auth.Login(c.Request.Context(), body.Phone, body.Code, body.InviteCode)
	if err != nil {
		status := http.StatusInternalServerError
		switch err {
		case appErr.ErrInvalidPhone, appErr.ErrInvalidSMSCode, appErr.ErrInviteCodeNotFound, appErr.ErrAlreadyBoundAgent:
			status = http.StatusBadRequest
		case appErr.ErrSMSCodeExpired:
			status = http.StatusGone
		case appErr.ErrUserBanned:
			status = http.StatusForbidden
		default:
			status = http.StatusInternalServerError
		}
		response.Error(c, status, err.Error())
		return
	}

	response.Success(c, resp)
}

func (h *Handler) AdminLogin(c *gin.Context) {
	var body adminLoginBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	resp, err := h.services.Admin.Login(c.Request.Context(), body.Username, body.Password)
	if err != nil {
		status := http.StatusInternalServerError
		switch err {
		case appErr.ErrAdminNotFound, appErr.ErrInvalidAdminPassword:
			status = http.StatusUnauthorized
		case appErr.ErrAdminDisabled:
			status = http.StatusForbidden
		default:
			status = http.StatusInternalServerError
		}
		response.Error(c, status, err.Error())
		return
	}

	response.Success(c, resp)
}

func (h *Handler) AdminListScenes(c *gin.Context) {
	page, err := parsePositiveIntQuery(c, "page", 1)
	if err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	size, err := parsePositiveIntQuery(c, "size", 20)
	if err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	result, err := h.services.Scene.AdminListScenes(c.Request.Context(), page, size)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	response.Success(c, gin.H{
		"items": result.Items,
		"total": result.Total,
		"page":  page,
		"size":  size,
	})
}

func (h *Handler) AdminCreateScene(c *gin.Context) {
	var body sceneMutationBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	scene, err := h.services.Scene.CreateScene(c.Request.Context(), body.toParams())
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			status = http.StatusConflict
		}
		response.Error(c, status, err.Error())
		return
	}

	response.Success(c, gin.H{"id": scene.ID})
}

func (h *Handler) AdminUpdateScene(c *gin.Context) {
	idStr := c.Param("id")
	sceneID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || sceneID <= 0 {
		response.Error(c, http.StatusBadRequest, "invalid scene id")
		return
	}

	var body sceneMutationBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	scene, err := h.services.Scene.UpdateScene(c.Request.Context(), sceneID, body.toParams())
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, appErr.ErrSceneNotFound):
			status = http.StatusNotFound
		case errors.Is(err, gorm.ErrDuplicatedKey):
			status = http.StatusConflict
		}
		response.Error(c, status, err.Error())
		return
	}

	response.Success(c, scene)
}

func (h *Handler) AdminListRakeRules(c *gin.Context) {
	page, err := parsePositiveIntQuery(c, "page", 1)
	if err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	size, err := parsePositiveIntQuery(c, "size", 20)
	if err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	result, err := h.services.Rake.List(c.Request.Context(), page, size)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	response.Success(c, gin.H{
		"items": result.Items,
		"total": result.Total,
		"page":  page,
		"size":  size,
	})
}

func (h *Handler) AdminCreateRakeRule(c *gin.Context) {
	var body rakeRuleBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	if !json.Valid(body.ConfigJSON) {
		response.Error(c, http.StatusBadRequest, "configJson must be valid JSON")
		return
	}

	params, err := body.toParams()
	if err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	rule, err := h.services.Rake.Create(c.Request.Context(), params)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	response.Success(c, gin.H{"id": rule.ID})
}

func (h *Handler) AdminUpdateRakeRule(c *gin.Context) {
	idStr := c.Param("id")
	ruleID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || ruleID <= 0 {
		response.Error(c, http.StatusBadRequest, "invalid rake rule id")
		return
	}

	var body rakeRuleBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	if !json.Valid(body.ConfigJSON) {
		response.Error(c, http.StatusBadRequest, "configJson must be valid JSON")
		return
	}

	params, err := body.toParams()
	if err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	rule, err := h.services.Rake.Update(c.Request.Context(), ruleID, params)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, appErr.ErrRakeRuleNotFound) {
			status = http.StatusNotFound
		}
		response.Error(c, status, err.Error())
		return
	}

	response.Success(c, rule)
}

func (h *Handler) AdminListAgentRules(c *gin.Context) {
	page, err := parsePositiveIntQuery(c, "page", 1)
	if err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	size, err := parsePositiveIntQuery(c, "size", 20)
	if err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	result, err := h.services.Agent.List(c.Request.Context(), page, size)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	response.Success(c, gin.H{
		"items": result.Items,
		"total": result.Total,
		"page":  page,
		"size":  size,
	})
}

func (h *Handler) AdminCreateAgentRule(c *gin.Context) {
	var body agentRuleBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	params, err := body.toParams()
	if err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	rule, err := h.services.Agent.Create(c.Request.Context(), params)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, appErr.ErrInvalidAgentRule) {
			status = http.StatusBadRequest
		}
		response.Error(c, status, err.Error())
		return
	}

	response.Success(c, gin.H{"id": rule.ID})
}

func (h *Handler) AdminUpdateAgentRule(c *gin.Context) {
	idStr := c.Param("id")
	ruleID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || ruleID <= 0 {
		response.Error(c, http.StatusBadRequest, "invalid agent rule id")
		return
	}

	var body agentRuleBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	params, err := body.toParams()
	if err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	rule, err := h.services.Agent.Update(c.Request.Context(), ruleID, params)
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, appErr.ErrAgentRuleNotFound):
			status = http.StatusNotFound
		case errors.Is(err, appErr.ErrInvalidAgentRule):
			status = http.StatusBadRequest
		}
		response.Error(c, status, err.Error())
		return
	}

	response.Success(c, rule)
}

func (h *Handler) AdminListUsers(c *gin.Context) {
	page, err := parsePositiveIntQuery(c, "page", 1)
	if err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	size, err := parsePositiveIntQuery(c, "size", 20)
	if err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	status := strings.ToLower(strings.TrimSpace(c.Query("status")))
	if status != "" && status != "normal" && status != "banned" {
		response.Error(c, http.StatusBadRequest, "invalid status filter")
		return
	}

	phone := strings.TrimSpace(c.Query("phone"))
	inviteCode := strings.TrimSpace(c.Query("inviteCode"))
	agentIDStr := strings.TrimSpace(c.Query("agentId"))
	var agentID *int64
	if agentIDStr != "" {
		id, parseErr := strconv.ParseInt(agentIDStr, 10, 64)
		if parseErr != nil || id <= 0 {
			response.Error(c, http.StatusBadRequest, "invalid agentId")
			return
		}
		agentID = &id
	}

	result, err := h.services.User.AdminListUsers(c.Request.Context(), usersvc.AdminListUsersFilter{
		Page:         page,
		Size:         size,
		Status:       status,
		PhoneKeyword: phone,
		InviteCode:   inviteCode,
		AgentID:      agentID,
	})
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	response.Success(c, gin.H{
		"items": result.Items,
		"total": result.Total,
		"page":  page,
		"size":  size,
	})
}

func (h *Handler) AdminGetUser(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || userID <= 0 {
		response.Error(c, http.StatusBadRequest, "invalid user id")
		return
	}

	user, err := h.services.User.AdminGetUser(c.Request.Context(), userID)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, appErr.ErrUserNotFound) {
			status = http.StatusNotFound
		}
		response.Error(c, status, err.Error())
		return
	}

	response.Success(c, gin.H{"user": user})
}

func (h *Handler) AdminBanUser(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || userID <= 0 {
		response.Error(c, http.StatusBadRequest, "invalid user id")
		return
	}

	var body adminUserBanBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	status := strings.ToLower(strings.TrimSpace(body.Status))
	if status != "normal" && status != "banned" {
		response.Error(c, http.StatusBadRequest, "status must be 'normal' or 'banned'")
		return
	}

	updated, err := h.services.User.AdminUpdateUserStatus(c.Request.Context(), userID, status, body.Reason)
	if err != nil {
		statusCode := http.StatusInternalServerError
		switch {
		case errors.Is(err, appErr.ErrUserNotFound):
			statusCode = http.StatusNotFound
		case errors.Is(err, appErr.ErrInvalidUserStatus):
			statusCode = http.StatusBadRequest
		}
		response.Error(c, statusCode, err.Error())
		return
	}

	response.Success(c, gin.H{"user": updated})
}

func (h *Handler) AdminSetUserWallet(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || userID <= 0 {
		response.Error(c, http.StatusBadRequest, "invalid user id")
		return
	}

	var body adminSetWalletBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	wallet, err := h.services.Wallet.AdminSetWallet(c.Request.Context(), userID, walletsvc.AdminSetWalletRequest{
		BalanceAvailable: body.BalanceAvailable,
		BalanceFrozen:    body.BalanceFrozen,
	})
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, appErr.ErrInvalidWalletPayload) {
			status = http.StatusBadRequest
		}
		response.Error(c, status, err.Error())
		return
	}

	response.Success(c, gin.H{"wallet": wallet})
}

func (h *Handler) MatchJoin(c *gin.Context) {
	var body matchJoinBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	userID, ok := getUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, "unauthorized")
		return
	}

	req := match.JoinQueueRequest{
		UserID:  userID,
		SceneID: body.SceneID,
		BuyIn:   body.BuyIn,
		GPSLat:  body.GPSLat,
		GPSLng:  body.GPSLng,
		IP:      c.ClientIP(),
	}

	queueID, err := h.services.Match.JoinQueue(c.Request.Context(), req)
	if err != nil {
		h.handleMatchError(c, err)
		return
	}

	response.Success(c, gin.H{
		"queueId": queueID,
		"status":  match.QueueStatusQueued,
	})
}

func (h *Handler) MatchCancel(c *gin.Context) {
	var body matchCancelBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, ok := getUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, "unauthorized")
		return
	}

	if err := h.services.Match.CancelQueue(c.Request.Context(), match.CancelQueueRequest{
		UserID:  userID,
		SceneID: body.SceneID,
		Reason:  "user_cancel",
	}); err != nil {
		h.handleMatchError(c, err)
		return
	}

	response.SuccessWithMsg(c, gin.H{"status": "cancelled"}, "")
}

func (h *Handler) MatchStatus(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, "unauthorized")
		return
	}

	sceneID, err := parseInt64Query(c, "sceneId")
	if err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	status, err := h.services.Match.GetStatus(c.Request.Context(), userID, sceneID)
	if err != nil {
		h.handleMatchError(c, err)
		return
	}

	response.Success(c, status)
}

func (h *Handler) ListScenes(c *gin.Context) {
	scenes, err := h.services.Scene.ListScenes(c.Request.Context())
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.Success(c, gin.H{"scenes": scenes})
}

func (h *Handler) GetWallet(c *gin.Context) {
	userID, err := parseInt64Query(c, "userId")
	if err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	wallet, err := h.services.Wallet.GetWallet(c.Request.Context(), userID)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.Success(c, gin.H{"wallet": wallet})
}

func (h *Handler) GetProfile(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	profile, err := h.services.User.GetProfile(c.Request.Context(), userID)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.Success(c, profile)
}

func (h *Handler) UpdateProfile(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, "unauthorized")
		return
	}

	var body updateProfileBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	updated, err := h.services.User.UpdateProfile(c.Request.Context(), userID, usersvc.UpdateProfileRequest{
		Nickname:     body.Nickname,
		Avatar:       body.Avatar,
		LocationCity: body.LocationCity,
		GPSLat:       body.GPSLat,
		GPSLng:       body.GPSLng,
	})
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.Success(c, updated)
}

func (h *Handler) handleMatchError(c *gin.Context, err error) {
	switch err {
	case appErr.ErrSceneNotFound:
		response.Error(c, http.StatusNotFound, err.Error())
	case appErr.ErrInvalidBuyIn:
		response.Error(c, http.StatusBadRequest, "买入金额不合法")
	case appErr.ErrInsufficientBalance:
		response.Error(c, http.StatusBadRequest, "余额不足")
	case appErr.ErrAlreadyInQueue:
		response.Error(c, http.StatusConflict, err.Error())
	case appErr.ErrQueueProcessing:
		response.Error(c, http.StatusTooManyRequests, err.Error())
	default:
		response.Error(c, http.StatusInternalServerError, err.Error())
	}
}

func parseInt64Query(c *gin.Context, key string) (int64, error) {
	val := c.Query(key)
	return strconv.ParseInt(val, 10, 64)
}

func parsePositiveIntQuery(c *gin.Context, key string, defaultVal int) (int, error) {
	val := c.Query(key)
	if val == "" {
		return defaultVal, nil
	}
	parsed, err := strconv.Atoi(val)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("invalid %s", key)
	}
	return parsed, nil
}

func getUserID(c *gin.Context) (int64, bool) {
	v, ok := c.Get(middleware.ContextUserIDKey)
	if !ok {
		return 0, false
	}
	id, ok := v.(int64)
	return id, ok
}

func parseTimeWithLayouts(value string) (*time.Time, error) {
	layouts := []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if ts, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return &ts, nil
		}
	}
	return nil, fmt.Errorf("invalid effectiveAt, expected RFC3339 or '2006-01-02 15:04:05'")
}

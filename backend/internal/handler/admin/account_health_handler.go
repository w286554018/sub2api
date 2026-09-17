package admin

import (
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type AccountHealthHandler struct {
	svc *service.AccountHealthService
}

func NewAccountHealthHandler(svc *service.AccountHealthService) *AccountHealthHandler {
	return &AccountHealthHandler{svc: svc}
}

func accountHealthActor(c *gin.Context) (int64, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "authentication required")
		return 0, false
	}
	return subject.UserID, true
}

func parseAccountHealthFilter(c *gin.Context) (service.AccountHealthFilter, bool) {
	filter := service.AccountHealthFilter{
		Page:     1,
		PageSize: 25,
		Search:   c.Query("search"),
		Platform: c.Query("platform"),
		State:    c.Query("state"),
	}
	for key, target := range map[string]*int{"page": &filter.Page, "page_size": &filter.PageSize} {
		raw := c.Query(key)
		if raw == "" {
			continue
		}
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			response.BadRequest(c, "invalid "+key)
			return filter, false
		}
		*target = value
	}
	return filter, true
}

func parseAccountHealthAccountID(c *gin.Context) (int64, bool) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "invalid account id")
		return 0, false
	}
	return accountID, true
}

func (h *AccountHealthHandler) Snapshot(c *gin.Context) {
	actorID, ok := accountHealthActor(c)
	if !ok {
		return
	}
	filter, ok := parseAccountHealthFilter(c)
	if !ok {
		return
	}
	out, err := h.svc.Snapshot(c.Request.Context(), actorID, filter)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *AccountHealthHandler) Settings(c *gin.Context) {
	actorID, ok := accountHealthActor(c)
	if !ok {
		return
	}
	out, err := h.svc.Settings(c.Request.Context(), actorID)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *AccountHealthHandler) UpdateSettings(c *gin.Context) {
	actorID, ok := accountHealthActor(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
	var input service.AccountHealthSettings
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "invalid account health settings")
		return
	}
	out, err := h.svc.UpdateSettings(c.Request.Context(), actorID, input)
	middleware.SetAuditExtra(c, map[string]any{
		"enabled":            input.Enabled,
		"window_minutes":     input.WindowMinutes,
		"min_samples":        input.MinSamples,
		"isolate_error_rate": input.IsolateErrorRate,
		"recover_error_rate": input.RecoverErrorRate,
		"cooldown_minutes":   input.CooldownMinutes,
		"interval_seconds":   input.IntervalSeconds,
	})
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *AccountHealthHandler) ManualIsolate(c *gin.Context) {
	actorID, ok := accountHealthActor(c)
	if !ok {
		return
	}
	accountID, ok := parseAccountHealthAccountID(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
	var input service.AccountHealthManualIsolationInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "invalid account health isolation request")
		return
	}
	out, err := h.svc.ManualIsolate(c.Request.Context(), actorID, accountID, input)
	middleware.SetAuditExtra(c, map[string]any{
		"account_id":       accountID,
		"duration_minutes": input.DurationMinutes,
		"applied":          err == nil && out != nil && out.Applied,
	})
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *AccountHealthHandler) ManualRecover(c *gin.Context) {
	actorID, ok := accountHealthActor(c)
	if !ok {
		return
	}
	accountID, ok := parseAccountHealthAccountID(c)
	if !ok {
		return
	}
	out, err := h.svc.ManualRecover(c.Request.Context(), actorID, accountID)
	middleware.SetAuditExtra(c, map[string]any{
		"account_id": accountID,
		"applied":    err == nil && out != nil && out.Applied,
	})
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

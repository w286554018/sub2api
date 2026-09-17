package admin

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type IntelligentTestHandler struct {
	svc *service.IntelligentTestService
}

func NewIntelligentTestHandler(svc *service.IntelligentTestService) *IntelligentTestHandler {
	return &IntelligentTestHandler{svc: svc}
}

func intelligentTestActor(c *gin.Context) (int64, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "authentication required")
		return 0, false
	}
	return subject.UserID, true
}

func parseIntelligentTestID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid id")
		return 0, false
	}
	return id, true
}

func parseIntelligentTestFilter(c *gin.Context) (service.IntelligentTestFilter, bool) {
	filter := service.IntelligentTestFilter{Page: 1, PageSize: 24, Search: c.Query("search"), Platform: c.Query("platform"), AccountType: c.Query("account_type"), AccountStatus: c.Query("account_status"), TestType: c.Query("test_type"), Status: c.Query("status")}
	if filter.AccountType == "" {
		filter.AccountType = c.Query("type")
	}
	for key, target := range map[string]*int{"page": &filter.Page, "page_size": &filter.PageSize} {
		if raw := c.Query(key); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 1 {
				response.BadRequest(c, "invalid "+key)
				return filter, false
			}
			*target = value
		}
	}
	if raw := c.Query("account_id"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 {
			response.BadRequest(c, "invalid account_id")
			return filter, false
		}
		filter.AccountID = value
	}
	for key, target := range map[string]**time.Time{"from": &filter.From, "to": &filter.To} {
		if raw := c.Query(key); raw != "" {
			value, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				response.BadRequest(c, "invalid "+key)
				return filter, false
			}
			*target = &value
		}
	}
	return filter, true
}

func (h *IntelligentTestHandler) Accounts(c *gin.Context) {
	actorID, ok := intelligentTestActor(c)
	if !ok {
		return
	}
	filter, ok := parseIntelligentTestFilter(c)
	if !ok {
		return
	}
	out, err := h.svc.Accounts(c.Request.Context(), actorID, filter)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *IntelligentTestHandler) Jobs(c *gin.Context) {
	actorID, ok := intelligentTestActor(c)
	if !ok {
		return
	}
	filter, ok := parseIntelligentTestFilter(c)
	if !ok {
		return
	}
	out, err := h.svc.Records(c.Request.Context(), actorID, filter)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *IntelligentTestHandler) GetJob(c *gin.Context) {
	actorID, ok := intelligentTestActor(c)
	if !ok {
		return
	}
	id, ok := parseIntelligentTestID(c)
	if !ok {
		return
	}
	out, err := h.svc.Get(c.Request.Context(), actorID, id)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *IntelligentTestHandler) Run(c *gin.Context) {
	actorID, ok := intelligentTestActor(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var req service.IntelligentTestEnqueue
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid test request")
		return
	}
	out, err := h.svc.Enqueue(c.Request.Context(), actorID, req)
	if !response.ErrorFrom(c, err) {
		response.Accepted(c, out)
	}
}

func (h *IntelligentTestHandler) Cancel(c *gin.Context) {
	actorID, ok := intelligentTestActor(c)
	if !ok {
		return
	}
	id, ok := parseIntelligentTestID(c)
	if !ok {
		return
	}
	out, err := h.svc.Cancel(c.Request.Context(), actorID, id)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *IntelligentTestHandler) Reevaluate(c *gin.Context) {
	actorID, ok := intelligentTestActor(c)
	if !ok {
		return
	}
	id, ok := parseIntelligentTestID(c)
	if !ok {
		return
	}
	out, err := h.svc.Reevaluate(c.Request.Context(), actorID, id)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

func (h *IntelligentTestHandler) Settings(c *gin.Context) {
	actorID, ok := intelligentTestActor(c)
	if !ok {
		return
	}
	out, err := h.svc.Settings(c.Request.Context(), actorID)
	if !response.ErrorFrom(c, err) {
		response.Success(c, gin.H{"items": out})
	}
}

func (h *IntelligentTestHandler) UpdateSetting(c *gin.Context) {
	actorID, ok := intelligentTestActor(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128<<10)
	var req service.IntelligentTestSetting
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid test settings")
		return
	}
	req.TestType = c.Param("test_type")
	if err := h.svc.UpdateSetting(c.Request.Context(), actorID, &req); !response.ErrorFrom(c, err) {
		response.Success(c, gin.H{"updated": true})
	}
}

func (h *IntelligentTestHandler) PreviewEvaluation(c *gin.Context) {
	actorID, ok := intelligentTestActor(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	var req struct {
		Output string                        `json:"output"`
		Config service.IntelligentTestConfig `json:"config"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid evaluation preview")
		return
	}
	out, err := h.svc.PreviewEvaluation(c.Request.Context(), actorID, req.Output, req.Config)
	if !response.ErrorFrom(c, err) {
		response.Success(c, out)
	}
}

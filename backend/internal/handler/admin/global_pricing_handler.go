package admin

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type GlobalPricingHandler struct {
	service *service.GlobalModelPricingService
}

func NewGlobalPricingHandler(svc *service.GlobalModelPricingService) *GlobalPricingHandler {
	return &GlobalPricingHandler{service: svc}
}

type globalPricingUpsertRequest struct {
	ModelPattern      string              `json:"model_pattern"`
	BillingMode       service.BillingMode `json:"billing_mode"`
	InputPrice        *float64            `json:"input_price"`
	OutputPrice       *float64            `json:"output_price"`
	CacheWritePrice   *float64            `json:"cache_write_price"`
	CacheWrite1hPrice *float64            `json:"cache_write_1h_price"`
	CacheReadPrice    *float64            `json:"cache_read_price"`
	PerRequestPrice   *float64            `json:"per_request_price"`
}

type globalPricingEnableRequest struct {
	Enabled *bool `json:"enabled"`
}

func (h *GlobalPricingHandler) requireService(c *gin.Context) bool {
	if h == nil || h.service == nil {
		response.ErrorFrom(c, errors.New("global pricing service unavailable"))
		return false
	}
	return true
}

func (h *GlobalPricingHandler) List(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	items, err := h.service.List(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"items": items, "count": len(items)})
}

func (h *GlobalPricingHandler) Create(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	var req globalPricingUpsertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	item, err := h.service.Create(c.Request.Context(), req.toInput())
	if err != nil {
		handleGlobalPricingError(c, err)
		return
	}
	response.Success(c, item)
}

func (h *GlobalPricingHandler) Update(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	id, ok := parseGlobalPricingID(c)
	if !ok {
		return
	}
	var req globalPricingUpsertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	item, err := h.service.Update(c.Request.Context(), id, req.toInput())
	if err != nil {
		handleGlobalPricingError(c, err)
		return
	}
	response.Success(c, item)
}

func (h *GlobalPricingHandler) Delete(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	id, ok := parseGlobalPricingID(c)
	if !ok {
		return
	}
	if err := h.service.Delete(c.Request.Context(), id); err != nil {
		handleGlobalPricingError(c, err)
		return
	}
	response.Success(c, gin.H{"message": "Global pricing deleted successfully"})
}

func (h *GlobalPricingHandler) SetEnabled(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	id, ok := parseGlobalPricingID(c)
	if !ok {
		return
	}
	var req globalPricingEnableRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if req.Enabled == nil {
		response.BadRequest(c, "Invalid request: enabled is required")
		return
	}
	item, err := h.service.SetEnabled(c.Request.Context(), id, *req.Enabled)
	if err != nil {
		handleGlobalPricingError(c, err)
		return
	}
	response.Success(c, item)
}

func (r globalPricingUpsertRequest) toInput() service.GlobalModelPriceInput {
	return service.GlobalModelPriceInput{
		ModelPattern:      r.ModelPattern,
		BillingMode:       r.BillingMode,
		InputPrice:        r.InputPrice,
		OutputPrice:       r.OutputPrice,
		CacheWritePrice:   r.CacheWritePrice,
		CacheWrite1hPrice: r.CacheWrite1hPrice,
		CacheReadPrice:    r.CacheReadPrice,
		PerRequestPrice:   r.PerRequestPrice,
	}
}

func parseGlobalPricingID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid pricing ID")
		return 0, false
	}
	return id, true
}

func handleGlobalPricingError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrGlobalModelPricingNotFound):
		response.NotFound(c, "Global pricing entry not found")
	case errors.Is(err, service.ErrGlobalModelPricingDuplicate):
		response.Error(c, http.StatusConflict, "This model already has a global pricing entry")
	default:
		response.ErrorFrom(c, err)
	}
}

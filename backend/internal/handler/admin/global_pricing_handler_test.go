package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type handlerGlobalPricingRepoStub struct {
	rows       []service.GlobalModelPrice
	next       int64
	lastUpdate service.GlobalModelPriceInput
}

func (s *handlerGlobalPricingRepoStub) ListEnabled(context.Context) ([]service.GlobalModelPrice, error) {
	out := make([]service.GlobalModelPrice, 0)
	for _, row := range s.rows {
		if row.Enabled {
			out = append(out, row)
		}
	}
	return out, nil
}

func (s *handlerGlobalPricingRepoStub) ListAll(context.Context) ([]service.GlobalModelPrice, error) {
	return append([]service.GlobalModelPrice(nil), s.rows...), nil
}

func (s *handlerGlobalPricingRepoStub) Create(_ context.Context, in service.GlobalModelPriceInput) (*service.GlobalModelPrice, error) {
	s.next++
	out := service.GlobalModelPrice{ID: s.next, ModelPattern: in.ModelPattern, BillingMode: in.BillingMode,
		InputPrice: in.InputPrice, OutputPrice: in.OutputPrice, PerRequestPrice: in.PerRequestPrice, Enabled: true}
	s.rows = append(s.rows, out)
	return &out, nil
}

func (s *handlerGlobalPricingRepoStub) GetByID(_ context.Context, id int64) (*service.GlobalModelPrice, error) {
	for i := range s.rows {
		if s.rows[i].ID == id {
			out := s.rows[i]
			return &out, nil
		}
	}
	return nil, sql.ErrNoRows
}

func (s *handlerGlobalPricingRepoStub) Update(_ context.Context, id int64, in service.GlobalModelPriceInput) (*service.GlobalModelPrice, error) {
	s.lastUpdate = in
	for i := range s.rows {
		if s.rows[i].ID == id {
			s.rows[i].ModelPattern = in.ModelPattern
			s.rows[i].BillingMode = in.BillingMode
			s.rows[i].InputPrice = in.InputPrice
			s.rows[i].OutputPrice = in.OutputPrice
			out := s.rows[i]
			return &out, nil
		}
	}
	return nil, sql.ErrNoRows
}

func (s *handlerGlobalPricingRepoStub) Delete(_ context.Context, id int64) error {
	for i := range s.rows {
		if s.rows[i].ID == id {
			s.rows = append(s.rows[:i], s.rows[i+1:]...)
			return nil
		}
	}
	return sql.ErrNoRows
}

func (s *handlerGlobalPricingRepoStub) SetEnabled(_ context.Context, id int64, enabled bool) (*service.GlobalModelPrice, error) {
	for i := range s.rows {
		if s.rows[i].ID == id {
			s.rows[i].Enabled = enabled
			out := s.rows[i]
			return &out, nil
		}
	}
	return nil, sql.ErrNoRows
}

func newGlobalPricingHandlerForTest(repo *handlerGlobalPricingRepoStub) *GlobalPricingHandler {
	return NewGlobalPricingHandler(service.NewGlobalModelPricingService(repo, nil))
}

func serveGlobalPricing(h *GlobalPricingHandler, method, target, body string, params gin.Params) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = params
	switch {
	case method == http.MethodGet:
		h.List(c)
	case method == http.MethodPost && strings.HasSuffix(target, "/enable"):
		h.SetEnabled(c)
	case method == http.MethodPost:
		h.Create(c)
	case method == http.MethodPut:
		h.Update(c)
	case method == http.MethodDelete:
		h.Delete(c)
	}
	return rec
}

func TestGlobalPricingHandlerCreateListAndToggle(t *testing.T) {
	repo := &handlerGlobalPricingRepoStub{}
	h := newGlobalPricingHandlerForTest(repo)
	rec := serveGlobalPricing(h, http.MethodPost, "/api/v1/admin/global-pricing",
		`{"model_pattern":"gpt-5","billing_mode":"token","input_price":0.000001,"output_price":0.000002}`, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, repo.rows[0].Enabled)

	rec = serveGlobalPricing(h, http.MethodPost, "/api/v1/admin/global-pricing/1/enable",
		`{"enabled":false}`, gin.Params{{Key: "id", Value: "1"}})
	require.Equal(t, http.StatusOK, rec.Code)
	var toggleResp struct {
		Data service.GlobalModelPrice `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &toggleResp))
	require.False(t, toggleResp.Data.Enabled)

	rec = serveGlobalPricing(h, http.MethodGet, "/api/v1/admin/global-pricing", "", nil)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestGlobalPricingHandlerUpdateDoesNotChangeEnabled(t *testing.T) {
	input, output := 0.000001, 0.000002
	repo := &handlerGlobalPricingRepoStub{rows: []service.GlobalModelPrice{{ID: 7, ModelPattern: "gpt-5", BillingMode: service.BillingModeToken, InputPrice: &input, OutputPrice: &output, Enabled: false}}}
	h := newGlobalPricingHandlerForTest(repo)
	rec := serveGlobalPricing(h, http.MethodPut, "/api/v1/admin/global-pricing/7",
		`{"model_pattern":"gpt-5","billing_mode":"token","input_price":0.000003,"output_price":0.000004,"enabled":true}`,
		gin.Params{{Key: "id", Value: "7"}})
	require.Equal(t, http.StatusOK, rec.Code)
	require.False(t, repo.rows[0].Enabled)
}

func TestGlobalPricingHandlerRejectsInvalidAndMissingEnabled(t *testing.T) {
	h := newGlobalPricingHandlerForTest(&handlerGlobalPricingRepoStub{})
	rec := serveGlobalPricing(h, http.MethodPost, "/api/v1/admin/global-pricing",
		`{"model_pattern":"*foo","billing_mode":"token","input_price":1,"output_price":2}`, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	rec = serveGlobalPricing(h, http.MethodPost, "/api/v1/admin/global-pricing/1/enable",
		`{}`, gin.Params{{Key: "id", Value: "1"}})
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGlobalPricingHandlerNotFound(t *testing.T) {
	h := newGlobalPricingHandlerForTest(&handlerGlobalPricingRepoStub{})
	params := gin.Params{{Key: "id", Value: "999"}}
	rec := serveGlobalPricing(h, http.MethodPut, "/api/v1/admin/global-pricing/999",
		`{"model_pattern":"gpt-5","billing_mode":"token","input_price":1,"output_price":2}`, params)
	require.Equal(t, http.StatusNotFound, rec.Code)

	rec = serveGlobalPricing(h, http.MethodDelete, "/api/v1/admin/global-pricing/999", "", params)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

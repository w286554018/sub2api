package admin

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountHealthSettingsUpdateRequiresSuperAdminRouteGuard(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewAccountHealthHandler(nil)
	router.PUT("/settings", func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
		c.Set(string(middleware.ContextKeyUserRole), "admin")
	}, middleware.SuperAdminOnly(), handler.UpdateSettings)

	req := httptest.NewRequest(http.MethodPut, "/settings", bytes.NewBufferString(`{"enabled":false,"window_minutes":10,"min_samples":10,"isolate_error_rate":0.5,"recover_error_rate":0.2,"cooldown_minutes":30,"interval_seconds":60}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
}

func TestParseAccountHealthFilterRejectsInvalidPagination(t *testing.T) {
	gin.SetMode(gin.TestMode)
	req := httptest.NewRequest(http.MethodGet, "/health?page=zero", nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req

	_, ok := parseAccountHealthFilter(c)

	require.False(t, ok)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

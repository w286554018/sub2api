package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newAccountHealthWriteRoutesTestRouter(role string, stepUpCalls *int) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handlers := &handler.Handlers{Admin: &handler.AdminHandlers{
		AccountHealth: adminhandler.NewAccountHealthHandler(nil),
	}}
	adminAuth := servermiddleware.AdminAuthMiddleware(func(c *gin.Context) {
		c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: 7})
		c.Set(string(servermiddleware.ContextKeyUserRole), role)
		c.Next()
	})
	auditLog := servermiddleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })
	stepUp := servermiddleware.StepUpAuthMiddleware(func(c *gin.Context) {
		(*stepUpCalls)++
		c.AbortWithStatus(http.StatusPreconditionRequired)
	})
	RegisterAdminRoutes(router.Group("/api/v1"), handlers, adminAuth, auditLog, stepUp, nil, nil)
	return router
}

func TestAccountHealthWritesRequireSuperAdminBeforeStepUp(t *testing.T) {
	stepUpCalls := 0
	router := newAccountHealthWriteRoutesTestRouter(service.RoleAdmin, &stepUpCalls)

	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodPut, "/api/v1/admin/account-health/settings", strings.NewReader(`{}`)),
		httptest.NewRequest(http.MethodPost, "/api/v1/admin/account-health/42/isolate", strings.NewReader(`{}`)),
		httptest.NewRequest(http.MethodDelete, "/api/v1/admin/account-health/42/isolation", nil),
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusForbidden, recorder.Code)
	}
	require.Zero(t, stepUpCalls)
}

func TestAccountHealthWritesReachStepUpBeforeHandlers(t *testing.T) {
	stepUpCalls := 0
	router := newAccountHealthWriteRoutesTestRouter(service.RoleSuperAdmin, &stepUpCalls)

	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodPut, "/api/v1/admin/account-health/settings", strings.NewReader(`{}`)),
		httptest.NewRequest(http.MethodPost, "/api/v1/admin/account-health/42/isolate", strings.NewReader(`{}`)),
		httptest.NewRequest(http.MethodDelete, "/api/v1/admin/account-health/42/isolation", nil),
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusPreconditionRequired, recorder.Code)
	}
	require.Equal(t, 3, stepUpCalls)
}

package admin

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestIntelligentTestPreviewRequiresSuperAdminRouteGuard(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewIntelligentTestHandler(service.NewIntelligentTestService(&intelligentTestHandlerRepo{admin: true}, nil))
	router.POST("/preview", func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
		c.Set(string(middleware.ContextKeyUserRole), "admin")
	}, middleware.SuperAdminOnly(), handler.PreviewEvaluation)

	req := httptest.NewRequest(http.MethodPost, "/preview", bytes.NewBufferString(`{"output":"ANSWER: 12","config":{"prompt":"p","evaluator":"exact_answer","expected_answer":"12","timeout_seconds":60}}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
}

type intelligentTestHandlerRepo struct {
	admin      bool
	superAdmin bool
}

func (r *intelligentTestHandlerRepo) IsAdmin(context.Context, int64) (bool, error) {
	return r.admin, nil
}
func (r *intelligentTestHandlerRepo) IsSuperAdmin(context.Context, int64) (bool, error) {
	return r.superAdmin, nil
}
func (r *intelligentTestHandlerRepo) Settings(context.Context) ([]service.IntelligentTestSetting, error) {
	return nil, nil
}
func (r *intelligentTestHandlerRepo) UpdateSetting(context.Context, int64, *service.IntelligentTestSetting) error {
	return nil
}
func (r *intelligentTestHandlerRepo) Enqueue(context.Context, int64, service.IntelligentTestEnqueue) (*service.IntelligentTestEnqueued, error) {
	return nil, nil
}
func (r *intelligentTestHandlerRepo) Accounts(context.Context, service.IntelligentTestFilter) (*service.IntelligentTestAccounts, error) {
	return nil, nil
}
func (r *intelligentTestHandlerRepo) Records(context.Context, service.IntelligentTestFilter) (*service.IntelligentTestRecords, error) {
	return nil, nil
}
func (r *intelligentTestHandlerRepo) Get(context.Context, int64) (*service.IntelligentTestRecord, error) {
	return nil, service.ErrIntelligentTestNotFound
}
func (r *intelligentTestHandlerRepo) Claim(context.Context, time.Duration) (*service.IntelligentTestRecord, error) {
	return nil, nil
}
func (r *intelligentTestHandlerRepo) Finish(context.Context, *service.IntelligentTestRecord) error {
	return nil
}
func (r *intelligentTestHandlerRepo) Defer(context.Context, *service.IntelligentTestRecord, time.Time, string) error {
	return nil
}
func (r *intelligentTestHandlerRepo) Cancel(context.Context, int64, int64) (*service.IntelligentTestRecord, error) {
	return nil, nil
}
func (r *intelligentTestHandlerRepo) Reevaluate(context.Context, int64, int64, func(*service.IntelligentTestRecord) error) (*service.IntelligentTestRecord, error) {
	return nil, nil
}

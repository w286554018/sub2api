package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type adminBillingRepoStub struct {
	service.UsageLogRepository
	statementUser int64
	exportUser    int64
}

func (s *adminBillingRepoStub) GetBillingStatementRows(
	_ context.Context,
	userID int64,
	_, _ time.Time,
) ([]service.BillingStatementRow, error) {
	s.statementUser = userID
	return []service.BillingStatementRow{}, nil
}

func (s *adminBillingRepoStub) ListBillingExportRows(
	_ context.Context,
	userID int64,
	_, _ time.Time,
	_ int,
) ([]service.BillingExportRow, error) {
	s.exportUser = userID
	return []service.BillingExportRow{}, nil
}

func newAdminBillingTestRouter(repo *adminBillingRepoStub) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := NewUsageHandler(service.NewUsageService(repo, nil, nil, nil), nil, nil, nil)
	router := gin.New()
	router.GET("/admin/billing/users/:userId/statement", h.BillingStatement)
	router.GET("/admin/billing/users/:userId/export", h.ExportBillingCSV)
	return router
}

func TestAdminBillingStatementTargetsRequestedUser(t *testing.T) {
	repo := &adminBillingRepoStub{}
	router := newAdminBillingTestRouter(repo)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/billing/users/77/statement?year=2026&month=9&timezone=UTC", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, int64(77), repo.statementUser)
}

func TestAdminBillingExportRejectsInvalidUserID(t *testing.T) {
	repo := &adminBillingRepoStub{}
	router := newAdminBillingTestRouter(repo)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/billing/users/nope/export?start_date=2026-09-01&end_date=2026-09-02", nil))

	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

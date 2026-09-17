package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type userBillingRepoStub struct {
	service.UsageLogRepository
	statementUser int64
	exportUser    int64
	exportStart   time.Time
	exportEnd     time.Time
}

func (s *userBillingRepoStub) GetBillingStatementRows(
	_ context.Context,
	userID int64,
	_, _ time.Time,
) ([]service.BillingStatementRow, error) {
	s.statementUser = userID
	return []service.BillingStatementRow{{Model: "gpt-5.5", Requests: 2, Cost: 0.5}}, nil
}

func (s *userBillingRepoStub) ListBillingExportRows(
	_ context.Context,
	userID int64,
	start time.Time,
	end time.Time,
	_ int,
) ([]service.BillingExportRow, error) {
	s.exportUser = userID
	s.exportStart = start
	s.exportEnd = end
	return []service.BillingExportRow{{
		CreatedAt: time.Date(2026, 9, 2, 3, 4, 5, 0, time.UTC),
		Model:     "gpt-5.5",
		RequestID: "req-1",
	}}, nil
}

func newUserBillingTestRouter(repo *userBillingRepoStub) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := NewUsageHandler(service.NewUsageService(repo, nil, nil, nil), nil, nil, nil)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 42})
		c.Next()
	})
	router.GET("/billing/statement", h.BillingStatement)
	router.GET("/billing/export", h.ExportBillingCSV)
	return router
}

func TestUserBillingStatementUsesAuthenticatedUser(t *testing.T) {
	repo := &userBillingRepoStub{}
	router := newUserBillingTestRouter(repo)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/billing/statement?year=2026&month=9&timezone=UTC", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, int64(42), repo.statementUser)
	require.Contains(t, recorder.Body.String(), `"requests":2`)
}

func TestUserBillingStatementRejectsMalformedMonth(t *testing.T) {
	repo := &userBillingRepoStub{}
	router := newUserBillingTestRouter(repo)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/billing/statement?year=2026&month=bad", nil))

	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestUserBillingExportUsesHalfOpenLocalDateRange(t *testing.T) {
	repo := &userBillingRepoStub{}
	router := newUserBillingTestRouter(repo)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/billing/export?start_date=2026-09-01&end_date=2026-09-03&timezone=UTC", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, int64(42), repo.exportUser)
	require.Equal(t, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), repo.exportStart)
	require.Equal(t, time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC), repo.exportEnd)
	require.Equal(t, "text/csv; charset=utf-8", recorder.Header().Get("Content-Type"))
	require.Contains(t, recorder.Header().Get("Content-Disposition"), "billing-42-20260901.csv")
}

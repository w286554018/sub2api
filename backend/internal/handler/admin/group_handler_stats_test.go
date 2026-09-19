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

type groupStatsHandlerRepo struct {
	service.UsageLogRepository
	stats *service.GroupDetailStats
	id    int64
	from  *time.Time
	to    *time.Time
}

func (r *groupStatsHandlerRepo) GetGroupDetailStats(ctx context.Context, id int64, from, to *time.Time) (*service.GroupDetailStats, error) {
	r.id = id
	r.from = from
	r.to = to
	if r.stats != nil {
		return r.stats, nil
	}
	return &service.GroupDetailStats{GroupID: id, GroupName: "pro", TotalRequests: 12, TotalCost: 3.5, GeneratedAt: time.Now().UTC()}, nil
}

func newGroupStatsTestRouter(repo *groupStatsHandlerRepo) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := NewGroupHandler(newStubAdminService(), service.NewDashboardService(repo, nil, nil, nil), nil)
	r := gin.New()
	r.GET("/groups/:id/stats", h.GetStats)
	return r
}

func TestGroupHandlerGetStatsReturnsRealStats(t *testing.T) {
	repo := &groupStatsHandlerRepo{}
	r := newGroupStatsTestRouter(repo)
	from := "2026-01-02T03:04:05Z"
	to := "2026-01-03T03:04:05Z"

	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/groups/42/stats?from="+from+"&to="+to, nil)
	r.ServeHTTP(res, req)

	require.Equal(t, http.StatusOK, res.Code)
	require.Equal(t, "no-store", res.Header().Get("Cache-Control"))
	require.Contains(t, res.Body.String(), `"group_id":42`)
	require.Contains(t, res.Body.String(), `"group_name":"pro"`)
	require.Contains(t, res.Body.String(), `"total_requests":12`)
	require.Equal(t, int64(42), repo.id)
	require.Equal(t, "2026-01-02T03:04:05Z", repo.from.Format(time.RFC3339))
	require.Equal(t, "2026-01-03T03:04:05Z", repo.to.Format(time.RFC3339))
}

func TestGroupHandlerGetStatsRejectsInvalidTime(t *testing.T) {
	r := newGroupStatsTestRouter(&groupStatsHandlerRepo{})

	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/groups/42/stats?from=not-a-time", nil)
	r.ServeHTTP(res, req)

	require.Equal(t, http.StatusBadRequest, res.Code)
}

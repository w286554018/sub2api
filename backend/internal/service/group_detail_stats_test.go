package service

import (
	"context"
	"errors"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type groupDetailStatsRepoStub struct {
	UsageLogRepository
	stats *GroupDetailStats
	err   error
	id    int64
	from  *time.Time
	to    *time.Time
}

func (s *groupDetailStatsRepoStub) GetGroupDetailStats(ctx context.Context, id int64, from, to *time.Time) (*GroupDetailStats, error) {
	s.id = id
	s.from = from
	s.to = to
	if s.err != nil {
		return nil, s.err
	}
	return s.stats, nil
}

func TestDashboardServiceGetGroupDetailStatsDelegatesRange(t *testing.T) {
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	want := &GroupDetailStats{GroupID: 7, GroupName: "OpenAI", TotalRequests: 12}
	repo := &groupDetailStatsRepoStub{stats: want}
	svc := NewDashboardService(repo, nil, nil, nil)

	got, err := svc.GetGroupDetailStats(context.Background(), 7, &from, &to)

	require.NoError(t, err)
	require.Same(t, want, got)
	require.Equal(t, int64(7), repo.id)
	require.Equal(t, from, *repo.from)
	require.Equal(t, to, *repo.to)
}

func TestDashboardServiceGetGroupDetailStatsRejectsInvalidRange(t *testing.T) {
	from := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	to := from
	repo := &groupDetailStatsRepoStub{}
	svc := NewDashboardService(repo, nil, nil, nil)

	got, err := svc.GetGroupDetailStats(context.Background(), 7, &from, &to)

	require.Nil(t, got)
	require.Error(t, err)
	require.Equal(t, "GROUP_STATS_INVALID_RANGE", infraerrors.Reason(err))
	require.Zero(t, repo.id)
}

func TestDashboardServiceGetGroupDetailStatsPropagatesRepositoryErrors(t *testing.T) {
	boom := errors.New("database unavailable")
	repo := &groupDetailStatsRepoStub{err: boom}
	svc := NewDashboardService(repo, nil, nil, nil)

	got, err := svc.GetGroupDetailStats(context.Background(), 7, nil, nil)

	require.Nil(t, got)
	require.ErrorIs(t, err, boom)
}

func TestDashboardServiceGetGroupDetailStatsRequiresProvider(t *testing.T) {
	svc := NewDashboardService(&usageRepoStub{}, nil, nil, nil)

	got, err := svc.GetGroupDetailStats(context.Background(), 7, nil, nil)

	require.Nil(t, got)
	require.Error(t, err)
	require.True(t, infraerrors.IsServiceUnavailable(err))
	require.Equal(t, "GROUP_STATS_UNAVAILABLE", infraerrors.Reason(err))
}

func TestDashboardServiceGetGroupDetailStatsPropagatesGroupNotFound(t *testing.T) {
	repo := &groupDetailStatsRepoStub{err: ErrGroupNotFound}
	svc := NewDashboardService(repo, nil, nil, nil)

	got, err := svc.GetGroupDetailStats(context.Background(), 99, nil, nil)

	require.Nil(t, got)
	require.ErrorIs(t, err, ErrGroupNotFound)
}

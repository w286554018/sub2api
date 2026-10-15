//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/qualityqueue/queuetest"
	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"
)

// Moving the models transport into its Vertex-compatible helper must retain
// the fork's immediate quality protection for AI Studio OAuth failures.
func TestSourceSyncGeminiModelsObserveQualityOnce(t *testing.T) {
	for _, endpoint := range []string{"list", "get"} {
		t.Run(endpoint, func(t *testing.T) {
			mr := miniredis.RunT(t)
			rdb := queuetest.NewClient(mr.Addr())
			t.Cleanup(func() { _ = rdb.Close() })
			repo := &immediateQualityAccountRepo{}
			limiter := ProvideRateLimitService(repo, nil, &config.Config{}, nil, nil, nil, nil, nil, nil, nil, nil, rdb)
			t.Cleanup(func() { _ = limiter.qualityTrigger.queue.Close() })
			upstream := &httpUpstreamRecorder{resp: vertexModelsJSONResponse(http.StatusServiceUnavailable, `{"error":{"code":503}}`)}
			svc := newGeminiModelsEndpointsTestService(nil, upstream)
			svc.rateLimitService = limiter
			account := &Account{ID: 261, Platform: PlatformGemini, Type: AccountTypeOAuth, Credentials: map[string]any{}}

			var result *UpstreamHTTPResult
			var err error
			if endpoint == "list" {
				result, err = svc.ForwardListModels(context.Background(), account)
			} else {
				result, err = svc.ForwardGetModel(context.Background(), account, "gemini-2.5-flash")
			}
			require.NoError(t, err)
			require.Equal(t, http.StatusServiceUnavailable, result.StatusCode)
			require.Len(t, upstream.requests, 1)
			require.Equal(t, 1, repo.calls, "one upstream failure must apply quality protection once")
			require.Equal(t, []string{"261"}, rdb.ZRange(context.Background(), quality5xxPendingKey, 0, -1).Val())
		})
	}
}

//go:build unit

package admin

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type codexProbeHandlerRepo struct {
	service.AccountRepository
	account           *service.Account
	clearErrorCalls   int
	clearRuntimeCalls int
	setErrors         []string
}

func (r *codexProbeHandlerRepo) GetByID(_ context.Context, id int64) (*service.Account, error) {
	if r.account.ID != id {
		return nil, service.ErrAccountNotFound
	}
	return r.account, nil
}

func (r *codexProbeHandlerRepo) ClearError(context.Context, int64) error {
	r.clearErrorCalls++
	r.account.Status = service.StatusActive
	return nil
}

func (r *codexProbeHandlerRepo) ClearRateLimit(context.Context, int64) error {
	r.clearRuntimeCalls++
	r.account.RateLimitedAt, r.account.RateLimitResetAt, r.account.OverloadUntil = nil, nil, nil
	return nil
}

func (r *codexProbeHandlerRepo) ClearModelRateLimits(context.Context, int64) error {
	r.clearRuntimeCalls++
	delete(r.account.Extra, "model_rate_limits")
	return nil
}

func (r *codexProbeHandlerRepo) ClearTempUnschedulable(context.Context, int64) error {
	r.clearRuntimeCalls++
	r.account.TempUnschedulableUntil = nil
	return nil
}

func (r *codexProbeHandlerRepo) ClearAntigravityQuotaScopes(context.Context, int64) error { return nil }
func (r *codexProbeHandlerRepo) UpdateExtra(context.Context, int64, map[string]any) error { return nil }
func (r *codexProbeHandlerRepo) SetError(_ context.Context, _ int64, message string) error {
	r.setErrors = append(r.setErrors, message)
	return nil
}

// Only the network boundary is replaced. The handler and both services are real.
type codexProbeOfflineUpstream struct {
	calls   atomic.Int32
	succeed bool
}

func (u *codexProbeOfflineUpstream) Do(*http.Request, string, int64, int) (*http.Response, error) {
	u.calls.Add(1)
	if !u.succeed {
		return nil, errors.New("offline: unexpected inference")
	}
	return &http.Response{
		StatusCode: http.StatusOK, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\"}\n\n")),
	}, nil
}

func (u *codexProbeOfflineUpstream) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

func TestAccountHandlerTestRecoveryFollowsProbeKind(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name     string
		enabled  bool
		typ      string
		status   int
		testBody string
	}{
		{"device-normal", true, service.AccountTypeOAuth, 200, `{"model_id":"gpt-5.5"}`},
		{"device-compact", true, service.AccountTypeOAuth, 200, `{"model_id":"gpt-5.5","mode":"compact"}`},
		{"device-image", true, service.AccountTypeOAuth, 200, `{"model_id":"gpt-image-2"}`},
		{"device-setup-token", true, service.AccountTypeSetupToken, 200, `{}`},
		{"legacy-inference", false, service.AccountTypeOAuth, 200, `{}`},
		{"device-rejected", true, service.AccountTypeOAuth, 401, `{}`},
		{"device-failed", true, service.AccountTypeOAuth, 503, `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var modelsCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				modelsCalls.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/backend-api/codex/models" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, `{"models":[{"slug":"gpt-5.5","visibility":"list"}]}`)
			}))
			t.Cleanup(server.Close)
			t.Cleanup(service.SetCodexModelsURLForTest(server.URL + "/backend-api/codex/models"))
			now, later := time.Now(), time.Now().Add(time.Hour)
			account := &service.Account{
				ID: 9101, Platform: service.PlatformOpenAI, Type: tc.typ, Status: service.StatusError,
				Concurrency: 1, RateLimitedAt: &now, RateLimitResetAt: &later,
				OverloadUntil: &later, TempUnschedulableUntil: &later,
				Credentials: map[string]any{"access_token": "offline-token"},
				Extra: map[string]any{
					"codex_fingerprint_mode":                     "device",
					"codex_experimental_fingerprint_convergence": tc.enabled,
					"model_rate_limits":                          map[string]any{"gpt-5.5": true},
				},
			}
			repo := &codexProbeHandlerRepo{account: account}
			up := &codexProbeOfflineUpstream{succeed: !tc.enabled}
			cfg := &config.Config{}
			tests := service.NewAccountTestService(repo, nil, nil, nil, nil, nil, up, cfg, nil)
			tests.SetOpenAIGatewayService(&service.OpenAIGatewayService{})
			handler := &AccountHandler{
				accountTestService: tests,
				rateLimitService:   service.NewRateLimitService(repo, nil, cfg, nil, nil),
			}
			router := gin.New()
			router.POST("/api/v1/admin/accounts/:id/test", handler.Test)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/9101/test", strings.NewReader(tc.testBody))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(rec, req)
			if tc.enabled {
				require.Equal(t, int32(1), modelsCalls.Load())
				require.Zero(t, up.calls.Load())
				require.Zero(t, repo.clearRuntimeCalls)
				require.Equal(t, &later, account.RateLimitResetAt)
				require.Equal(t, &later, account.OverloadUntil)
				require.Equal(t, &later, account.TempUnschedulableUntil)
				require.Contains(t, account.Extra, "model_rate_limits")
			} else {
				require.Zero(t, modelsCalls.Load())
				require.Equal(t, int32(1), up.calls.Load())
				require.Equal(t, 3, repo.clearRuntimeCalls)
			}
			if tc.status == 200 {
				require.Contains(t, rec.Body.String(), `"success":true`)
				require.Equal(t, 1, repo.clearErrorCalls)
				require.Empty(t, repo.setErrors)
			} else {
				require.NotContains(t, rec.Body.String(), `"success":true`)
				require.Zero(t, repo.clearErrorCalls)
				if tc.status == 401 {
					require.Len(t, repo.setErrors, 1)
					require.Contains(t, repo.setErrors[0], "Authentication failed (401)")
				}
			}
		})
	}
}

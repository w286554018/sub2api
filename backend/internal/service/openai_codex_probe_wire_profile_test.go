//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type codexModelsProbeServer struct {
	mu       sync.Mutex
	requests []*http.Request
	bodies   [][]byte
	status   int
	headers  http.Header
	body     string
}

func newCodexModelsProbeServer(t *testing.T) *codexModelsProbeServer {
	t.Helper()
	s := &codexModelsProbeServer{
		status: http.StatusOK,
		body:   `{"models":[{"slug":"gpt-5.5","visibility":"list"},{"slug":"gpt-5.6","visibility":"list"}]}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		defer s.mu.Unlock()
		s.requests = append(s.requests, r.Clone(context.Background()))
		s.bodies = append(s.bodies, body)
		for key, values := range s.headers {
			w.Header()[key] = append([]string(nil), values...)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `"probe-etag"`)
		w.WriteHeader(s.status)
		_, _ = io.WriteString(w, s.body)
	}))
	t.Cleanup(server.Close)
	original := chatgptCodexModelsURL
	chatgptCodexModelsURL = server.URL + "/backend-api/codex/models"
	t.Cleanup(func() { chatgptCodexModelsURL = original })
	return s
}

func (s *codexModelsProbeServer) respond(status int, body string, headers http.Header) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status, s.body, s.headers = status, body, headers
}

func (s *codexModelsProbeServer) captured() ([]*http.Request, [][]byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*http.Request(nil), s.requests...), append([][]byte(nil), s.bodies...)
}

func codexProbeTestAccount(enabled bool) *Account {
	return &Account{
		ID: 9101, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1,
		Credentials: map[string]any{"access_token": "offline-token", "chatgpt_account_id": "offline-account"},
		Extra: map[string]any{
			codexFingerprintModeExtraKey: "device", codexFingerprintConvergenceExtraKey: enabled,
			codexFingerprintSeedExtraKey: "11111111-1111-4111-8111-111111111111",
		},
	}
}

func newCodexProbeAdminContext() (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/9101/test", nil)
	c.Request.Header.Set("session-id", "admin-session")
	c.Request.Header.Set(openAIWSTurnMetadataHeader, `{"session_id":"admin-session","sandbox":"admin"}`)
	return c, rec
}

func assertFreshCodexModelsRequest(t *testing.T, req *http.Request, body []byte) {
	t.Helper()
	require.Equal(t, http.MethodGet, req.Method)
	require.Equal(t, "/backend-api/codex/models", req.URL.Path)
	require.Equal(t, CodexCanonicalClientVersion(), req.URL.Query().Get("client_version"))
	require.Empty(t, body)
	require.Equal(t, "Bearer offline-token", req.Header.Get("Authorization"))
	require.Equal(t, "offline-account", req.Header.Get("ChatGPT-Account-ID"))
	require.Equal(t, "*/*", req.Header.Get("Accept"))
	require.NotEmpty(t, req.Header.Get("Version"))
	require.NotEmpty(t, req.Header.Get("Originator"))
	require.NotEmpty(t, req.Header.Get("User-Agent"))
	names := make([]string, 0, len(req.Header))
	for key := range req.Header {
		key = strings.ToLower(key)
		if key != "accept-encoding" && key != "content-length" && key != "connection" {
			names = append(names, key)
		}
	}
	require.ElementsMatch(t, []string{"authorization", "chatgpt-account-id", "accept", "version", "originator", "user-agent"}, names)
}

func TestCodexDeviceWireProfileAccountProbes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{"device", "off", "session", "full"} {
		for _, enabled := range []bool{false, true} {
			for _, probe := range []string{"normal", "compact", "image"} {
				t.Run(fmt.Sprintf("%s/%t/%s", mode, enabled, probe), func(t *testing.T) {
					account := codexProbeTestAccount(enabled)
					account.Extra[codexFingerprintModeExtraKey] = mode
					models := newCodexModelsProbeServer(t)
					up := &httpUpstreamRecorder{err: errors.New("offline-probe-captured")}
					gateway := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: up}
					svc := &AccountTestService{cfg: &config.Config{}, httpUpstream: up, openaiGatewayService: gateway}
					model, testMode := "gpt-5.5", AccountTestModeDefault
					if probe == "compact" {
						testMode = AccountTestModeCompact
					} else if probe == "image" {
						model = "gpt-image-2"
					}
					if enabled && mode == "device" {
						// A fresh picker cache must not make a connectivity test succeed offline.
						_, err := gateway.FetchCodexModelsManifest(context.Background(), account, "", "")
						require.NoError(t, err)
					}
					c, rec := newCodexProbeAdminContext()
					err := svc.testOpenAIAccountConnection(c, account, model, "offline", testMode)
					requests, bodies := models.captured()
					if enabled && mode == "device" {
						require.NoError(t, err)
						require.Empty(t, up.requests)
						require.Len(t, requests, 2, "test must bypass an already fresh catalog")
						assertFreshCodexModelsRequest(t, requests[1], bodies[1])
						require.Contains(t, rec.Body.String(), `"success":true`)
						require.NotContains(t, rec.Body.String(), "admin-session")
						require.True(t, c.GetBool("account_test_credentials_only"))
						c, _ = newCodexProbeAdminContext()
						require.NoError(t, svc.testOpenAIAccountConnection(c, account, model, "", testMode))
						requests, bodies = models.captured()
						require.Len(t, requests, 3)
						assertFreshCodexModelsRequest(t, requests[2], bodies[2])
					} else {
						require.ErrorContains(t, err, "offline-probe-captured")
						require.Empty(t, requests)
						require.Len(t, up.requests, 1)
						require.Equal(t, http.MethodPost, up.lastReq.Method)
						require.False(t, c.GetBool("account_test_credentials_only"))
						require.NotContains(t, string(up.lastBody), "admin-session")
					}
				})
			}
		}
	}
}

func TestCodexModelsSetupTokenOnlyUnderDeviceWireProfile(t *testing.T) {
	for _, mode := range []string{"device", "off", "session", "full"} {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", mode, enabled), func(t *testing.T) {
				models := newCodexModelsProbeServer(t)
				account := codexProbeTestAccount(enabled)
				account.Type = AccountTypeSetupToken
				account.Extra[codexFingerprintModeExtraKey] = mode
				svc := &OpenAIGatewayService{}
				_, err := svc.FetchCodexModelsManifest(context.Background(), account, "", "")
				requests, bodies := models.captured()
				if mode == "device" && enabled {
					require.NoError(t, err)
					require.Len(t, requests, 1)
					assertFreshCodexModelsRequest(t, requests[0], bodies[0])
				} else {
					require.ErrorContains(t, err, "OPENAI_CODEX_MODELS_ACCOUNT_TYPE_UNSUPPORTED")
					require.Empty(t, requests)
				}
			})
		}
	}
}

func TestCodexDeviceWireProfileModelsManifestAccept(t *testing.T) {
	for _, tc := range []struct {
		mode    string
		enabled bool
		accept  string
	}{{"device", true, "*/*"}, {"device", false, "application/json"}, {"session", true, "application/json"}, {"full", true, "application/json"}, {"off", true, "application/json"}} {
		t.Run(fmt.Sprintf("%s/%t", tc.mode, tc.enabled), func(t *testing.T) {
			models := newCodexModelsProbeServer(t)
			account := codexProbeTestAccount(tc.enabled)
			account.Extra[codexFingerprintModeExtraKey] = tc.mode
			_, err := (&OpenAIGatewayService{}).FetchCodexModelsManifest(context.Background(), account, "", "")
			require.NoError(t, err)
			requests, _ := models.captured()
			require.Len(t, requests, 1)
			require.Equal(t, tc.accept, requests[0].Header.Get("Accept"))
		})
	}
}

func TestCodexDeviceWireProfileFreshSessionProbeErrors(t *testing.T) {
	for _, typ := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
		for _, status := range []int{401, 429, 503} {
			t.Run(fmt.Sprintf("%s/%d", typ, status), func(t *testing.T) {
				models := newCodexModelsProbeServer(t)
				models.respond(status, `{"error":{"type":"usage_limit_reached"}}`, http.Header{
					"X-Codex-Primary-Used-Percent": {"100"}, "X-Codex-Primary-Reset-After-Seconds": {"3600"},
				})
				account := codexProbeTestAccount(true)
				account.Type = typ
				repo := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{account.ID: account}}}
				up := &httpUpstreamRecorder{err: errors.New("offline-probe-captured")}
				svc := &AccountTestService{accountRepo: repo, cfg: &config.Config{}, httpUpstream: up, openaiGatewayService: &OpenAIGatewayService{}}
				result, err := svc.RunTestBackground(context.Background(), account.ID, "gpt-5.5")
				require.NoError(t, err)
				require.Equal(t, "failed", result.Status)
				require.False(t, result.CredentialsOnly)
				require.Contains(t, result.ErrorMessage, "GET /models")
				require.Contains(t, result.ErrorMessage, fmt.Sprint(status))
				require.Empty(t, up.requests)
				requests, _ := models.captured()
				require.Len(t, requests, 1)
				if status == 401 {
					require.Equal(t, account.ID, repo.setErrorID)
					require.Contains(t, repo.setErrorMsg, "Authentication failed (401)")
				} else {
					require.Zero(t, repo.setErrorID)
				}
				if status == 429 {
					require.Equal(t, account.ID, repo.rateLimitedID)
					require.NotNil(t, repo.rateLimitedAt)
					require.WithinDuration(t, time.Now().Add(time.Hour), *repo.rateLimitedAt, 2*time.Minute)
				} else {
					require.Zero(t, repo.rateLimitedID, "only 429 may reconcile runtime windows")
				}
			})
		}
	}
}

func TestCodexDeviceWireProfileModelsClientOptions(t *testing.T) {
	for _, tc := range []struct {
		mode    string
		typ     string
		enabled bool
		wantH2  bool
	}{
		{"device", AccountTypeOAuth, true, true},
		{"device", AccountTypeSetupToken, true, true},
		{"device", AccountTypeOAuth, false, false},
		{"session", AccountTypeOAuth, true, false},
		{"full", AccountTypeOAuth, true, false},
		{"off", AccountTypeOAuth, true, false},
	} {
		t.Run(fmt.Sprintf("%s/%s/%t", tc.mode, tc.typ, tc.enabled), func(t *testing.T) {
			models := newCodexModelsProbeServer(t)
			account := codexProbeTestAccount(tc.enabled)
			account.Type = tc.typ
			account.Extra[codexFingerprintModeExtraKey] = tc.mode
			var mu sync.Mutex
			var seen []httpclient.Options
			original := openAIModelsHTTPClient
			openAIModelsHTTPClient = func(opts httpclient.Options) (*http.Client, error) {
				mu.Lock()
				seen = append(seen, opts)
				mu.Unlock()
				return original(opts)
			}
			t.Cleanup(func() { openAIModelsHTTPClient = original })
			svc := &OpenAIGatewayService{}
			_, err := svc.FetchCodexModelsManifest(context.Background(), account, "", "")
			require.NoError(t, err)
			_, err = svc.ProbeCodexModelsManifest(context.Background(), account)
			require.NoError(t, err)
			requests, _ := models.captured()
			require.Len(t, requests, 2)
			mu.Lock()
			defer mu.Unlock()
			require.Len(t, seen, 2, "both manifest and probe must pass their transport policy")
			for _, opts := range seen {
				require.Equal(t, tc.wantH2, opts.ForceHTTP2)
				require.Equal(t, codexModelsManifestRequestTimeout, opts.Timeout)
				require.Equal(t, 10*time.Second, opts.ResponseHeaderTimeout)
				require.False(t, opts.InsecureSkipVerify)
			}
		})
	}
}

type scheduledTestProbeAccountRepo struct {
	openAIAccountTestRepo
	clearRateLimitIDs []int64
}

func (r *scheduledTestProbeAccountRepo) ClearRateLimit(_ context.Context, id int64) error {
	r.clearRateLimitIDs = append(r.clearRateLimitIDs, id)
	return nil
}

type scheduledTestProbePlanRepo struct {
	ScheduledTestPlanRepository
	updatedAfterRun []int64
}

func (r *scheduledTestProbePlanRepo) UpdateAfterRun(_ context.Context, id int64, _, _ time.Time) error {
	r.updatedAfterRun = append(r.updatedAfterRun, id)
	return nil
}

type scheduledTestProbeResultRepo struct {
	ScheduledTestResultRepository
	results []*ScheduledTestResult
}

func (r *scheduledTestProbeResultRepo) Create(_ context.Context, result *ScheduledTestResult) (*ScheduledTestResult, error) {
	r.results = append(r.results, result)
	return result, nil
}

func (r *scheduledTestProbeResultRepo) PruneOldResults(context.Context, int64, int) error { return nil }

func TestScheduledTestRunnerCredentialsOnlyRecoveryKeepsRateLimitWindows(t *testing.T) {
	for _, tc := range []struct {
		name        string
		enabled     bool
		autoRecover bool
		status      int
		typ         string
	}{
		{"device", true, true, 200, AccountTypeOAuth},
		{"setup-token", true, true, 200, AccountTypeSetupToken},
		{"auto-recovery-disabled", true, false, 200, AccountTypeOAuth},
		{"failed", true, true, 503, AccountTypeOAuth},
		{"legacy-inference", false, true, 200, AccountTypeOAuth},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := codexProbeTestAccount(tc.enabled)
			account.Type, account.Status = tc.typ, StatusError
			later := time.Now().Add(time.Hour)
			account.RateLimitedAt, account.RateLimitResetAt = &later, &later
			account.OverloadUntil, account.TempUnschedulableUntil = &later, &later
			repo := &scheduledTestProbeAccountRepo{openAIAccountTestRepo: openAIAccountTestRepo{
				mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{account.ID: account}},
			}}
			models := newCodexModelsProbeServer(t)
			models.respond(tc.status, `{"models":[]}`, nil)
			up := &httpUpstreamRecorder{err: errors.New("offline: unexpected inference")}
			if !tc.enabled {
				up.err = nil
				up.resp = newJSONResponse(200, "data: {\"type\":\"response.completed\"}\n\n")
			}
			tests := &AccountTestService{accountRepo: repo, cfg: &config.Config{}, httpUpstream: up, openaiGatewayService: &OpenAIGatewayService{}}
			plans, results := &scheduledTestProbePlanRepo{}, &scheduledTestProbeResultRepo{}
			rateLimitSvc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			runner := NewScheduledTestRunnerService(plans, NewScheduledTestService(plans, results), tests, rateLimitSvc, &config.Config{})
			plan := &ScheduledTestPlan{ID: 31, AccountID: account.ID, ModelID: "gpt-5.5", CronExpression: "*/5 * * * *", MaxResults: 10, AutoRecover: tc.autoRecover}
			runner.runOnePlan(context.Background(), plan)
			require.Len(t, results.results, 1)
			require.Equal(t, tc.enabled && tc.status == 200, results.results[0].CredentialsOnly)
			require.Equal(t, []int64{plan.ID}, plans.updatedAfterRun)
			requests, _ := models.captured()
			if tc.enabled {
				require.Len(t, requests, 1)
				require.Empty(t, up.requests)
				require.Empty(t, repo.clearRateLimitIDs)
			} else {
				require.Empty(t, requests)
				require.Len(t, up.requests, 1)
				require.Equal(t, []int64{account.ID}, repo.clearRateLimitIDs)
			}
			if tc.status == 200 {
				require.Equal(t, "success", results.results[0].Status)
				if tc.autoRecover {
					require.Equal(t, account.ID, repo.clearedErrorID)
				} else {
					require.Zero(t, repo.clearedErrorID)
				}
			} else {
				require.Equal(t, "failed", results.results[0].Status)
				require.Zero(t, repo.clearedErrorID)
			}
		})
	}
}

func TestProbeCodexModelsManifestBypassesAndDoesNotPopulateCache(t *testing.T) {
	models := newCodexModelsProbeServer(t)
	account := codexProbeTestAccount(true)
	svc := &OpenAIGatewayService{}
	cached, err := svc.FetchCodexModelsManifest(context.Background(), account, "", "")
	require.NoError(t, err)
	models.respond(503, `{"error":"offline failure"}`, nil)
	_, err = svc.ProbeCodexModelsManifest(context.Background(), account)
	require.ErrorContains(t, err, "503")
	stillCached, err := svc.FetchCodexModelsManifest(context.Background(), account, "", "")
	require.NoError(t, err)
	require.Equal(t, cached.Body, stillCached.Body)
	models.respond(200, `{"models":[{"slug":"new-upstream-model"}]}`, nil)
	probed, err := svc.ProbeCodexModelsManifest(context.Background(), account)
	require.NoError(t, err)
	require.Contains(t, string(probed.Body), "new-upstream-model")
	stillCached, err = svc.FetchCodexModelsManifest(context.Background(), account, "", "")
	require.NoError(t, err)
	require.Equal(t, cached.Body, stillCached.Body, "probe success must not replace a picker catalog")
	notModified, err := svc.FetchCodexModelsManifest(context.Background(), account, "", cached.ETag)
	require.NoError(t, err)
	require.True(t, notModified.NotModified, "regular cached ETag behavior remains available")
	requests, _ := models.captured()
	require.Len(t, requests, 3)
	for _, request := range requests {
		require.Empty(t, request.Header.Get("If-None-Match"))
	}
	uncached := &OpenAIGatewayService{}
	_, err = uncached.ProbeCodexModelsManifest(context.Background(), account)
	require.NoError(t, err)
	_, err = uncached.FetchCodexModelsManifest(context.Background(), account, "", "")
	require.NoError(t, err)
	requests, _ = models.captured()
	require.Len(t, requests, 5, "probe must not populate an empty catalog cache")
}

func TestProbeCodexModelsManifest401LeavesStateToCaller(t *testing.T) {
	models := newCodexModelsProbeServer(t)
	models.respond(401, `{"detail":{"message":"invalid token"}}`, nil)
	for _, typ := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
		t.Run(typ, func(t *testing.T) {
			account := codexProbeTestAccount(true)
			account.Type = typ
			account.Credentials["refresh_token"] = "offline-refresh-token"
			repo := &codexModelsAccountStateRepo{}
			svc := newCodexModels401TestService(repo)
			_, err := svc.ProbeCodexModelsManifest(context.Background(), account)
			require.ErrorContains(t, err, "401")
			require.Zero(t, repo.setErrorCalls)
			require.Zero(t, repo.setTempUnschedCalls)
			require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
			_, err = svc.FetchCodexModelsManifest(context.Background(), account, "", "")
			require.ErrorContains(t, err, "401")
			require.Equal(t, 1, repo.setErrorCalls+repo.setTempUnschedCalls, "forwarding still owns its auth-state transition")
			require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
		})
	}
}

func TestProbeCodexModelsManifestAgentIdentityRecoversInvalidTaskOnce(t *testing.T) {
	for _, retrySucceeds := range []bool{true, false} {
		t.Run(fmt.Sprint(retrySucceeds), func(t *testing.T) {
			key, privateKey := newTestAgentIdentityKey(t)
			account := codexProbeTestAccount(true)
			account.Credentials = map[string]any{
				"auth_mode": OpenAIAuthModeAgentIdentity, "agent_runtime_id": key.runtimeID,
				"agent_private_key": privateKey, "task_id": "task-probe-old",
				"chatgpt_account_id": "offline-account",
			}
			repo := &stubQuotaAccountRepo{accounts: map[int64]*Account{account.ID: account}}
			var mu sync.Mutex
			registerCalls := 0
			var assertions []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				if strings.Contains(r.URL.Path, "/task/register") {
					registerCalls++
					_, _ = io.WriteString(w, `{"task_id":"task-probe-new"}`)
					return
				}
				assertions = append(assertions, r.Header.Get("Authorization"))
				if len(assertions) == 1 || !retrySucceeds {
					w.WriteHeader(401)
					_, _ = io.WriteString(w, `{"error":{"code":"invalid_task_id"}}`)
					return
				}
				_, _ = io.WriteString(w, `{"models":[]}`)
			}))
			t.Cleanup(server.Close)
			t.Cleanup(SetCodexModelsURLForTest(server.URL))
			originalAuth := openAIAgentIdentityAuthAPIBaseURL
			openAIAgentIdentityAuthAPIBaseURL = server.URL
			t.Cleanup(func() { openAIAgentIdentityAuthAPIBaseURL = originalAuth })
			svc := &OpenAIGatewayService{accountRepo: repo}
			manifest, err := svc.ProbeCodexModelsManifest(context.Background(), account)
			if retrySucceeds {
				require.NoError(t, err)
				require.JSONEq(t, `{"models":[]}`, string(manifest.Body))
			} else {
				require.ErrorContains(t, err, "401")
			}
			mu.Lock()
			defer mu.Unlock()
			require.Equal(t, 1, registerCalls, "a second invalid task must not start another recovery")
			require.Len(t, assertions, 2)
			require.Equal(t, "task-probe-old", decodeAgentAssertionTask(t, assertions[0]))
			require.Equal(t, "task-probe-new", decodeAgentAssertionTask(t, assertions[1]))
		})
	}
}

func TestCodexDeviceProbeUsesSelectedModeAndCredentialOwnerGate(t *testing.T) {
	for _, tc := range []struct {
		name          string
		selectedMode  string
		childEnabled  bool
		parentEnabled bool
		missingParent bool
		wantProbe     bool
	}{
		{"owner-enabled", "device", false, true, false, true},
		{"child-only-enabled", "device", true, false, false, false},
		{"selected-session", "session", true, true, false, false},
		{"missing-parent", "device", true, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			models := newCodexModelsProbeServer(t)
			parent := codexProbeTestAccount(tc.parentEnabled)
			parent.Extra[codexFingerprintModeExtraKey] = "off"
			child := codexProbeTestAccount(tc.childEnabled)
			child.ID, child.ParentAccountID = 9102, &parent.ID
			child.Credentials = map[string]any{"access_token": "wrong-child-token"}
			child.Extra[codexFingerprintModeExtraKey] = tc.selectedMode
			accounts := map[int64]*Account{child.ID: child}
			if !tc.missingParent {
				accounts[parent.ID] = parent
			}
			repo := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: accounts}}
			up := &httpUpstreamRecorder{err: errors.New("offline: legacy inference")}
			svc := &AccountTestService{
				accountRepo: repo, httpUpstream: up, cfg: &config.Config{},
				openaiGatewayService: &OpenAIGatewayService{accountRepo: repo},
			}
			c, _ := newCodexProbeAdminContext()
			err := svc.TestAccountConnection(c, child.ID, "gpt-5.5", "", "")
			requests, bodies := models.captured()
			require.Equal(t, tc.wantProbe, AccountTestCredentialsOnly(c))
			if tc.wantProbe {
				require.NoError(t, err)
				require.Len(t, requests, 1)
				assertFreshCodexModelsRequest(t, requests[0], bodies[0])
				require.Empty(t, up.requests)
			} else {
				require.Error(t, err)
				require.Empty(t, requests)
				if tc.missingParent {
					require.Empty(t, up.requests, "unresolved ownership must not fall through to inference")
				} else {
					require.Len(t, up.requests, 1)
					require.Equal(t, "Bearer offline-token", up.lastReq.Header.Get("Authorization"))
				}
			}
		})
	}
}

func TestAccountTestCredentialsOnlyReflectsProbeOutcome(t *testing.T) {
	require.False(t, AccountTestCredentialsOnly(nil))
	models := newCodexModelsProbeServer(t)
	account := codexProbeTestAccount(true)
	repo := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{account.ID: account}}}
	up := &httpUpstreamRecorder{err: errors.New("offline: unexpected inference")}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: up, openaiGatewayService: &OpenAIGatewayService{}}
	c, _ := newCodexProbeAdminContext()
	require.NoError(t, svc.TestAccountConnection(c, account.ID, "gpt-5.5", "", ""))
	require.True(t, AccountTestCredentialsOnly(c))
	for _, tc := range []struct {
		status int
		body   string
	}{{503, `{"error":"failed"}`}, {200, `{"data":[]}`}, {200, `not-json`}, {304, ""}} {
		models.respond(tc.status, tc.body, nil)
		require.Error(t, svc.TestAccountConnection(c, account.ID, "gpt-5.5", "", ""))
		require.False(t, AccountTestCredentialsOnly(c), "failed or empty probes must not reuse a previous success")
	}
	svc.SetOpenAIGatewayService(nil)
	require.ErrorContains(t, svc.TestAccountConnection(c, account.ID, "gpt-image-2", "", "compact"), "not configured")
	require.False(t, AccountTestCredentialsOnly(c))
	require.Empty(t, up.requests)
}

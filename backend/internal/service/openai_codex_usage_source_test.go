package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	httppool "github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/imroc/req/v3"
	"github.com/stretchr/testify/require"
)

type codexUsageSourceUpdate struct {
	accountID int64
	extra     map[string]any
}

type codexUsageSourceRepo struct {
	stubQuotaAccountRepo
	updates chan codexUsageSourceUpdate
}

func (r *codexUsageSourceRepo) UpdateExtra(_ context.Context, id int64, extra map[string]any) error {
	r.updates <- codexUsageSourceUpdate{accountID: id, extra: shallowCopyMap(extra)}
	return nil
}

func newQuotaRecordingFactory(handler http.Handler) func(string) (*req.Client, error) {
	return func(string) (*req.Client, error) {
		client := req.C()
		client.GetTransport().WrapRoundTripFunc(func(http.RoundTripper) req.HttpRoundTripFunc {
			return func(r *http.Request) (*http.Response, error) {
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, r)
				return recorder.Result(), nil
			}
		})
		return client, nil
	}
}

// Trap the retired inference transport as well as recording quota HTTP calls.
// A regression must fail locally, never attempt a request to a real upstream.
func trapCodexUsageInference(t *testing.T, account *Account) *atomic.Int32 {
	t.Helper()
	proxy := &Proxy{ID: account.ID, Protocol: "http", Host: "quota-inference.invalid", Port: 1234}
	account.ProxyID, account.Proxy = &proxy.ID, proxy
	client, err := httppool.GetClient(httppool.Options{
		ProxyURL: proxy.URL(), Timeout: 15 * time.Second, ResponseHeaderTimeout: 10 * time.Second,
	})
	require.NoError(t, err)
	previous := client.Transport
	calls := &atomic.Int32{}
	client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		t.Errorf("usage refresh attempted inference: %s %s", r.Method, r.URL.Path)
		return nil, errors.New("inference disabled by quota test")
	})
	t.Cleanup(func() { client.Transport = previous })
	return calls
}

func TestGetOpenAIUsage_OrdinaryOAuthUsesQuotaEndpoint(t *testing.T) {
	account := &Account{
		ID: 701, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"chatgpt_account_id": "quota-account", "access_token": "test-quota-token"},
		Extra: map[string]any{
			"codex_5h_used_percent": 1.0, "codex_7d_used_percent": 2.0,
			"codex_5h_reset_at":      time.Now().Add(time.Hour).Format(time.RFC3339),
			"codex_7d_reset_at":      time.Now().Add(24 * time.Hour).Format(time.RFC3339),
			"codex_usage_updated_at": time.Now().Add(-time.Hour).Format(time.RFC3339),
			"local_setting":          "preserved",
		},
	}
	require.False(t, account.IsOpenAIResponsesWebSocketV2Enabled())
	inferenceCalls := trapCodexUsageInference(t, account)
	repo := &codexUsageSourceRepo{
		stubQuotaAccountRepo: stubQuotaAccountRepo{accounts: map[int64]*Account{account.ID: account}},
		updates:              make(chan codexUsageSourceUpdate, 4),
	}
	tokens := &stubQuotaTokenCache{tokens: map[string]string{OpenAITokenCacheKey(account): "test-quota-token"}}
	var usageCalls, creditCalls atomic.Int32
	var failUsage atomic.Bool
	factory := newQuotaRecordingFactory(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("usage refresh must be read-only: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected write", http.StatusBadRequest)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-quota-token" ||
			r.Header.Get("ChatGPT-Account-Id") != "quota-account" {
			t.Error("usage refresh lost the ordinary account credentials")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/backend-api/wham/usage":
			usageCalls.Add(1)
			if failUsage.Load() {
				http.Error(w, "test quota failure", http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte(`{"rate_limit":{"primary_window":{"used_percent":42,"limit_window_seconds":18000,"reset_after_seconds":3600},"secondary_window":{"used_percent":10,"limit_window_seconds":604800,"reset_after_seconds":86400}},"additional_rate_limits":[{"metered_feature":"codex_bengalfox","rate_limit":{"primary_window":{"used_percent":91,"limit_window_seconds":18000,"reset_after_seconds":3600}}}]}`))
		case "/backend-api/wham/rate-limit-reset-credits":
			creditCalls.Add(1)
			_, _ = w.Write([]byte(`{"available_count":0,"credits":[]}`))
		default:
			t.Errorf("unexpected usage refresh endpoint: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	svc := &AccountUsageService{
		accountRepo: repo, cache: NewUsageCache(),
		openAIQuotaService: NewOpenAIQuotaService(repo, nil, NewOpenAITokenProvider(repo, tokens, nil),
			factory),
	}
	waitForPersistence := func() {
		t.Helper()
		select {
		case update := <-repo.updates:
			require.Equal(t, account.ID, update.accountID)
			require.Equal(t, 42.0, update.extra["codex_5h_used_percent"])
			require.Equal(t, 10.0, update.extra["codex_7d_used_percent"])
			require.NotEmpty(t, update.extra["codex_usage_updated_at"])
			require.Equal(t, 300, update.extra["codex_5h_window_minutes"])
			require.Equal(t, 10080, update.extra["codex_7d_window_minutes"])
			require.WithinDuration(t, time.Now().Add(time.Hour), mustParseQuotaTime(t, update.extra["codex_5h_reset_at"]), 5*time.Second)
		case <-time.After(2 * time.Second):
			t.Fatal("ordinary quota windows were not persisted")
		}
	}
	usage, err := svc.GetUsage(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, int32(1), usageCalls.Load(), "stale non-WS OAuth must query quota")
	require.Equal(t, 42.0, usage.FiveHour.Utilization, "ordinary usage must not use Spark windows")
	require.Equal(t, 10.0, usage.SevenDay.Utilization)
	waitForPersistence()
	require.Equal(t, "preserved", account.Extra["local_setting"])
	require.Nil(t, account.RateLimitResetAt)

	_, err = svc.GetUsage(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, int32(1), usageCalls.Load(), "fresh snapshot must not requery")
	_, err = svc.GetUsage(context.Background(), account.ID, true)
	require.NoError(t, err)
	require.Equal(t, int32(2), usageCalls.Load(), "force must bypass the deadline")
	waitForPersistence()

	failUsage.Store(true)
	before := shallowCopyMap(account.Extra)
	usage, err = svc.GetUsage(context.Background(), account.ID, true)
	require.NoError(t, err)
	require.Equal(t, int32(3), usageCalls.Load())
	require.Equal(t, int32(2), creditCalls.Load())
	require.Equal(t, before, account.Extra, "quota errors must retain the snapshot")
	require.Equal(t, 42.0, usage.FiveHour.Utilization)
	require.Equal(t, int32(0), inferenceCalls.Load(), "quota errors must not fall back to inference")
	require.Empty(t, repo.updates)
}

func mustParseQuotaTime(t *testing.T, value any) time.Time {
	t.Helper()
	text, ok := value.(string)
	require.True(t, ok)
	parsed, err := time.Parse(time.RFC3339, text)
	require.NoError(t, err)
	return parsed
}

func TestGetOpenAIUsage_QuotaFailuresKeepSnapshotAndLocalStats(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			account := &Account{
				ID: 702, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
				Credentials: map[string]any{"chatgpt_account_id": "quota-account", "access_token": "test-quota-token"},
				Extra: map[string]any{
					"codex_5h_used_percent": 25.0, "codex_7d_used_percent": 50.0,
					"codex_usage_updated_at": time.Now().Add(-time.Hour).Format(time.RFC3339),
				},
			}
			inferenceCalls := trapCodexUsageInference(t, account)
			repo := &codexUsageSourceRepo{
				stubQuotaAccountRepo: stubQuotaAccountRepo{accounts: map[int64]*Account{account.ID: account}},
				updates:              make(chan codexUsageSourceUpdate, 1),
			}
			tokens := &stubQuotaTokenCache{tokens: map[string]string{OpenAITokenCacheKey(account): "test-quota-token"}}
			var calls atomic.Int32
			factory := newQuotaRecordingFactory(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/backend-api/wham/usage" {
					t.Errorf("unexpected request on quota failure: %s %s", r.Method, r.URL.Path)
				}
				http.Error(w, "test quota failure", code)
			}))
			svc := &AccountUsageService{
				accountRepo: repo, cache: NewUsageCache(),
				usageLogRepo: &usageLogWindowBatchRepoStub{singleResult: map[int64]*usagestats.AccountStats{
					account.ID: {Requests: 3, Tokens: 123, KiroCredits: 0.17},
				}},
				openAIQuotaService: NewOpenAIQuotaService(repo, nil, NewOpenAITokenProvider(repo, tokens, nil),
					factory),
			}
			before := shallowCopyMap(account.Extra)
			usage, err := svc.GetUsage(context.Background(), account.ID, true)
			require.NoError(t, err)
			require.Equal(t, int32(1), calls.Load())
			require.Equal(t, int32(0), inferenceCalls.Load())
			require.Equal(t, before, account.Extra)
			require.Equal(t, 25.0, usage.FiveHour.Utilization)
			require.Equal(t, 50.0, usage.SevenDay.Utilization)
			require.Equal(t, int64(3), usage.FiveHour.WindowStats.Requests)
			require.Equal(t, int64(123), usage.SevenDay.WindowStats.Tokens)
			require.Empty(t, repo.updates)
			_, err = svc.GetUsage(context.Background(), account.ID)
			require.NoError(t, err)
			require.Equal(t, int32(1), calls.Load(), "failed refreshes also obey the deadline")
		})
	}
}

func TestShouldProbeOpenAICodexSnapshotHonoursStoredDeadline(t *testing.T) {
	svc := &AccountUsageService{cache: NewUsageCache()}
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	deadline := now.Add(20 * time.Minute)
	svc.cache.openAIProbeCache.Store(int64(1), deadline)
	require.False(t, svc.shouldProbeOpenAICodexSnapshot(1, deadline.Add(-time.Nanosecond)))
	require.True(t, svc.shouldProbeOpenAICodexSnapshot(1, deadline), "deadline is inclusive")
	require.True(t, svc.shouldProbeOpenAICodexSnapshot(1, now, true), "force bypasses the deadline")
	next, ok := svc.cache.openAIProbeCache.Load(int64(1))
	require.True(t, ok)
	require.WithinRange(t, next.(time.Time), now.Add(10*time.Minute), now.Add(30*time.Minute))
	require.True(t, svc.shouldProbeOpenAICodexSnapshot(2, now), "accounts have independent deadlines")
}

func TestNextOpenAIProbeAllowedAtJitters(t *testing.T) {
	svc := &AccountUsageService{cache: NewUsageCache()}
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	seen := map[time.Time]bool{}
	for id := int64(1); id <= 200; id++ {
		require.True(t, svc.shouldProbeOpenAICodexSnapshot(id, now))
		next, ok := svc.cache.openAIProbeCache.Load(id)
		require.True(t, ok)
		deadline, ok := next.(time.Time)
		require.True(t, ok)
		require.WithinRange(t, deadline, now.Add(10*time.Minute), now.Add(30*time.Minute))
		seen[deadline] = true
	}
	require.Greater(t, len(seen), 1, "refresh deadlines must include jitter")
}

func TestBuildCodexPrimaryWindowExtraUpdates(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	usage := &OpenAIQuotaUsage{
		RateLimit: &OpenAIRateLimit{
			PrimaryWindow:   &OpenAIRateLimitWindow{UsedPercent: 34, LimitWindowSeconds: 604800, ResetAfterSeconds: 86400},
			SecondaryWindow: &OpenAIRateLimitWindow{UsedPercent: 12, LimitWindowSeconds: 18000, ResetAfterSeconds: 900},
		},
		AdditionalRateLimits: []OpenAIAdditionalRateLimit{{
			MeteredFeature: "codex_bengalfox",
			RateLimit: &OpenAIRateLimit{
				PrimaryWindow: &OpenAIRateLimitWindow{UsedPercent: 91, LimitWindowSeconds: 18000, ResetAfterSeconds: 1200},
			},
		}},
	}
	require.Equal(t, map[string]any{
		"codex_5h_used_percent": 12.0, "codex_5h_reset_after_seconds": 900,
		"codex_5h_window_minutes": 300, "codex_5h_reset_at": "2026-09-12T12:15:00Z",
		"codex_7d_used_percent": 34.0, "codex_7d_reset_after_seconds": 86400,
		"codex_7d_window_minutes": 10080, "codex_7d_reset_at": "2026-09-13T12:00:00Z",
		"codex_usage_updated_at": "2026-09-12T12:00:00Z",
	}, buildCodexPrimaryWindowExtraUpdates(usage, now))
	require.Equal(t, 91.0, buildCodexSparkWindowExtraUpdates(usage, now)["codex_5h_used_percent"])
	require.Nil(t, buildCodexPrimaryWindowExtraUpdates(nil, now))
	require.Nil(t, buildCodexPrimaryWindowExtraUpdates(&OpenAIQuotaUsage{}, now))
	require.Nil(t, buildCodexPrimaryWindowExtraUpdates(&OpenAIQuotaUsage{RateLimit: &OpenAIRateLimit{}}, now))
	usage.RateLimit = nil
	require.Nil(t, buildCodexPrimaryWindowExtraUpdates(usage, now), "Spark cannot substitute for missing primary quota")
}

func TestGetOpenAIUsage_MissingQuotaDataNeverInfers(t *testing.T) {
	for _, mode := range []string{"unconfigured", "client-error", "transport-error", "malformed-json", "empty-windows"} {
		t.Run(mode, func(t *testing.T) {
			account := &Account{
				ID: 703, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
				Credentials: map[string]any{"chatgpt_account_id": "quota-account", "access_token": "test-token"},
				Extra:       map[string]any{"codex_5h_used_percent": 12.0, "codex_7d_used_percent": 34.0},
			}
			inferenceCalls := trapCodexUsageInference(t, account)
			repo := &codexUsageSourceRepo{
				stubQuotaAccountRepo: stubQuotaAccountRepo{accounts: map[int64]*Account{account.ID: account}},
				updates:              make(chan codexUsageSourceUpdate, 1),
			}
			tokens := &stubQuotaTokenCache{tokens: map[string]string{OpenAITokenCacheKey(account): "test-token"}}
			factory := newQuotaRecordingFactory(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				w.Header().Set("Content-Type", "application/json")
				if mode == "malformed-json" {
					_, _ = w.Write([]byte(`{"rate_limit":`))
					return
				}
				_, _ = w.Write([]byte(`{"rate_limit":{},"additional_rate_limits":[{"metered_feature":"codex_bengalfox","rate_limit":{"primary_window":{"used_percent":91,"limit_window_seconds":18000}}}]}`))
			}))
			if mode == "client-error" {
				factory = func(string) (*req.Client, error) { return nil, errors.New("test client error") }
			}
			if mode == "transport-error" {
				factory = func(string) (*req.Client, error) {
					client := req.C()
					client.GetTransport().WrapRoundTripFunc(func(http.RoundTripper) req.HttpRoundTripFunc {
						return func(*http.Request) (*http.Response, error) {
							return nil, errors.New("test transport error")
						}
					})
					return client, nil
				}
			}
			svc := &AccountUsageService{accountRepo: repo, cache: NewUsageCache()}
			if mode != "unconfigured" {
				svc.openAIQuotaService = NewOpenAIQuotaService(repo, nil, NewOpenAITokenProvider(repo, tokens, nil), factory)
			}
			before := shallowCopyMap(account.Extra)
			usage, err := svc.GetUsage(context.Background(), account.ID, true)
			require.NoError(t, err)
			require.Equal(t, int32(0), inferenceCalls.Load())
			require.Equal(t, before, account.Extra)
			require.Equal(t, 12.0, usage.FiveHour.Utilization)
			require.Equal(t, 34.0, usage.SevenDay.Utilization)
			require.Empty(t, repo.updates)
		})
	}
}

func TestGetOpenAIUsage_SparkShadowSelectsSparkQuota(t *testing.T) {
	parent := &Account{
		ID: 704, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"chatgpt_account_id": "parent-quota-account"},
	}
	shadow := &Account{
		ID: 705, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		ParentAccountID: &parent.ID, QuotaDimension: QuotaDimensionSpark,
	}
	repo := &codexUsageSourceRepo{
		stubQuotaAccountRepo: stubQuotaAccountRepo{accounts: map[int64]*Account{parent.ID: parent, shadow.ID: shadow}},
		updates:              make(chan codexUsageSourceUpdate, 1),
	}
	tokens := &stubQuotaTokenCache{tokens: map[string]string{OpenAITokenCacheKey(parent): "parent-test-token"}}
	factory := newQuotaRecordingFactory(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "Bearer parent-test-token", r.Header.Get("Authorization"))
		require.Equal(t, "parent-quota-account", r.Header.Get("ChatGPT-Account-Id"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/backend-api/wham/rate-limit-reset-credits" {
			_, _ = w.Write([]byte(`{"available_count":0,"credits":[]}`))
			return
		}
		require.Equal(t, "/backend-api/wham/usage", r.URL.Path)
		_, _ = w.Write([]byte(`{"rate_limit":{"primary_window":{"used_percent":12,"limit_window_seconds":18000,"reset_after_seconds":3600}},"additional_rate_limits":[{"metered_feature":"codex_bengalfox","rate_limit":{"primary_window":{"used_percent":91,"limit_window_seconds":18000,"reset_after_seconds":3600},"secondary_window":{"used_percent":73,"limit_window_seconds":604800,"reset_after_seconds":86400}}}]}`))
	}))
	svc := &AccountUsageService{
		accountRepo: repo, cache: NewUsageCache(),
		openAIQuotaService: NewOpenAIQuotaService(repo, nil, NewOpenAITokenProvider(repo, tokens, nil), factory),
	}
	usage, err := svc.GetUsage(context.Background(), shadow.ID, true)
	require.NoError(t, err)
	require.Equal(t, 91.0, usage.FiveHour.Utilization)
	require.Equal(t, 73.0, usage.SevenDay.Utilization)
	require.Nil(t, parent.Extra, "shadow refresh must not overwrite parent windows")
	select {
	case update := <-repo.updates:
		require.Equal(t, shadow.ID, update.accountID)
		require.Equal(t, 91.0, update.extra["codex_5h_used_percent"])
		require.Equal(t, 73.0, update.extra["codex_7d_used_percent"])
	case <-time.After(2 * time.Second):
		t.Fatal("Spark quota was not persisted to the shadow row")
	}
}

//go:build unit

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const (
	codexWireSession = "0198f0de-7b2e-7abc-8def-123456789abc"
	codexWireInstall = "7f582abd-05d2-4a59-b4e5-ec1b733b4edc"
	codexWireMarker  = "responses_websockets=2026-02-06"
	codexWireMeta    = `{"installation_id":"` + codexWireInstall + `","session_id":"` + codexWireSession +
		`","thread_id":"` + codexWireSession + `","turn_id":"0198f0df-0123-7abc-8def-123456789abc",` +
		`"window_id":"` + codexWireSession + `:3","window_number":3,"tool_namespaces_info":["shell"]}`
)

type codexWireCapture struct {
	accountID     int64
	path          string
	header        http.Header
	raw, body     []byte
	contentLength int64
}

type codexWireUpstream struct {
	service.HTTPUpstream
	mu       sync.Mutex
	captures []codexWireCapture
	status   map[int64]int
}

func (u *codexWireUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	body := raw
	if req.Header.Get("Content-Encoding") == "zstd" {
		decoder, err := zstd.NewReader(nil)
		if err != nil {
			return nil, err
		}
		body, err = decoder.DecodeAll(raw, nil)
		decoder.Close()
		if err != nil {
			return nil, err
		}
	}
	u.mu.Lock()
	u.captures = append(u.captures, codexWireCapture{
		accountID: accountID, path: req.URL.Path, header: req.Header.Clone(),
		raw: raw, body: body, contentLength: req.ContentLength,
	})
	status := u.status[accountID]
	u.mu.Unlock()
	if status != 0 {
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"error":{"message":"upstream unavailable"}}`))}, nil
	}
	if gjson.GetBytes(body, "stream").Bool() {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_wire\",\"model\":\"gpt-5.4\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"))}, nil
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{"id":"resp_wire","object":"response","model":"gpt-5.4","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`))}, nil
}

func (u *codexWireUpstream) taken() []codexWireCapture {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]codexWireCapture(nil), u.captures...)
}

func codexWireAccount(id int64, mode string, enabled, raw bool) service.Account {
	return service.Account{
		ID: id, Name: fmt.Sprintf("wire-%d", id), Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: int(id),
		Credentials: map[string]any{"access_token": "offline-token",
			"chatgpt_account_id": fmt.Sprintf("credential-%d", id),
			"expires_at":         time.Now().Add(time.Hour).Format(time.RFC3339)},
		Extra: map[string]any{"codex_fingerprint_mode": mode,
			"codex_fingerprint_seed":                     "951af12d-881d-4865-8f1b-3d952e328525",
			"codex_experimental_fingerprint_convergence": enabled, "openai_passthrough": raw},
	}
}

func newCodexWireEntry(t *testing.T, accounts ...service.Account) (*codexWireUpstream, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	repo := &grokCredentialHandlerRepo{accounts: accounts, missingOnGet: map[int64]bool{}}
	upstream := &codexWireUpstream{status: map[int64]int{}}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Gateway.MaxAccountSwitches = 3
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	gateway := service.NewOpenAIGatewayService(
		repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil,
		service.NewBillingService(cfg, nil), nil, billingCache, upstream,
		&service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil,
	)
	cache := &concurrencyCacheMock{
		acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
		acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
	}
	h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(cache), billingCache,
		&service.APIKeyService{}, nil, nil, nil, nil, nil, cfg)
	groupID := int64(9001)
	key := &service.APIKey{
		ID: 9002, GroupID: &groupID, User: &service.User{ID: 9003, Status: service.StatusActive},
		Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, AllowMessagesDispatch: true},
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), key)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: key.User.ID, Concurrency: 1})
		c.Next()
	})
	router.POST("/v1/responses", h.Responses)
	router.POST("/v1/responses/*subpath", h.Responses)
	router.POST("/v1/messages", h.Messages)
	return upstream, router
}

func codexWireBody(key string) string {
	meta, _ := json.Marshal(codexWireMeta)
	cache := ""
	if key != "" {
		cache = `,"prompt_cache_key":` + key
	}
	return `{"stream":true,"store":false,"model":"gpt-5.4","instructions":"wire",` +
		`"input":[{"type":"message","role":"user","content":"hi"}],` +
		`"client_metadata":{"session_id":"` + codexWireSession + `","thread_id":"` + codexWireSession +
		`","x-codex-installation-id":"` + codexWireInstall + `","x-codex-turn-metadata":` + string(meta) + `}` + cache + `}`
}

func codexWireSend(t *testing.T, router *gin.Engine, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	for key, value := range map[string]string{
		"Content-Type": "application/json", "User-Agent": "codex-tui/0.153.4",
		"originator": "codex-tui", "session-id": codexWireSession, "thread-id": codexWireSession,
		"x-client-request-id": codexWireSession, "x-codex-installation-id": codexWireInstall,
		"x-codex-turn-metadata": codexWireMeta, "openai-beta": codexWireMarker,
		"x-openai-memgen-request": "true", "x-responsesapi-include-timing-metrics": "true",
	} {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	return rec
}

func TestCodexWireEntryEndpointMatrix(t *testing.T) {
	for _, mode := range []string{"off", "device", "session", "full"} {
		for _, enabled := range []bool{false, true} {
			for _, raw := range []bool{false, true} {
				for _, compact := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/enabled=%v/raw=%v/compact=%v", mode, enabled, raw, compact), func(t *testing.T) {
						upstream, router := newCodexWireEntry(t, codexWireAccount(701, mode, enabled, raw))
						path := "/v1/responses"
						if compact {
							path += "/compact"
						}
						body := strings.TrimSuffix(codexWireBody(`"`+codexWireSession+`"`), "}") + `,"access_programs":{"cyber":"daybreak_blue"}}`
						codexWireSend(t, router, path, body)
						captures := upstream.taken()
						require.Len(t, captures, 1)
						got := captures[0]
						strict := mode == "device" && enabled
						require.Equal(t, "/backend-api/codex"+strings.TrimPrefix(path, "/v1"), got.path)
						if raw {
							require.Equal(t, codexWireMarker, got.header.Get("OpenAI-Beta"), "prove the passthrough branch")
						} else {
							require.NotEqual(t, codexWireMarker, got.header.Get("OpenAI-Beta"))
						}
						if strict && !compact {
							require.Equal(t, "zstd", got.header.Get("Content-Encoding"))
							require.Equal(t, int64(len(got.raw)), got.contentLength)
							require.Greater(t, len(got.raw), 6)
							require.Equal(t, []byte{0x28, 0xb5, 0x2f, 0xfd, 0, 0x58}, got.raw[:6])
						} else {
							require.Empty(t, got.header.Get("Content-Encoding"))
							require.Equal(t, got.raw, got.body)
						}
						if strict {
							require.True(t, bytes.HasPrefix(got.body, []byte(`{"model":`)))
							session := got.header.Get("session-id")
							require.NotEmpty(t, session)
							require.NotEqual(t, codexWireSession, session)
							require.Equal(t, session, gjson.GetBytes(got.body, "prompt_cache_key").String())
							require.JSONEq(t, `{"cyber":"daybreak_blue"}`, gjson.GetBytes(got.body, "access_programs").Raw)
						}
						if compact {
							for _, field := range []string{"client_metadata", "stream", "store"} {
								require.False(t, gjson.GetBytes(got.body, field).Exists(), field)
							}
							if strict {
								require.Empty(t, got.header.Get("x-client-request-id"))
								require.NotEmpty(t, got.header.Get("x-codex-installation-id"))
							} else {
								require.False(t, gjson.GetBytes(got.body, "prompt_cache_key").Exists())
								require.False(t, gjson.GetBytes(got.body, "access_programs").Exists())
							}
						} else if strict {
							require.Empty(t, got.header.Get("x-codex-installation-id"))
							require.NotEmpty(t, got.header.Get("x-client-request-id"))
							require.NotEmpty(t, gjson.GetBytes(got.body, "client_metadata.x-codex-installation-id").String())
							require.False(t, gjson.Get(got.header.Get("x-codex-turn-metadata"), "tool_namespaces_info").Exists())
							embedded := gjson.GetBytes(got.body, "client_metadata.x-codex-turn-metadata").String()
							require.True(t, gjson.Get(embedded, "tool_namespaces_info").Exists())
						}
						for _, header := range []string{"x-openai-memgen-request", "x-responsesapi-include-timing-metrics"} {
							require.Equal(t, "true", got.header.Get(header))
						}
					})
				}
			}
		}
	}
}

func TestCodexWireEntryCompactCacheKeyRules(t *testing.T) {
	for _, raw := range []bool{false, true} {
		for _, key := range []string{"", `""`, `"   "`, `123`, `"custom-key"`, `"agent:` + codexWireSession + `"`, `"` + codexWireSession + `"`} {
			t.Run(fmt.Sprintf("raw=%v/key=%s", raw, key), func(t *testing.T) {
				upstream, router := newCodexWireEntry(t, codexWireAccount(702, "device", true, raw))
				codexWireSend(t, router, "/v1/responses/compact", codexWireBody(key))
				captures := upstream.taken()
				require.Len(t, captures, 1)
				got := gjson.GetBytes(captures[0].body, "prompt_cache_key")
				require.Equal(t, key != "", got.Exists())
				if key == "" {
					return
				}
				value := gjson.Parse(key)
				if value.Type == gjson.String && strings.TrimSpace(value.Str) != "" {
					require.NotEqual(t, value.Str, got.String())
					if value.Str == codexWireSession {
						require.Equal(t, captures[0].header.Get("session-id"), got.String())
					}
				} else {
					require.Equal(t, key, got.Raw)
				}
				require.False(t, gjson.GetBytes(captures[0].body, "access_programs").Exists(), "must not synthesize access programs")
			})
		}
	}
}

func TestCodexWireEntryFailoverDoesNotLeakPreviousAccountIdentity(t *testing.T) {
	for _, raw := range []bool{false, true} {
		t.Run(fmt.Sprintf("raw=%v", raw), func(t *testing.T) {
			next := codexWireAccount(802, "off", false, raw)
			upstream, router := newCodexWireEntry(t, codexWireAccount(801, "device", true, raw), next)
			upstream.status[801] = 529 // Both OAuth paths classify overload as failover.
			body := codexWireBody(`"` + codexWireSession + `"`)
			codexWireSend(t, router, "/v1/responses", body)
			captures := upstream.taken()
			require.GreaterOrEqual(t, len(captures), 2)
			first, last := captures[0], captures[len(captures)-1]
			require.EqualValues(t, 801, first.accountID)
			require.EqualValues(t, 802, last.accountID)
			firstID := gjson.GetBytes(first.body, "client_metadata.x-codex-installation-id").String()
			lastID := gjson.GetBytes(last.body, "client_metadata.x-codex-installation-id").String()
			require.NotEmpty(t, firstID)
			require.NotEmpty(t, lastID)
			require.NotEqual(t, firstID, lastID)
			require.Equal(t, "zstd", first.header.Get("Content-Encoding"))
			require.Empty(t, last.header.Get("Content-Encoding"))

			// Credential scoping remains active with convergence off. A clean
			// attempt on the second account must produce the same identity.
			direct, directRouter := newCodexWireEntry(t, next)
			codexWireSend(t, directRouter, "/v1/responses", body)
			require.Len(t, direct.taken(), 1)
			expected := direct.taken()[0]
			for _, field := range []string{"prompt_cache_key", "client_metadata"} {
				require.Equal(t, gjson.GetBytes(expected.body, field).Raw, gjson.GetBytes(last.body, field).Raw, field)
			}
			for _, header := range []string{"x-codex-installation-id", "session-id", "session_id", "thread-id", "x-codex-turn-metadata"} {
				require.Equal(t, expected.header.Get(header), last.header.Get(header), header)
			}
			require.NotEmpty(t, last.header.Get("session_id"))
		})
	}
}

func TestCodexWireEntryNonOAuthAccountUntouched(t *testing.T) {
	account := codexWireAccount(804, "device", true, false)
	account.Type = service.AccountTypeAPIKey
	account.Credentials = map[string]any{"api_key": "offline-api-key"}
	upstream, router := newCodexWireEntry(t, account)
	codexWireSend(t, router, "/v1/responses", codexWireBody(`"`+codexWireSession+`"`))
	captures := upstream.taken()
	require.Len(t, captures, 1)
	require.Empty(t, captures[0].header.Get("session-id"))
	require.Empty(t, captures[0].header.Get("Content-Encoding"))
	require.Equal(t, codexWireSession, gjson.GetBytes(captures[0].body, "prompt_cache_key").String())
}

func TestCodexWireEntryMessagesBridgeMatchesResponses(t *testing.T) {
	upstream, router := newCodexWireEntry(t, codexWireAccount(809, "device", true, false))
	codexWireSend(t, router, "/v1/responses", codexWireBody(`"`+codexWireSession+`"`))
	codexWireSend(t, router, "/v1/messages",
		`{"model":"gpt-5.4","stream":true,"max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)
	captures := upstream.taken()
	require.Len(t, captures, 2)
	var previous string
	for _, got := range captures {
		installation := gjson.GetBytes(got.body, "client_metadata.x-codex-installation-id").String()
		require.NotEmpty(t, installation)
		if previous != "" {
			require.Equal(t, previous, installation)
		}
		previous = installation
		require.Empty(t, got.header.Get("OpenAI-Beta"))
		require.Empty(t, got.header.Get("x-codex-installation-id"))
		require.Empty(t, got.header.Get("session_id"))
		require.Empty(t, got.header.Get("conversation_id"))
		require.NotEmpty(t, got.header.Get("session-id"))
		require.Equal(t, "zstd", got.header.Get("Content-Encoding"))
	}
}

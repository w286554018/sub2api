package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const codexHTTPDeliveryCreated = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_delivery\",\"model\":\"gpt-5.4\",\"status\":\"in_progress\",\"output\":[]}}\n\n"
const codexHTTPDeliveryDelta = "data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"delivered\"}\n\n"
const codexHTTPDeliveryCompleted = "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_delivery\",\"model\":\"gpt-5.4\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"delivered\"}]}],\"usage\":{\"input_tokens\":3,\"output_tokens\":1}}}\n\n"
const codexHTTPDeliveryFailed = "data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_delivery\",\"status\":\"failed\",\"error\":{\"code\":\"server_error\",\"message\":\"upstream overloaded\"}}}\n\n"
const codexHTTPDeliveryJSON = `{"id":"resp_delivery","model":"gpt-5.4","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"delivered"}]}],"usage":{"input_tokens":3,"output_tokens":1}}`

type codexHTTPDeliveryUpstream struct {
	client      *http.Client
	url         *url.URL
	mu          sync.Mutex
	state       string
	body        string
	contentType string
	gate        <-chan struct{}
	seen        []http.Header
}

func newCodexHTTPDeliveryService(t *testing.T, state, body string) (*OpenAIGatewayService, *codexHTTPDeliveryUpstream) {
	t.Helper()
	up := &codexHTTPDeliveryUpstream{state: state, body: body}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		up.mu.Lock()
		up.seen = append(up.seen, r.Header.Clone())
		state, body, gate, contentType := up.state, up.body, up.gate, up.contentType
		up.mu.Unlock()
		if contentType == "" {
			contentType = "text/event-stream"
		}
		w.Header().Set("Content-Type", contentType)
		if state != "" {
			w.Header().Set("x-codex-turn-state", state)
		}
		if gate != nil {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	up.client = server.Client()
	var err error
	up.url, err = url.Parse(server.URL)
	require.NoError(t, err)
	cfg := &config.Config{}
	cfg.Security.ResponseHeaders = config.ResponseHeaderConfig{
		Enabled: true, AdditionalAllowed: []string{"x-codex-turn-state"},
	}
	return &OpenAIGatewayService{
		cfg: cfg, httpUpstream: up, toolCorrector: NewCodexToolCorrector(),
		responseHeaderFilter: compileResponseHeaderFilter(cfg),
	}, up
}

func (u *codexHTTPDeliveryUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	local := req.Clone(req.Context())
	local.URL.Scheme, local.URL.Host = u.url.Scheme, u.url.Host
	local.Host = u.url.Host
	return u.client.Do(local)
}

func (u *codexHTTPDeliveryUpstream) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

func (u *codexHTTPDeliveryUpstream) respond(state, body string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.state, u.body = state, body
}

func (u *codexHTTPDeliveryUpstream) respondJSON(state, body string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.state, u.body, u.contentType = state, body, "application/json"
}

func (u *codexHTTPDeliveryUpstream) lastHeader() http.Header {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.seen[len(u.seen)-1].Clone()
}

func codexHTTPDeliveryAccount(id int64, owner string) *Account {
	return &Account{
		ID: id, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1,
		Credentials: map[string]any{"access_token": "offline-delivery-token", "chatgpt_account_id": owner},
		Extra: map[string]any{
			"openai_oauth_passthrough":                   true,
			"codex_fingerprint_mode":                     "device",
			"codex_experimental_fingerprint_convergence": true,
			"codex_fingerprint_seed":                     "11111111-1111-4111-8111-111111111111",
		},
	}
}

func codexHTTPDeliveryContext(route string, writer http.ResponseWriter) *gin.Context {
	c, _ := gin.CreateTestContext(writer)
	path := "/v1/responses"
	if strings.HasPrefix(route, "messages") {
		path = "/v1/messages"
	} else if route == "raw-buffered" || route == "raw-json" {
		path = "/v1/responses/compact"
	}
	c.Request = httptest.NewRequest(http.MethodPost, path, nil)
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.153.4")
	c.Request.Header.Set("originator", "codex_cli_rs")
	c.Request.Header.Set("session-id", "delivery-session")
	c.Set("api_key", &APIKey{ID: 977})
	return c
}

func codexHTTPDeliveryForward(s *OpenAIGatewayService, c *gin.Context, account *Account, route string) (*OpenAIForwardResult, error) {
	stream := strings.HasSuffix(route, "-stream")
	if strings.HasPrefix(route, "messages") {
		body := []byte(fmt.Sprintf(`{"model":"gpt-5.4","max_tokens":16,"stream":%t,"messages":[{"role":"user","content":"hi"}]}`, stream))
		return s.ForwardAsAnthropic(context.Background(), c, account, body, "delivery-cache", "")
	}
	if strings.HasPrefix(route, "map-") {
		selected := *account
		selected.Extra = maps.Clone(account.Extra)
		selected.Extra["openai_oauth_passthrough"] = false
		account = &selected
	}
	body := []byte(fmt.Sprintf(`{"model":"gpt-5.4","instructions":"Answer briefly.","input":"hi","stream":%t}`, stream))
	return s.Forward(context.Background(), c, account, body)
}

func TestCodexHTTPDeliveryAbandonedAttempt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, route := range []string{"raw-stream", "raw-buffered", "messages-stream", "messages-buffered", "map-stream", "map-buffered"} {
		t.Run(route, func(t *testing.T) {
			body := codexHTTPDeliveryFailed
			if route == "raw-stream" || route == "map-stream" {
				body = codexHTTPDeliveryCreated + body
			}
			svc, up := newCodexHTTPDeliveryService(t, "abandoned-state", body)
			first := codexHTTPDeliveryAccount(9771, "owner-a")
			other := codexHTTPDeliveryAccount(9772, "owner-b")
			rec := httptest.NewRecorder()
			c := codexHTTPDeliveryContext(route, rec)
			_, err := codexHTTPDeliveryForward(svc, c, first, route)
			var failover *UpstreamFailoverError
			require.ErrorAs(t, err, &failover)
			require.Empty(t, rec.Body.String(), "pre-output failure must remain replayable")
			_, known := svc.lookupOpenAICodexTurnStateOrigin("abandoned-state")
			assert.False(t, known, "unwritten upstream header must not acquire provenance")
			assert.Empty(t, svc.getOpenAICompatSessionTurnState(context.Background(), c, first, "delivery-cache"))
			assert.Empty(t, c.Writer.Header().Get("x-codex-turn-state"), "abandoned header must not remain pending")

			up.respond("", codexHTTPDeliveryCompleted)
			_, err = codexHTTPDeliveryForward(svc, c, other, route)
			require.NoError(t, err)
			assert.Empty(t, rec.Result().Header.Get("x-codex-turn-state"), "replacement must not transmit the abandoned header")
			echo := codexHTTPDeliveryContext(route, httptest.NewRecorder())
			echo.Request.Header.Set("x-codex-turn-state", "abandoned-state")
			_, err = codexHTTPDeliveryForward(svc, echo, other, route)
			require.NoError(t, err)
			assert.Equal(t, "abandoned-state", up.lastHeader().Get("x-codex-turn-state"), "unknown state must not be stripped by poisoned provenance")
		})
	}
}

type codexHTTPDeliveryFailWriter struct {
	*httptest.ResponseRecorder
	partial bool
}

func (w *codexHTTPDeliveryFailWriter) Write(p []byte) (int, error) {
	if w.partial && len(p) > 0 {
		n, _ := w.ResponseRecorder.Write(p[:1])
		return n, errors.New("downstream disconnected after partial write")
	}
	return 0, errors.New("downstream disconnected before write")
}

func (w *codexHTTPDeliveryFailWriter) WriteString(value string) (int, error) {
	return w.Write([]byte(value))
}

func TestCodexHTTPDeliveryWriteOutcome(t *testing.T) {
	for _, route := range []string{"raw-stream", "raw-buffered", "raw-json", "messages-stream", "messages-buffered", "map-stream", "map-buffered", "map-json"} {
		for _, partial := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/partial=%t", route, partial), func(t *testing.T) {
				svc, up := newCodexHTTPDeliveryService(t, "write-state", codexHTTPDeliveryCreated+codexHTTPDeliveryDelta+codexHTTPDeliveryCompleted)
				if route == "raw-json" || route == "map-json" {
					up.respondJSON("write-state", codexHTTPDeliveryJSON)
				}
				account := codexHTTPDeliveryAccount(9771, "owner-a")
				writer := &codexHTTPDeliveryFailWriter{ResponseRecorder: httptest.NewRecorder(), partial: partial}
				c := codexHTTPDeliveryContext(route, writer)
				_, _ = codexHTTPDeliveryForward(svc, c, account, route)
				require.Equal(t, partial, writer.Body.Len() > 0)
				_, known := svc.lookupOpenAICodexTurnStateOrigin("write-state")
				assert.Equal(t, partial, known, "only actual writes establish delivery, regardless of the handler's return error")
				if strings.HasPrefix(route, "messages") {
					want := ""
					if partial {
						want = "write-state"
					}
					assert.Equal(t, want, svc.getOpenAICompatSessionTurnState(context.Background(), c, account, "delivery-cache"))
				}
			})
		}
	}
}

func TestCodexHTTPDeliverySuccessAndLateFailure(t *testing.T) {
	for _, route := range []string{"raw-stream", "messages-stream", "messages-buffered", "raw-buffered", "map-stream", "map-buffered", "map-json"} {
		for _, lateFailure := range []bool{false, true} {
			if lateFailure && !strings.HasSuffix(route, "-stream") {
				continue
			}
			t.Run(fmt.Sprintf("%s/late-failure=%t", route, lateFailure), func(t *testing.T) {
				terminal := codexHTTPDeliveryCompleted
				if lateFailure {
					terminal = codexHTTPDeliveryFailed
				}
				svc, up := newCodexHTTPDeliveryService(t, "delivered-state", codexHTTPDeliveryCreated+codexHTTPDeliveryDelta+terminal)
				if route == "map-json" {
					up.respondJSON("delivered-state", codexHTTPDeliveryJSON)
				}
				first := codexHTTPDeliveryAccount(9771, "owner-a")
				other := codexHTTPDeliveryAccount(9772, "owner-b")
				rec := httptest.NewRecorder()
				c := codexHTTPDeliveryContext(route, rec)
				_, err := codexHTTPDeliveryForward(svc, c, first, route)
				if lateFailure {
					require.Error(t, err)
					var failover *UpstreamFailoverError
					require.False(t, errors.As(err, &failover), "delivered stream must not be replayed")
				} else {
					require.NoError(t, err)
				}
				require.Contains(t, rec.Body.String(), "delivered")
				require.Equal(t, "delivered-state", rec.Result().Header.Get("x-codex-turn-state"))
				_, known := svc.lookupOpenAICodexTurnStateOrigin("delivered-state")
				assert.True(t, known, "later failure must not discard actual delivery")
				if strings.HasPrefix(route, "messages") {
					assert.Equal(t, "delivered-state", svc.getOpenAICompatSessionTurnState(context.Background(), c, first, "delivery-cache"))
				}

				up.respond("", codexHTTPDeliveryCompleted)
				if route == "map-json" {
					up.respondJSON("", codexHTTPDeliveryJSON)
				}
				for _, account := range []*Account{other, first} {
					echo := codexHTTPDeliveryContext(route, httptest.NewRecorder())
					echo.Request.Header.Set("x-codex-turn-state", "delivered-state")
					_, err = codexHTTPDeliveryForward(svc, echo, account, route)
					require.NoError(t, err)
					want := "delivered-state"
					if account == other {
						want = ""
					}
					assert.Equal(t, want, up.lastHeader().Get("x-codex-turn-state"))
				}
			})
		}
	}
}

type codexHTTPDeliveryFlush struct {
	header http.Header
	body   string
}

type codexHTTPDeliveryFlushWriter struct {
	*httptest.ResponseRecorder
	first chan codexHTTPDeliveryFlush
	once  sync.Once
}

func (w *codexHTTPDeliveryFlushWriter) Flush() {
	w.ResponseRecorder.Flush()
	w.once.Do(func() {
		w.first <- codexHTTPDeliveryFlush{header: w.Result().Header.Clone(), body: w.Body.String()}
	})
}

func TestCodexHTTPDeliveryKeepaliveDoesNotMintWithheldHeader(t *testing.T) {
	for _, route := range []string{"raw-stream", "map-stream"} {
		for _, failed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/failed=%t", route, failed), func(t *testing.T) {
				body := codexHTTPDeliveryCreated + codexHTTPDeliveryDelta + codexHTTPDeliveryCompleted
				if failed {
					body = codexHTTPDeliveryCreated + codexHTTPDeliveryFailed
				}
				svc, up := newCodexHTTPDeliveryService(t, "withheld-state", body)
				svc.cfg.Gateway.StreamKeepaliveInterval = 1
				gate := make(chan struct{})
				up.mu.Lock()
				up.gate = gate
				up.mu.Unlock()
				account := codexHTTPDeliveryAccount(9771, "owner-a")
				writer := &codexHTTPDeliveryFlushWriter{
					ResponseRecorder: httptest.NewRecorder(), first: make(chan codexHTTPDeliveryFlush, 1),
				}
				c := codexHTTPDeliveryContext(route, writer)
				originalWriter := c.Writer
				done := make(chan error, 1)
				var release sync.Once
				t.Cleanup(func() {
					release.Do(func() { close(gate) })
					<-done
				})
				go func() {
					defer close(done)
					_, err := codexHTTPDeliveryForward(svc, c, account, route)
					done <- err
				}()
				select {
				case flushed := <-writer.first:
					heartbeat := ": keepalive"
					if route == "map-stream" {
						heartbeat = ":\n\n"
					}
					require.Contains(t, flushed.body, heartbeat)
					assert.Empty(t, flushed.header.Get("x-codex-turn-state"), "heartbeat must not leak an unselected attempt's header")
					_, known := svc.lookupOpenAICodexTurnStateOrigin("withheld-state")
					assert.False(t, known)
				case <-time.After(5 * time.Second):
					t.Fatal("downstream keepalive was not flushed")
				}
				release.Do(func() { close(gate) })
				err := <-done
				if failed {
					var failover *UpstreamFailoverError
					require.ErrorAs(t, err, &failover)
				} else {
					require.NoError(t, err)
					require.Contains(t, writer.Body.String(), "delivered")
				}
				assert.Same(t, originalWriter, c.Writer)
				_, known := svc.lookupOpenAICodexTurnStateOrigin("withheld-state")
				assert.False(t, known, "later body output cannot transmit a header after the heartbeat committed it")
			})
		}
	}
}

func TestCodexHTTPDeliveryRawKeepaliveAccountingSurvivesMultipleFailovers(t *testing.T) {
	svc, up := newCodexHTTPDeliveryService(t, "first-state", codexHTTPDeliveryCreated+codexHTTPDeliveryFailed)
	svc.cfg.Gateway.StreamKeepaliveInterval = 1
	account := codexHTTPDeliveryAccount(9771, "owner-a")
	writer := &codexHTTPDeliveryFlushWriter{
		ResponseRecorder: httptest.NewRecorder(), first: make(chan codexHTTPDeliveryFlush, 1),
	}
	c := codexHTTPDeliveryContext("raw-stream", writer)
	originalWriter := c.Writer

	runFailedAttempt := func(state string) {
		t.Helper()
		gate := make(chan struct{})
		up.mu.Lock()
		up.state = state
		up.body = codexHTTPDeliveryCreated + codexHTTPDeliveryFailed
		up.contentType = "text/event-stream"
		up.gate = gate
		up.mu.Unlock()
		writer.once = sync.Once{}
		writer.first = make(chan codexHTTPDeliveryFlush, 1)
		done := make(chan error, 1)
		go func() {
			_, err := codexHTTPDeliveryForward(svc, c, account, "raw-stream")
			done <- err
		}()
		select {
		case flushed := <-writer.first:
			require.Contains(t, flushed.body, ": keepalive")
		case <-time.After(5 * time.Second):
			close(gate)
			t.Fatal("downstream keepalive was not flushed")
		}
		close(gate)
		err := <-done
		var failover *UpstreamFailoverError
		require.ErrorAs(t, err, &failover, "keepalive-only attempts must remain failover eligible")
		assert.Equal(t, -1, OpenAICompactKeepaliveAdjustedWrittenSize(c), "all prior raw keepalive bytes must remain excluded")
		assert.Same(t, originalWriter, c.Writer)
	}

	runFailedAttempt("first-state")
	runFailedAttempt("second-state")

	up.mu.Lock()
	up.state = ""
	up.body = codexHTTPDeliveryCreated + codexHTTPDeliveryDelta + codexHTTPDeliveryCompleted
	up.gate = nil
	up.mu.Unlock()
	_, err := codexHTTPDeliveryForward(svc, c, account, "raw-stream")
	require.NoError(t, err)
	require.Contains(t, writer.Body.String(), "delivered")
	assert.Same(t, originalWriter, c.Writer)
}

func TestCodexHTTPDeliveryMessagesKeepaliveSelection(t *testing.T) {
	for _, mode := range []string{"device", "session"} {
		for _, failed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/failed=%t", mode, failed), func(t *testing.T) {
				body := codexHTTPDeliveryCreated + codexHTTPDeliveryDelta + codexHTTPDeliveryCompleted
				if failed {
					body = codexHTTPDeliveryFailed
				}
				svc, up := newCodexHTTPDeliveryService(t, "messages-withheld-state", body)
				svc.cfg.Gateway.StreamKeepaliveInterval = 1
				gate := make(chan struct{})
				up.mu.Lock()
				up.gate = gate
				up.mu.Unlock()
				account := codexHTTPDeliveryAccount(9771, "owner-a")
				account.Extra["codex_fingerprint_mode"] = mode
				writer := &codexHTTPDeliveryFlushWriter{
					ResponseRecorder: httptest.NewRecorder(), first: make(chan codexHTTPDeliveryFlush, 1),
				}
				c := codexHTTPDeliveryContext("messages-stream", writer)
				originalWriter := c.Writer
				done := make(chan error, 1)
				var release sync.Once
				t.Cleanup(func() {
					release.Do(func() { close(gate) })
					<-done
				})
				go func() {
					defer close(done)
					_, err := codexHTTPDeliveryForward(svc, c, account, "messages-stream")
					done <- err
				}()
				select {
				case flushed := <-writer.first:
					require.Contains(t, flushed.body, "event: ping")
					assert.NotContains(t, flushed.body, "message_start")
					assert.Empty(t, flushed.header.Get("x-codex-turn-state"), "pre-output Messages ping must not expose an unselected account's state")
					_, known := svc.lookupOpenAICodexTurnStateOrigin("messages-withheld-state")
					assert.False(t, known, "ping alone must not mint Messages provenance")
					assert.Empty(t, svc.getOpenAICompatSessionTurnState(context.Background(), c, account, "delivery-cache"), "ping alone must not bind the compatibility cache")
				case <-time.After(5 * time.Second):
					t.Fatal("Messages keepalive was not flushed")
				}
				release.Do(func() { close(gate) })
				err := <-done
				if failed {
					require.Error(t, err)
					assert.NotContains(t, writer.Body.String(), "message_start")
				} else {
					require.NoError(t, err)
					require.Contains(t, writer.Body.String(), "delivered")
				}
				assert.Same(t, originalWriter, c.Writer)
				_, known := svc.lookupOpenAICodexTurnStateOrigin("messages-withheld-state")
				assert.False(t, known, "selected Messages output cannot retroactively deliver the withheld header")
				wantCache := ""
				if !failed {
					wantCache = "messages-withheld-state"
				}
				assert.Equal(t, wantCache, svc.getOpenAICompatSessionTurnState(context.Background(), c, account, "delivery-cache"))
				assert.Empty(t, writer.Result().Header.Get("x-codex-turn-state"), "later output cannot retroactively change heartbeat headers")
				if failed {
					assert.Empty(t, c.Writer.Header().Get("x-codex-turn-state"))
				}

				up.respond("", codexHTTPDeliveryCompleted)
				other := codexHTTPDeliveryAccount(9772, "owner-b")
				other.Extra["codex_fingerprint_mode"] = mode
				echo := codexHTTPDeliveryContext("messages-stream", httptest.NewRecorder())
				echo.Request.Header.Set("x-codex-turn-state", "messages-withheld-state")
				_, err = codexHTTPDeliveryForward(svc, echo, other, "messages-stream")
				require.NoError(t, err)
				assert.Equal(t, "messages-withheld-state", up.lastHeader().Get("x-codex-turn-state"), "the undelivered header is still unknown")
				next := codexHTTPDeliveryContext("messages-stream", httptest.NewRecorder())
				_, err = codexHTTPDeliveryForward(svc, next, account, "messages-stream")
				require.NoError(t, err)
				wantNext := ""
				if !failed && mode == "session" {
					wantNext = "messages-withheld-state"
				}
				assert.Equal(t, wantNext, up.lastHeader().Get("x-codex-turn-state"), "legacy continuation is selected-output state, not client-visible provenance")
			})
		}
	}
}

func TestCodexHTTPDeliveryMessagesLocalErrorDoesNotMintState(t *testing.T) {
	const nonretryable = "data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_delivery\",\"status\":\"failed\",\"error\":{\"code\":\"invalid_request_error\",\"message\":\"invalid_request: unsupported argument\"}}}\n\n"
	for _, mode := range []string{"device", "session"} {
		for _, tc := range []struct {
			name  string
			route string
			body  string
		}{
			{name: "empty", route: "messages-buffered"},
			{name: "malformed", route: "messages-buffered", body: "data: {broken\n\n"},
			{name: "buffered-nonretryable", route: "messages-buffered", body: nonretryable},
			{name: "streaming-nonretryable", route: "messages-stream", body: nonretryable},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				svc, up := newCodexHTTPDeliveryService(t, "local-error-state", tc.body)
				account := codexHTTPDeliveryAccount(9771, "owner-a")
				account.Extra["codex_fingerprint_mode"] = mode
				rec := httptest.NewRecorder()
				c := codexHTTPDeliveryContext(tc.route, rec)
				_, err := codexHTTPDeliveryForward(svc, c, account, tc.route)
				require.Error(t, err)
				var failover *UpstreamFailoverError
				require.False(t, errors.As(err, &failover), "this fixture must exercise a locally written error, not abandoned failover")
				require.Equal(t, http.StatusBadGateway, rec.Code)
				require.Contains(t, rec.Body.String(), `"type":"error"`)
				assert.Empty(t, rec.Result().Header.Get("x-codex-turn-state"))
				assert.Empty(t, c.Writer.Header().Get("x-codex-turn-state"))
				_, known := svc.lookupOpenAICodexTurnStateOrigin("local-error-state")
				assert.False(t, known, "local error bytes are not delivery of the upstream state")
				assert.Empty(t, svc.getOpenAICompatSessionTurnState(context.Background(), c, account, "delivery-cache"), "local errors must not prime legacy continuation")

				up.respond("", codexHTTPDeliveryCompleted)
				other := codexHTTPDeliveryAccount(9772, "owner-b")
				echo := codexHTTPDeliveryContext(tc.route, httptest.NewRecorder())
				echo.Request.Header.Set("x-codex-turn-state", "local-error-state")
				_, err = codexHTTPDeliveryForward(svc, echo, other, tc.route)
				require.NoError(t, err)
				assert.Equal(t, "local-error-state", up.lastHeader().Get("x-codex-turn-state"))
				next := codexHTTPDeliveryContext(tc.route, httptest.NewRecorder())
				_, err = codexHTTPDeliveryForward(svc, next, account, tc.route)
				require.NoError(t, err)
				assert.Empty(t, up.lastHeader().Get("x-codex-turn-state"), "a later same-owner request must not inherit local-error state")
			})
		}
	}
}

func TestCodexHTTPDeliveryMessagesFilteredHeader(t *testing.T) {
	for _, mode := range []string{"device", "session"} {
		for _, route := range []string{"messages-stream", "messages-buffered"} {
			t.Run(mode+"/"+route, func(t *testing.T) {
				svc, up := newCodexHTTPDeliveryService(t, "filtered-state", codexHTTPDeliveryCreated+codexHTTPDeliveryDelta+codexHTTPDeliveryCompleted)
				svc.cfg.Security.ResponseHeaders.AdditionalAllowed = nil
				svc.responseHeaderFilter = compileResponseHeaderFilter(svc.cfg)
				account := codexHTTPDeliveryAccount(9771, "owner-a")
				account.Extra["codex_fingerprint_mode"] = mode
				rec := httptest.NewRecorder()
				c := codexHTTPDeliveryContext(route, rec)
				_, err := codexHTTPDeliveryForward(svc, c, account, route)
				require.NoError(t, err)
				require.Contains(t, rec.Body.String(), "delivered")
				require.Empty(t, rec.Result().Header.Get("x-codex-turn-state"))
				_, known := svc.lookupOpenAICodexTurnStateOrigin("filtered-state")
				assert.False(t, known, "a filtered-out upstream header has no client-visible provenance")
				assert.Equal(t, "filtered-state", svc.getOpenAICompatSessionTurnState(context.Background(), c, account, "delivery-cache"), "successful output preserves internal compatibility binding")

				up.respond("", codexHTTPDeliveryCompleted)
				next := codexHTTPDeliveryContext(route, httptest.NewRecorder())
				_, err = codexHTTPDeliveryForward(svc, next, account, route)
				require.NoError(t, err)
				wantNext := ""
				if mode == "session" {
					wantNext = "filtered-state"
				}
				assert.Equal(t, wantNext, up.lastHeader().Get("x-codex-turn-state"), "only legacy mode may consume the internal continuation cache")
				other := codexHTTPDeliveryAccount(9772, "owner-b")
				echo := codexHTTPDeliveryContext(route, httptest.NewRecorder())
				echo.Request.Header.Set("x-codex-turn-state", "filtered-state")
				_, err = codexHTTPDeliveryForward(svc, echo, other, route)
				require.NoError(t, err)
				assert.Equal(t, "filtered-state", up.lastHeader().Get("x-codex-turn-state"), "internal binding must not poison the client echo guard")
			})
		}
	}
}

func TestCodexHTTPDeliveryMapFailedTransformation(t *testing.T) {
	for _, route := range []string{"map-stream", "map-buffered", "map-json"} {
		t.Run(route, func(t *testing.T) {
			// Valid JSON, but the namespace restoration's map decoder cannot
			// represent this upstream extension as a float64.
			response := strings.TrimSuffix(codexHTTPDeliveryJSON, "}") + `,"extension":1e1000}`
			body := "data: {\"type\":\"response.completed\",\"response\":" + response + "}\n\n"
			svc, up := newCodexHTTPDeliveryService(t, "transform-state", body)
			if route == "map-json" {
				up.respondJSON("transform-state", response)
			}
			account := codexHTTPDeliveryAccount(9771, "owner-a")
			account.Extra["openai_oauth_passthrough"] = false
			account.Extra["openai_responses_flatten_namespaces"] = true
			rec := httptest.NewRecorder()
			c := codexHTTPDeliveryContext(route, rec)
			request := fmt.Sprintf(`{"model":"gpt-5.4","instructions":"Answer briefly.","input":"hi","stream":%t,"tools":[{"type":"namespace","name":"ns","tools":[{"type":"function","name":"f","parameters":{"type":"object","properties":{}}}]}]}`, route == "map-stream")
			_, err := svc.Forward(context.Background(), c, account, []byte(request))
			require.ErrorContains(t, err, "namespace")
			require.ErrorContains(t, err, "1e1000")
			require.NotEmpty(t, up.lastHeader(), "failure must come after actual upstream HTTP")
			assert.Empty(t, rec.Body.String())
			assert.Empty(t, c.Writer.Header().Get("x-codex-turn-state"))
			_, known := svc.lookupOpenAICodexTurnStateOrigin("transform-state")
			assert.False(t, known)
		})
	}
}

func TestCodexHTTPDeliveryMapLocalError(t *testing.T) {
	svc, _ := newCodexHTTPDeliveryService(t, "local-map-state", "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"invalid_request_error\",\"message\":\"invalid_request: unsupported argument\"}}}\n\n")
	svc.cfg.Security.ResponseHeaders.AdditionalAllowed = nil
	svc.responseHeaderFilter = compileResponseHeaderFilter(svc.cfg)
	account := codexHTTPDeliveryAccount(9771, "owner-a")
	rec := httptest.NewRecorder()
	c := codexHTTPDeliveryContext("map-buffered", rec)
	_, err := codexHTTPDeliveryForward(svc, c, account, "map-buffered")
	require.Error(t, err)
	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Contains(t, rec.Body.String(), `"upstream_error"`)
	require.Empty(t, rec.Result().Header.Get("x-codex-turn-state"))
	_, known := svc.lookupOpenAICodexTurnStateOrigin("local-map-state")
	assert.False(t, known, "local error output without a relayed header is not upstream state delivery")
}

func TestCodexHTTPDeliveryMapExplicitHeaderRelay(t *testing.T) {
	for _, route := range []string{"map-stream", "map-buffered", "map-json"} {
		t.Run(route, func(t *testing.T) {
			svc, up := newCodexHTTPDeliveryService(t, "map-relay-state", codexHTTPDeliveryCompleted)
			if route == "map-json" {
				up.respondJSON("map-relay-state", codexHTTPDeliveryJSON)
			}
			svc.cfg.Security.ResponseHeaders.AdditionalAllowed = nil
			svc.responseHeaderFilter = compileResponseHeaderFilter(svc.cfg)
			account := codexHTTPDeliveryAccount(9771, "owner-a")
			rec := httptest.NewRecorder()
			c := codexHTTPDeliveryContext(route, rec)
			_, err := codexHTTPDeliveryForward(svc, c, account, route)
			require.NoError(t, err)
			require.Equal(t, "map-relay-state", rec.Result().Header.Get("x-codex-turn-state"), "Responses explicitly relays Codex headers outside the generic filter")
			_, known := svc.lookupOpenAICodexTurnStateOrigin("map-relay-state")
			assert.True(t, known)
		})
	}
}

func TestCodexHTTPDeliveryExplicitCommitBoundary(t *testing.T) {
	for _, kind := range []string{"status-only", "header-now", "flush", "empty-write", "string-write", "failed-string", "failed-write-then-flush"} {
		t.Run(kind, func(t *testing.T) {
			var writer http.ResponseWriter = httptest.NewRecorder()
			if strings.HasPrefix(kind, "failed-") {
				writer = &codexHTTPDeliveryFailWriter{ResponseRecorder: httptest.NewRecorder()}
			}
			c := codexHTTPDeliveryContext("raw-stream", writer)
			originalWriter := c.Writer
			account := codexHTTPDeliveryAccount(9771, "owner-a")
			svc := &OpenAIGatewayService{}
			c.Writer.Header().Set("x-codex-turn-state", "explicit-state")
			restore := observeOpenAICodexHTTPDelivery(c, func(headers http.Header) {
				svc.noteStagedOpenAICodexTurnStateCommitted(c, account, headers)
			})
			switch kind {
			case "status-only":
				c.Writer.WriteHeader(http.StatusOK)
			case "header-now":
				c.Writer.WriteHeaderNow()
			case "flush":
				c.Writer.Flush()
			case "empty-write":
				_, err := c.Writer.Write(nil)
				require.NoError(t, err)
			case "string-write":
				_, err := c.Writer.WriteString("delivered")
				require.NoError(t, err)
			case "failed-string":
				_, err := c.Writer.WriteString("undelivered")
				require.Error(t, err)
			case "failed-write-then-flush":
				_, err := c.Writer.Write([]byte("undelivered"))
				require.Error(t, err)
				c.Writer.Flush()
			}
			_, known := svc.lookupOpenAICodexTurnStateOrigin("explicit-state")
			want := kind != "status-only" && !strings.HasPrefix(kind, "failed-")
			assert.Equal(t, want, known)
			restore()
			assert.Same(t, originalWriter, c.Writer)
		})
	}
}

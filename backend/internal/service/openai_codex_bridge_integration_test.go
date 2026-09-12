package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const codexBridgeIntegrationSession = "11111111-2222-4333-8444-555555555555"

func codexBridgeIntegrationAccount(id int64, credential string) *Account {
	return &Account{
		ID:          id,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "token-" + credential,
			"chatgpt_account_id": credential,
			"chatgpt_user_id":    "owner",
		},
		Extra: map[string]any{
			codexFingerprintModeExtraKey:        "device",
			codexFingerprintConvergenceExtraKey: true,
			codexFingerprintSeedExtraKey:        "11111111-1111-4111-8111-111111111111",
		},
	}
}

func codexBridgeIntegrationBody(t *testing.T, session string, stream, followup bool) []byte {
	t.Helper()
	messages := []map[string]any{{"role": "user", "content": "hello"}}
	if followup {
		messages = append(messages,
			map[string]any{"role": "assistant", "content": "ok"},
			map[string]any{"role": "user", "content": "continue"},
		)
	}
	body := map[string]any{
		"model": "claude-sonnet-4-5", "max_tokens": 16,
		"messages": messages, "stream": stream,
	}
	if session != "" {
		body["metadata"] = map[string]any{
			"user_id": "user_" + strings.Repeat("a", 64) + "_account__session_" + session,
		}
	}
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	return encoded
}

func codexBridgeIntegrationContext(body []byte, apiKeyID int64) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("api_key", &APIKey{ID: apiKeyID})
	return c, rec
}

func codexBridgeIntegrationService(turnStates ...string) (*OpenAIGatewayService, *httpUpstreamRecorder) {
	up := &httpUpstreamRecorder{}
	for i, state := range turnStates {
		resp := openAICompatSSECompletedResponse(fmt.Sprintf("resp_bridge_%d", i), "gpt-5.4")
		if state != "" {
			resp.Header.Set(openAICodexTurnStateHeader, state)
		}
		up.responses = append(up.responses, resp)
	}
	return &OpenAIGatewayService{
		httpUpstream: up,
		cfg: &config.Config{
			Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}},
		},
	}, up
}

func codexBridgeIntegrationForward(t *testing.T, svc *OpenAIGatewayService, account *Account, body []byte, apiKeyID int64, cacheKey, model string) *gin.Context {
	t.Helper()
	c, rec := codexBridgeIntegrationContext(body, apiKeyID)
	original := c.Request.Header.Clone()
	result, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, cacheKey, model)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, original, c.Request.Header, "temporary bridge headers must not survive forwarding")
	if gjson.GetBytes(body, "stream").Bool() {
		require.Contains(t, rec.Body.String(), "event: message_stop")
	} else {
		require.Equal(t, "message", gjson.GetBytes(rec.Body.Bytes(), "type").String())
		require.Equal(t, "ok", gjson.GetBytes(rec.Body.Bytes(), "content.0.text").String())
	}
	return c
}

func codexBridgeIntegrationWireBody(t *testing.T, up *httpUpstreamRecorder, index int, compressed bool) []byte {
	t.Helper()
	require.Greater(t, len(up.requests), index)
	require.Greater(t, len(up.bodies), index)
	req, wire := up.requests[index], up.bodies[index]
	require.Equal(t, http.MethodPost, req.Method)
	require.Equal(t, "/backend-api/codex/responses", req.URL.Path)
	require.Equal(t, int64(len(wire)), req.ContentLength)
	if !compressed {
		require.Empty(t, req.Header.Get("Content-Encoding"))
		require.True(t, json.Valid(wire), "uncompressed upstream body must be JSON")
		return wire
	}
	require.Equal(t, "zstd", req.Header.Get("Content-Encoding"))
	require.GreaterOrEqual(t, len(wire), 6)
	require.Equal(t, []byte{0x28, 0xb5, 0x2f, 0xfd, 0x00, 0x58}, wire[:6])
	decoder, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
	require.NoError(t, err)
	defer decoder.Close()
	body, err := decoder.DecodeAll(wire, nil)
	require.NoError(t, err)
	require.True(t, json.Valid(body), "decoded upstream body must be JSON")
	return body
}

type codexBridgeIntegrationIdentity struct {
	session, turn, contextWindow, installation string
}

func requireCodexBridgeIntegrationUUIDv7(t *testing.T, value string) {
	t.Helper()
	parsed, err := uuid.Parse(value)
	require.NoError(t, err)
	require.Equal(t, uuid.Version(7), parsed.Version())
	require.Equal(t, uuid.RFC4122, parsed.Variant())
	require.Equal(t, parsed.String(), value)
}

func requireCodexBridgeIntegrationIdentity(t *testing.T, up *httpUpstreamRecorder, index int) codexBridgeIntegrationIdentity {
	t.Helper()
	body := codexBridgeIntegrationWireBody(t, up, index, true)
	header := up.requests[index].Header
	session := header.Get("session-id")
	requireCodexBridgeIntegrationUUIDv7(t, session)
	require.Equal(t, session, header.Get("thread-id"))
	require.Equal(t, session, header.Get("x-client-request-id"))
	require.Equal(t, session, gjson.GetBytes(body, "prompt_cache_key").String())
	for _, name := range []string{"session_id", "conversation_id", "x-codex-installation-id", "OpenAI-Beta"} {
		require.Empty(t, header.Get(name), name)
	}
	for _, name := range []string{"originator", "version", "User-Agent", "Authorization", "ChatGPT-Account-Id"} {
		require.NotEmpty(t, header.Get(name), name)
	}
	require.Equal(t, "auto", gjson.GetBytes(body, "tool_choice").String())
	require.True(t, gjson.GetBytes(body, "stream").Bool())
	require.False(t, gjson.GetBytes(body, "previous_response_id").Exists())

	cm := gjson.GetBytes(body, "client_metadata")
	require.True(t, cm.IsObject())
	keys := func(value gjson.Result) []string {
		var names []string
		value.ForEach(func(key, _ gjson.Result) bool {
			names = append(names, key.String())
			return true
		})
		return names
	}
	require.ElementsMatch(t, []string{
		"session_id", "thread_id", "turn_id", "x-codex-installation-id",
		"x-codex-window-id", "x-codex-turn-metadata",
	}, keys(cm))
	require.Equal(t, session, cm.Get("session_id").String())
	require.Equal(t, session, cm.Get("thread_id").String())
	require.Equal(t, session+":0", header.Get("x-codex-window-id"))
	require.Equal(t, session+":0", cm.Get("x-codex-window-id").String())
	turn := cm.Get("turn_id").String()
	requireCodexBridgeIntegrationUUIDv7(t, turn)

	rawMetadata := header.Get(openAIWSTurnMetadataHeader)
	require.NotEmpty(t, rawMetadata)
	require.Equal(t, rawMetadata, cm.Get(openAIWSTurnMetadataHeader).String(), "header and embedded metadata must match byte-for-byte")
	tm := gjson.Parse(rawMetadata)
	require.Equal(t, []string{
		"installation_id", "session_id", "thread_id", "agent_name", "turn_id",
		"window_id", "window_number", "context_window_id", "request_kind", "turn_started_at_unix_ms",
	}, keys(tm))
	installation := cm.Get("x-codex-installation-id").String()
	require.NotEmpty(t, installation)
	require.Equal(t, installation, tm.Get("installation_id").String())
	require.Equal(t, session, tm.Get("session_id").String())
	require.Equal(t, session, tm.Get("thread_id").String())
	require.Equal(t, "/root", tm.Get("agent_name").String())
	require.Equal(t, turn, tm.Get("turn_id").String())
	require.Equal(t, session+":0", tm.Get("window_id").String())
	require.Equal(t, gjson.Number, tm.Get("window_number").Type)
	require.Zero(t, tm.Get("window_number").Int())
	contextWindow := tm.Get("context_window_id").String()
	requireCodexBridgeIntegrationUUIDv7(t, contextWindow)
	require.NotEqual(t, session, contextWindow)
	require.Equal(t, "turn", tm.Get("request_kind").String())
	require.WithinDuration(t, time.Now(), time.UnixMilli(tm.Get("turn_started_at_unix_ms").Int()), time.Minute)
	return codexBridgeIntegrationIdentity{session, turn, contextWindow, installation}
}

func TestForwardAsAnthropic_CodexBridgeIdentityAcrossRounds(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			account := codexBridgeIntegrationAccount(8101, "credential-a")
			svc, up := codexBridgeIntegrationService("turn-state-first", "turn-state-second", "")
			firstBody := codexBridgeIntegrationBody(t, codexBridgeIntegrationSession, stream, false)
			nextBody := codexBridgeIntegrationBody(t, codexBridgeIntegrationSession, stream, true)
			codexBridgeIntegrationForward(t, svc, account, firstBody, 71, "", "gpt-5.4")
			account.Credentials["access_token"] = "refreshed-token"
			codexBridgeIntegrationForward(t, svc, account, nextBody, 71, "", "gpt-5.4")
			otherBody := codexBridgeIntegrationBody(t, "99999999-2222-4333-8444-555555555555", stream, false)
			codexBridgeIntegrationForward(t, svc, account, otherBody, 71, "", "gpt-5.4")
			require.Len(t, up.requests, 3)
			first := requireCodexBridgeIntegrationIdentity(t, up, 0)
			second := requireCodexBridgeIntegrationIdentity(t, up, 1)
			other := requireCodexBridgeIntegrationIdentity(t, up, 2)
			require.Equal(t, first.session, second.session)
			require.Equal(t, first.contextWindow, second.contextWindow)
			require.Equal(t, first.installation, second.installation)
			require.NotEqual(t, first.turn, second.turn)
			require.NotEqual(t, first.session, other.session)
			require.NotEqual(t, first.contextWindow, other.contextWindow)
			for _, req := range up.requests {
				require.Empty(t, req.Header.Get(openAICodexTurnStateHeader), "device bridge must not replay the previous turn's blob")
			}
		})
	}
}

func TestForwardAsAnthropic_CodexBridgeIdentityIsolation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc, up := codexBridgeIntegrationService("", "", "", "", "", "")
	account := codexBridgeIntegrationAccount(8101, "credential-a")
	duplicate := codexBridgeIntegrationAccount(8102, "credential-a")
	other := codexBridgeIntegrationAccount(8101, "credential-b")
	other.Extra[codexFingerprintSeedExtraKey] = "22222222-2222-4222-8222-222222222222"
	body := codexBridgeIntegrationBody(t, codexBridgeIntegrationSession, false, false)
	for _, turn := range []struct {
		account *Account
		apiKey  int64
		cache   string
	}{
		{account, 71, "cache-a"},
		{duplicate, 71, "cache-a"},
		{account, 72, "cache-a"},
		{other, 71, "cache-a"},
		{account, 71, "cache-b"},
		{account, 71, "cache-a"},
	} {
		codexBridgeIntegrationForward(t, svc, turn.account, body, turn.apiKey, turn.cache, "gpt-5.4")
	}
	require.Len(t, up.requests, 6)
	first := requireCodexBridgeIntegrationIdentity(t, up, 0)
	for _, index := range []int{1, 5} {
		same := requireCodexBridgeIntegrationIdentity(t, up, index)
		require.Equal(t, first.session, same.session)
		require.Equal(t, first.contextWindow, same.contextWindow)
		require.Equal(t, first.installation, same.installation)
		require.NotEqual(t, first.turn, same.turn)
	}
	for _, index := range []int{2, 3, 4} {
		isolated := requireCodexBridgeIntegrationIdentity(t, up, index)
		require.NotEqual(t, first.session, isolated.session)
		require.NotEqual(t, first.contextWindow, isolated.contextWindow)
		require.NotEqual(t, first.turn, isolated.turn)
	}
	require.Equal(t, "Bearer token-credential-a", up.requests[0].Header.Get("Authorization"))
	require.Equal(t, "Bearer token-credential-b", up.requests[3].Header.Get("Authorization"))
}

func TestForwardAsAnthropic_CodexBridgeRestoresNilHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, transportError := range []bool{false, true} {
		t.Run(fmt.Sprintf("transport-error=%t", transportError), func(t *testing.T) {
			svc, up := codexBridgeIntegrationService("")
			if transportError {
				up.err = errors.New("connection reset by peer")
			}
			body := codexBridgeIntegrationBody(t, codexBridgeIntegrationSession, false, false)
			c, _ := codexBridgeIntegrationContext(body, 71)
			c.Request.Header = nil
			require.NotPanics(t, func() {
				result, err := svc.ForwardAsAnthropic(context.Background(), c, codexBridgeIntegrationAccount(8101, "credential-a"), body, "", "gpt-5.4")
				if transportError {
					var failover *UpstreamFailoverError
					require.ErrorAs(t, err, &failover)
					require.Nil(t, result)
				} else {
					require.NoError(t, err)
					require.NotNil(t, result)
				}
			})
			require.Nil(t, c.Request.Header, "restore must preserve the original nil header")
			requireCodexBridgeIntegrationIdentity(t, up, 0)
		})
	}
}

type codexBridgeIntegrationAccountRepo struct {
	AccountRepository
	accounts map[int64]*Account
}

func (r *codexBridgeIntegrationAccountRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	if account := r.accounts[id]; account != nil {
		return account, nil
	}
	return nil, fmt.Errorf("account %d not found", id)
}

func TestForwardAsAnthropic_CodexBridgeCompressionUsesDeviceProfileAndCredentialSource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tt := range []struct {
		name              string
		mode              string
		convergence       bool
		shadow            bool
		shadowConvergence bool
		compressed        bool
	}{
		{name: "device", mode: "device", convergence: true, compressed: true},
		{name: "device-without-convergence", mode: "device"},
		{name: "off-with-convergence", mode: "off", convergence: true},
		{name: "session-with-convergence", mode: "session", convergence: true},
		{name: "full-with-convergence", mode: "full", convergence: true},
		{name: "shadow-inherits-enabled-source", mode: "device", convergence: true, shadow: true, compressed: true},
		{name: "shadow-cannot-enable-disabled-source", mode: "device", shadow: true, shadowConvergence: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			source := codexBridgeIntegrationAccount(8101, "credential-a")
			source.Extra[codexFingerprintModeExtraKey] = tt.mode
			source.Extra[codexFingerprintConvergenceExtraKey] = tt.convergence
			account := source
			svc, up := codexBridgeIntegrationService("")
			if tt.shadow {
				shadow := *source
				shadow.ID = 8102
				shadow.ParentAccountID = &source.ID
				shadow.Credentials = nil
				shadow.Extra = maps.Clone(source.Extra)
				shadow.Extra[codexFingerprintConvergenceExtraKey] = tt.shadowConvergence
				account = &shadow
				svc.accountRepo = &codexBridgeIntegrationAccountRepo{accounts: map[int64]*Account{source.ID: source}}
			}
			body := codexBridgeIntegrationBody(t, codexBridgeIntegrationSession, false, false)
			codexBridgeIntegrationForward(t, svc, account, body, 71, "", "gpt-5.4")
			codexBridgeIntegrationWireBody(t, up, 0, tt.compressed)
			require.Equal(t, "Bearer token-credential-a", up.lastReq.Header.Get("Authorization"))
			if tt.compressed {
				requireCodexBridgeIntegrationIdentity(t, up, 0)
			}
		})
	}
}

func TestForwardAsAnthropic_CodexBridgeFailoverRestoresInboundHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, failure := range []string{"transport", "http", "stream", "credential"} {
		for _, inboundIdentity := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/inbound-identity=%t", failure, inboundIdentity), func(t *testing.T) {
				first := codexBridgeIntegrationAccount(8101, "credential-a")
				second := codexBridgeIntegrationAccount(8102, "credential-b")
				svc, up := codexBridgeIntegrationService("")
				body := codexBridgeIntegrationBody(t, codexBridgeIntegrationSession, false, false)
				c, rec := codexBridgeIntegrationContext(body, 71)
				c.Request.Header["X-Original"] = []string{"first", "second"}
				if inboundIdentity {
					c.Request.Header["Session-Id"] = []string{"inbound-session", "second-session"}
					c.Request.Header.Set("thread-id", "inbound-thread")
					c.Request.Header.Set("x-codex-window-id", "inbound-window")
					c.Request.Header.Set(openAIWSTurnMetadataHeader, `{ "session_id": "inbound-session", "keep": true }`)
				}
				original, snapshot := c.Request.Header, c.Request.Header.Clone()
				switch failure {
				case "transport":
					up.err = errors.New("connection reset by peer")
				case "http":
					up.responses = []*http.Response{{
						StatusCode: http.StatusServiceUnavailable,
						Header:     http.Header{"Content-Type": []string{"application/json"}},
						Body:       io.NopCloser(strings.NewReader(`{"error":{"type":"server_error","message":"upstream unavailable"}}`)),
					}}
				case "stream":
					up.responses = []*http.Response{{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
						Body: io.NopCloser(strings.NewReader(
							"data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_failed\",\"status\":\"failed\",\"error\":{\"code\":\"rate_limit_exceeded\",\"message\":\"Rate limit reached\"}}}\n\n",
						)),
					}}
				case "credential":
					delete(first.Credentials, "access_token")
				}
				result, err := svc.ForwardAsAnthropic(context.Background(), c, first, body, "", "gpt-5.4")
				require.Error(t, err)
				require.Nil(t, result)
				if failure == "credential" {
					require.ErrorContains(t, err, "get access token")
					require.Empty(t, up.requests)
				} else {
					var failover *UpstreamFailoverError
					require.ErrorAs(t, err, &failover)
					requireCodexBridgeIntegrationIdentity(t, up, 0)
				}
				require.Empty(t, rec.Body.String(), "the failed attempt must leave the response available for failover")
				require.False(t, c.Writer.Written())
				require.Equal(t, snapshot, original, "injection must not mutate the original map or its value slices")
				require.Equal(t, snapshot, c.Request.Header)
				c.Request.Header.Set("X-Restore-Probe", "restored")
				require.Equal(t, "restored", original.Get("X-Restore-Probe"), "restore must reinstate the original header map")
				original.Del("X-Restore-Probe")

				up.err = nil
				up.responses = []*http.Response{openAICompatSSECompletedResponse("resp_failover", "gpt-5.4")}
				result, err = svc.ForwardAsAnthropic(context.Background(), c, second, body, "", "gpt-5.4")
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, snapshot, c.Request.Header)
				require.Equal(t, snapshot, original)
				secondIndex := len(up.requests) - 1
				secondIdentity := requireCodexBridgeIntegrationIdentity(t, up, secondIndex)
				require.Equal(t, "Bearer token-credential-b", up.lastReq.Header.Get("Authorization"))
				require.Equal(t, "credential-b", up.lastReq.Header.Get("ChatGPT-Account-Id"))
				if secondIndex > 0 {
					firstIdentity := requireCodexBridgeIntegrationIdentity(t, up, 0)
					require.NotEqual(t, firstIdentity.session, secondIdentity.session)
					require.NotEqual(t, firstIdentity.contextWindow, secondIdentity.contextWindow)
				}
			})
		}
	}
}

func TestForwardAsAnthropic_CodexBridgeFailoverToNondevice(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc, up := codexBridgeIntegrationService("")
	up.err = errors.New("connection reset by peer")
	body := codexBridgeIntegrationBody(t, codexBridgeIntegrationSession, false, false)
	c, _ := codexBridgeIntegrationContext(body, 71)
	original := c.Request.Header.Clone()
	_, err := svc.ForwardAsAnthropic(context.Background(), c, codexBridgeIntegrationAccount(8101, "credential-a"), body, "", "gpt-5.4")
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.Equal(t, original, c.Request.Header)
	requireCodexBridgeIntegrationIdentity(t, up, 0)

	account := codexBridgeIntegrationAccount(8102, "credential-b")
	account.Extra[codexFingerprintConvergenceExtraKey] = false
	up.err = nil
	result, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "gpt-5.4")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, original, c.Request.Header)
	plain := codexBridgeIntegrationWireBody(t, up, 1, false)
	require.NotEmpty(t, up.lastReq.Header.Get("session_id"))
	for _, name := range []string{"session-id", "thread-id", "x-codex-window-id", openAIWSTurnMetadataHeader} {
		require.Empty(t, up.lastReq.Header.Get(name), "prior attempt must not leak %s", name)
	}
	require.False(t, gjson.GetBytes(plain, "prompt_cache_key").Exists())
	require.False(t, gjson.GetBytes(plain, "client_metadata.session_id").Exists())
	require.False(t, gjson.GetBytes(plain, "client_metadata.x-codex-turn-metadata").Exists())
}

func TestForwardAsAnthropic_CodexBridgeNondeviceCompatibility(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := codexBridgeIntegrationAccount(8101, "credential-a")
	account.Extra[codexFingerprintConvergenceExtraKey] = false
	svc, up := codexBridgeIntegrationService("legacy-turn-state", "")
	for _, followup := range []bool{false, true} {
		body := codexBridgeIntegrationBody(t, codexBridgeIntegrationSession, false, followup)
		codexBridgeIntegrationForward(t, svc, account, body, 71, "", "gpt-5.4")
	}
	require.Len(t, up.requests, 2)
	for index, req := range up.requests {
		body := codexBridgeIntegrationWireBody(t, up, index, false)
		require.NotEmpty(t, req.Header.Get("session_id"))
		for _, name := range []string{"session-id", "thread-id", "x-codex-window-id", openAIWSTurnMetadataHeader} {
			require.Empty(t, req.Header.Get(name), name)
		}
		for _, field := range []string{"prompt_cache_key", "tool_choice", "previous_response_id"} {
			require.False(t, gjson.GetBytes(body, field).Exists(), field)
		}
		metadata := gjson.GetBytes(body, "client_metadata").Map()
		require.Len(t, metadata, 1)
		require.NotEmpty(t, metadata["x-codex-installation-id"].String())
		require.Equal(t, "responses=experimental", req.Header.Get("OpenAI-Beta"))
	}
	require.Equal(t, up.requests[0].Header.Get("session_id"), up.requests[1].Header.Get("session_id"))
	require.Empty(t, up.requests[0].Header.Get(openAICodexTurnStateHeader))
	require.Equal(t, "legacy-turn-state", up.requests[1].Header.Get(openAICodexTurnStateHeader))
}

func TestForwardAsAnthropic_CodexBridgeLegacySessionModes(t *testing.T) {
	for _, mode := range []string{"session", "full"} {
		for _, convergence := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/convergence=%v", mode, convergence), func(t *testing.T) {
				account := codexBridgeIntegrationAccount(8101, "credential-a")
				account.Extra[codexFingerprintModeExtraKey] = mode
				account.Extra[codexFingerprintConvergenceExtraKey] = convergence
				svc, up := codexBridgeIntegrationService("", "")
				for _, followup := range []bool{false, true} {
					body := codexBridgeIntegrationBody(t, codexBridgeIntegrationSession, false, followup)
					codexBridgeIntegrationForward(t, svc, account, body, 71, "", "gpt-5.4")
				}
				for index, req := range up.requests {
					body := codexBridgeIntegrationWireBody(t, up, index, false)
					require.NotEmpty(t, req.Header.Get("session_id"))
					require.Empty(t, req.Header.Get("session-id"))
					require.Empty(t, req.Header.Get("thread-id"))
					require.False(t, gjson.GetBytes(body, "client_metadata.session_id").Exists())
					require.False(t, gjson.GetBytes(body, "client_metadata.thread_id").Exists())
				}
				require.Equal(t, up.requests[0].Header.Get("session_id"), up.requests[1].Header.Get("session_id"))
			})
		}
	}
}

func TestCodexBridgeSessionCacheReclaimsExpiredConversations(t *testing.T) {
	svc := &OpenAIGatewayService{}
	svc.openaiCompatBridgeSessions.Store("expired", openAICompatBridgeSession{
		SessionID: "expired-session", ExpiresAt: time.Now().Add(-time.Minute),
	})
	svc.openaiCompatBridgeSessions.Store("live", openAICompatBridgeSession{
		SessionID: "live-session", ExpiresAt: time.Now().Add(time.Hour),
	})
	account := codexBridgeIntegrationAccount(8101, "credential-a")
	// Exercise normal conversation churn through the cache's production entry.
	for i := 0; i < 256; i++ {
		_, ok := svc.openAICompatBridgeSession(nil, account, fmt.Sprintf("conversation-%d", i))
		require.True(t, ok)
	}
	_, exists := svc.openaiCompatBridgeSessions.Load("expired")
	require.False(t, exists)
	_, exists = svc.openaiCompatBridgeSessions.Load("live")
	require.True(t, exists)
}

func TestCodexBridgeSessionCacheConcurrentRoundsShareIdentity(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := codexBridgeIntegrationAccount(8101, "credential-a")
	var rounds sync.WaitGroup
	sessions := make(chan openAICompatBridgeSession, 32)
	for i := 0; i < cap(sessions); i++ {
		rounds.Add(1)
		go func() {
			defer rounds.Done()
			session, _ := svc.openAICompatBridgeSession(nil, account, "same-conversation")
			sessions <- session
		}()
	}
	rounds.Wait()
	close(sessions)
	first := <-sessions
	require.NotEmpty(t, first.SessionID)
	for session := range sessions {
		require.Equal(t, first.SessionID, session.SessionID)
		require.Equal(t, first.ContextWindowID, session.ContextWindowID)
	}
}

func TestForwardAsAnthropic_CodexBridgeWithoutCacheKeyDoesNotInventSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc, up := codexBridgeIntegrationService("")
	body := codexBridgeIntegrationBody(t, "", false, false)
	codexBridgeIntegrationForward(t, svc, codexBridgeIntegrationAccount(8101, "credential-a"), body, 71, "", "gpt-4o")
	decoded := codexBridgeIntegrationWireBody(t, up, 0, true)
	for _, name := range []string{"session-id", "session_id", "thread-id", "x-codex-window-id", openAIWSTurnMetadataHeader} {
		require.Empty(t, up.lastReq.Header.Get(name), name)
	}
	for _, field := range []string{"prompt_cache_key", "tool_choice", "client_metadata.session_id", "client_metadata.thread_id"} {
		require.False(t, gjson.GetBytes(decoded, field).Exists(), field)
	}
}

func TestForwardAsAnthropic_CodexBridgeSharesShadowCredentialSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	parent := codexBridgeIntegrationAccount(8101, "credential-a")
	shadow := *parent
	shadow.ID = 8102
	shadow.ParentAccountID = &parent.ID
	shadow.Credentials = nil
	shadow.Extra = maps.Clone(parent.Extra)
	delete(shadow.Extra, codexFingerprintConvergenceExtraKey)
	svc, up := codexBridgeIntegrationService("", "")
	svc.accountRepo = &codexBridgeIntegrationAccountRepo{accounts: map[int64]*Account{parent.ID: parent}}
	body := codexBridgeIntegrationBody(t, codexBridgeIntegrationSession, false, false)
	codexBridgeIntegrationForward(t, svc, parent, body, 71, "", "gpt-5.4")
	codexBridgeIntegrationForward(t, svc, &shadow, body, 71, "", "gpt-5.4")
	first := requireCodexBridgeIntegrationIdentity(t, up, 0)
	second := requireCodexBridgeIntegrationIdentity(t, up, 1)
	require.Equal(t, first.session, second.session)
	require.Equal(t, first.contextWindow, second.contextWindow)
	require.Equal(t, first.installation, second.installation)
	require.NotEqual(t, first.turn, second.turn)
	require.Equal(t, up.requests[0].Header.Get("Authorization"), up.requests[1].Header.Get("Authorization"))
}

func TestForwardAsAnthropic_CodexBridgeRecordsTurnStateCredentialOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, cacheKey := range []string{"cache-a", ""} {
		t.Run("cache="+cacheKey, func(t *testing.T) {
			svc, up := codexBridgeIntegrationService("credential-a-blob", "", "")
			svc.cfg.Security.ResponseHeaders = config.ResponseHeaderConfig{
				Enabled: true, AdditionalAllowed: []string{openAICodexTurnStateHeader},
			}
			svc.responseHeaderFilter = compileResponseHeaderFilter(svc.cfg)
			first := codexBridgeIntegrationAccount(8101, "credential-a")
			other := codexBridgeIntegrationAccount(8102, "credential-b")
			body := codexBridgeIntegrationBody(t, "", false, false)
			firstContext := codexBridgeIntegrationForward(t, svc, first, body, 71, cacheKey, "gpt-4o")
			state := firstContext.Writer.Header().Get(openAICodexTurnStateHeader)
			require.Equal(t, "credential-a-blob", state)
			for _, account := range []*Account{other, first} {
				c, _ := codexBridgeIntegrationContext(body, 71)
				c.Request.Header.Set(openAICodexTurnStateHeader, state)
				original := c.Request.Header.Clone()
				result, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, cacheKey, "gpt-4o")
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, original, c.Request.Header)
			}
			require.Len(t, up.requests, 3)
			require.Empty(t, up.requests[1].Header.Get(openAICodexTurnStateHeader), "known foreign-credential blob must be stripped")
			require.Equal(t, "credential-a-blob", up.requests[2].Header.Get(openAICodexTurnStateHeader), "same-credential echo must survive")
		})
	}
}

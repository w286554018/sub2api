package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const codexIdentitySessionFixture = "0198f0de-7b2e-7abc-8def-123456789abc"

func codexIdentityHTTPContext(path string, headers http.Header) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, path, nil)
	c.Request.Header = headers.Clone()
	if c.Request.Header == nil {
		c.Request.Header = http.Header{}
	}
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.153.4")
	c.Request.Header.Set("originator", "codex_cli_rs")
	c.Set("api_key", &APIKey{ID: 81})
	return c
}

func codexIdentityForward(t *testing.T, account *Account, path string, headers http.Header, body []byte, passthrough bool) (http.Header, []byte) {
	t.Helper()
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(compactProbeSSESuccessBody)),
	}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream, toolCorrector: NewCodexToolCorrector()}
	c := codexIdentityHTTPContext(path, headers)
	var err error
	if passthrough {
		_, err = svc.forwardOpenAIPassthrough(context.Background(), c, account, body, body, "gpt-5.4", false, nil, false, time.Now())
	} else {
		_, err = svc.Forward(context.Background(), c, account, body)
	}
	require.NoError(t, err)
	require.NotNil(t, upstream.lastReq)
	wire := upstream.lastBody
	if upstream.lastReq.Header.Get("Content-Encoding") == "zstd" {
		decoder, err := zstd.NewReader(nil)
		require.NoError(t, err)
		defer decoder.Close()
		wire, err = decoder.DecodeAll(wire, nil)
		require.NoError(t, err)
	}
	return upstream.lastReq.Header, wire
}

func codexIdentityAccount() *Account {
	a := newTestOAuthAccount(9120, map[string]any{
		codexFingerprintModeExtraKey: "device", codexFingerprintConvergenceExtraKey: true,
	})
	a.Credentials = map[string]any{"access_token": "test-token", "chatgpt_account_id": "identity-upstream"}
	a.Concurrency = 1
	return a
}

func TestCodexIdentityHTTPForwardCarriers(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, evidence := range []string{"headers", "body", "embedded"} {
			name := evidence
			if passthrough {
				name += "/passthrough"
			}
			t.Run(name, func(t *testing.T) {
				a := codexIdentityAccount()
				turn := "0198f0df-0123-7abc-8def-123456789abc"
				rawMeta := `{ "session_id" : "` + codexIdentitySessionFixture + `", "thread_id":"` + codexIdentitySessionFixture +
					`", "turn_id":"` + turn + `", "root_turn_id":"` + turn + `", "window_id":"` + codexIdentitySessionFixture +
					`:9", "window_number":9, "unknown" : "\u4fdd\u7559", "ratio":1e+06 }`
				cm := map[string]any{openAIWSTurnMetadataHeader: rawMeta}
				if evidence != "embedded" {
					cm["session_id"] = codexIdentitySessionFixture
					cm["thread_id"] = codexIdentitySessionFixture
					cm["x-codex-window-id"] = codexIdentitySessionFixture + ":9"
				}
				body, err := json.Marshal(map[string]any{
					"model": "gpt-5.4", "stream": true, "input": "hi", "instructions": "answer",
					"client_metadata": cm, "prompt_cache_key": codexIdentitySessionFixture,
				})
				require.NoError(t, err)
				headers := http.Header{}
				if evidence == "headers" {
					headers.Set("session-id", codexIdentitySessionFixture)
					headers.Set("thread-id", codexIdentitySessionFixture)
					headers.Set("x-codex-window-id", codexIdentitySessionFixture+":9")
					headers.Set(openAIWSTurnMetadataHeader, rawMeta)
				}
				h, wire := codexIdentityForward(t, a, "/v1/responses", headers, body, passthrough)
				sid, tid := h.Get("session-id"), h.Get("thread-id")
				require.NotEmpty(t, sid)
				require.Equal(t, sid, tid)
				require.Equal(t, sid, gjson.GetBytes(wire, "prompt_cache_key").String())
				require.Equal(t, tid, h.Get("x-client-request-id"))
				require.Empty(t, h.Get("session_id"))
				require.Empty(t, h.Get("conversation_id"))
				require.Equal(t, uuid.Version(7), uuid.MustParse(sid).Version())
				require.Equal(t, codexIdentitySessionFixture[:13], sid[:13])
				require.NotEqual(t, codexIdentitySessionFixture, sid)
				if evidence != "embedded" {
					require.Equal(t, sid, gjson.GetBytes(wire, "client_metadata.session_id").String())
					require.Equal(t, tid+":9", gjson.GetBytes(wire, "client_metadata.x-codex-window-id").String())
				}
				embedded := gjson.GetBytes(wire, "client_metadata."+openAIWSTurnMetadataHeader).String()
				require.Equal(t, sid, gjson.Get(embedded, "session_id").String())
				require.Equal(t, tid+":9", gjson.Get(embedded, "window_id").String())
				require.Equal(t, gjson.Get(embedded, "turn_id").String(), gjson.Get(embedded, "root_turn_id").String())
				require.Contains(t, embedded, `"unknown" : "\u4fdd\u7559"`)
				require.Contains(t, embedded, `"ratio":1e+06`)
				require.Equal(t, turn[:13], gjson.Get(embedded, "turn_id").String()[:13])
			})
		}
	}
}

func TestCodexIdentityCompactNormalizationReachesDeviceProfile(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, enabled := range []bool{false, true} {
			name := "off"
			if enabled {
				name = "device"
			}
			if passthrough {
				name += "/passthrough"
			}
			t.Run(name, func(t *testing.T) {
				a := codexIdentityAccount()
				a.Extra[codexFingerprintConvergenceExtraKey] = enabled
				raw := []byte(`{"model":"gpt-5.4","input":"compact","prompt_cache_key":"` + codexIdentitySessionFixture +
					`","access_programs":["tool-a"],"stream":false,"store":false}`)
				normalized, _, err := normalizeOpenAICompactRequestBody(raw)
				require.NoError(t, err)
				require.Equal(t, codexIdentitySessionFixture, gjson.GetBytes(normalized, "prompt_cache_key").String(),
					"normalization runs before the account/device switch is known")
				headers := http.Header{}
				headers.Set("session-id", codexIdentitySessionFixture)
				headers.Set("thread-id", codexIdentitySessionFixture)
				headers.Set("x-codex-installation-id", "client-installation")
				h, wire := codexIdentityForward(t, a, "/v1/responses/compact", headers, normalized, passthrough)
				require.Empty(t, h.Get("Content-Encoding"))
				if enabled {
					require.Equal(t, h.Get("session-id"), gjson.GetBytes(wire, "prompt_cache_key").String())
					require.Equal(t, `["tool-a"]`, gjson.GetBytes(wire, "access_programs").Raw)
					require.Empty(t, h.Get("x-client-request-id"))
					require.Equal(t, resolveConvergedInstallationID(a, testCodexFingerprintSeed), h.Get("x-codex-installation-id"))
				} else {
					require.False(t, gjson.GetBytes(wire, "prompt_cache_key").Exists())
					require.False(t, gjson.GetBytes(wire, "access_programs").Exists())
				}
			})
		}
	}
}

func TestCodexIdentityCompositeValuesAndMetadataBytes(t *testing.T) {
	account := codexIdentityAccount()
	session := scopeCodexAccountIdentityValue(account, 81, "session", codexIdentitySessionFixture)
	require.Equal(t, session, scopeCodexAccountIdentityValue(account, 81, "thread", codexIdentitySessionFixture))
	require.Equal(t, session+":19", scopeCodexAccountIdentityValue(account, 81, "window", codexIdentitySessionFixture+":19"))
	require.Equal(t, "agent_x:"+session, scopeCodexAccountIdentityValue(account, 81, "prompt-cache", "agent_x:"+codexIdentitySessionFixture))
	require.NotEqual(t, session, scopeCodexAccountIdentityValue(account, 82, "session", codexIdentitySessionFixture))
	other := codexIdentityAccount()
	other.Credentials["chatgpt_account_id"] = "other-upstream"
	require.NotEqual(t, session, scopeCodexAccountIdentityValue(other, 81, "session", codexIdentitySessionFixture))

	raw := `{ "session_id" : "` + codexIdentitySessionFixture + `", "session_id":"` + codexIdentitySessionFixture + `", "unknown" : "\u4fdd\u7559", "n":1e+06 }`
	rewritten := scopeCodexAccountTurnMetadata(raw, account, 81)
	require.Equal(t, strings.ReplaceAll(raw, codexIdentitySessionFixture, session), rewritten)
	body := []byte(`{"input": [ {"text":"\u4fdd\u7559","n":1e+06} ],"prompt_cache_key":"agent_x:` + codexIdentitySessionFixture + `"}`)
	next, changed, err := applyCodexAccountIdentityClientMetadataRaw(body, account, 81)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, bytes.ReplaceAll(body, []byte(codexIdentitySessionFixture), []byte(session)), next)
}

func TestCodexIdentityDuplicateMembersCannotBypassScoping(t *testing.T) {
	account := codexIdentityAccount()
	for _, raw := range []string{
		`{ "session_id":"` + codexIdentitySessionFixture + `", "session_id":null, "thread_id":"t" }`,
		`{ "session_id":null, "session_id":"` + codexIdentitySessionFixture + `" }`,
		`{ "session_id":"` + codexIdentitySessionFixture + `", "session_id":"other-session", "keep":1e+06 }`,
	} {
		t.Run(raw, func(t *testing.T) {
			got := scopeCodexAccountTurnMetadata(raw, account, 81)
			expected := strings.ReplaceAll(raw, codexIdentitySessionFixture, scopeCodexAccountIdentityValue(account, 81, "session", codexIdentitySessionFixture))
			expected = strings.ReplaceAll(expected, `"other-session"`, `"`+scopeCodexAccountIdentityValue(account, 81, "session", "other-session")+`"`)
			expected = strings.ReplaceAll(expected, `"t"`, `"`+scopeCodexAccountIdentityValue(account, 81, "thread", "t")+`"`)
			require.Equal(t, expected, got)
		})
	}
}

func TestCodexTurnStateDuplicateMembersCannotBypassGuard(t *testing.T) {
	svc := &OpenAIGatewayService{}
	a := codexIdentityAccount()
	b := codexIdentityAccount()
	b.Credentials["chatgpt_account_id"] = "other-upstream"
	svc.noteOpenAICodexTurnStateOrigin(nil, a, "foreign-blob")
	for _, raw := range []string{
		`{"client_metadata":{"x-codex-turn-state":"foreign-blob","x-codex-turn-state":"foreign-blob","keep":1e+06}}`,
		`{"client_metadata":{"x-codex-turn-state":"unknown","x-codex-turn-state":"foreign-blob","keep":1e+06}}`,
		`{"client_metadata":{"keep":1e+06},"client_metadata":{"x-codex-turn-state":"foreign-blob"}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			got := svc.guardOpenAICodexWSFrameTurnState(nil, b, []byte(raw))
			require.True(t, gjson.ValidBytes(got))
			require.NotContains(t, string(got), "foreign-blob")
			require.Contains(t, string(got), `"keep":1e+06`)
		})
	}
}

func TestCodexIdentityCompactAPIKeyEgressFiltersDeviceFields(t *testing.T) {
	account := &Account{ID: 9129, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	body, _, err := normalizeOpenAICompactRequestBody([]byte(`{"model":"gpt-5.4","input":"hi","prompt_cache_key":"cache","access_programs":["tool"]}`))
	require.NoError(t, err)
	svc := &OpenAIGatewayService{}
	c := codexIdentityHTTPContext("/v1/responses/compact", nil)
	for _, passthrough := range []bool{false, true} {
		var req *http.Request
		if passthrough {
			req, err = svc.buildUpstreamRequestOpenAIPassthrough(context.Background(), c, account, body, "token")
		} else {
			req, err = svc.buildUpstreamRequest(context.Background(), c, account, body, "token", false, "", false)
		}
		require.NoError(t, err)
		wire, readErr := io.ReadAll(req.Body)
		require.NoError(t, readErr)
		require.NoError(t, req.Body.Close())
		require.False(t, gjson.GetBytes(wire, "prompt_cache_key").Exists())
		require.False(t, gjson.GetBytes(wire, "access_programs").Exists())
	}
}

func TestCodexTurnStateBlobOwnerOverridesLegacySession(t *testing.T) {
	svc := &OpenAIGatewayService{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("session-id", "shared-session")
	c.Set("api_key", &APIKey{ID: 81})
	a := newTestOAuthAccount(9101, nil)
	a.Credentials = map[string]any{"chatgpt_account_id": "owner-a"}
	b := newTestOAuthAccount(9102, nil)
	b.Credentials = map[string]any{"chatgpt_account_id": "owner-b"}
	svc.relayOpenAICodexTurnState(c, a, http.Header{
		http.CanonicalHeaderKey(openAICodexTurnStateHeader): {"state-a"},
	})
	svc.relayOpenAICodexTurnState(c, b, http.Header{
		http.CanonicalHeaderKey(openAICodexTurnStateHeader): {"state-b"},
	})
	h := http.Header{}
	h.Set(openAICodexTurnStateHeader, "state-a")
	svc.guardOpenAICodexTurnStateEcho(c, a, h)
	require.Equal(t, "state-a", h.Get(openAICodexTurnStateHeader),
		"the latest response on a shared session must not reassign an older blob")
	svc.guardOpenAICodexTurnStateEcho(c, b, h)
	require.Empty(t, h.Get(openAICodexTurnStateHeader))
}

func TestCodexTurnStateOriginWritesSweepExpiredEntries(t *testing.T) {
	svc := &OpenAIGatewayService{}
	svc.openaiCodexTurnStateOrigins.Store("expired-blob", openAICodexTurnStateOrigin{
		owner: "old", expiresAt: time.Now().Add(-time.Minute),
	})
	svc.openaiCodexTurnStateWrites.Store(255)
	account := newTestOAuthAccount(9103, nil)
	svc.noteOpenAICodexTurnStateOrigin(nil, account, "current-state")
	_, exists := svc.openaiCodexTurnStateOrigins.Load("expired-blob")
	require.False(t, exists)
	_, exists = svc.openaiCodexTurnStateOrigins.Load(openAICodexTurnStateKey("current-state"))
	require.True(t, exists)
}

func TestCodexWindowEvidenceUsesCredentialSourceSwitch(t *testing.T) {
	child := newTestOAuthAccount(9104, map[string]any{codexFingerprintModeExtraKey: "session"})
	parent := newTestOAuthAccount(9105, map[string]any{codexFingerprintConvergenceExtraKey: true})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("session-id", "client-session")
	c.Request.Header.Set("x-codex-window-id", "0198f0de-7b2e-7abc-8def-123456789abc:9")
	c.Set(codexAccountIdentitySourceContextKey, parent)
	ids := resolveCodexFingerprintIDsWithBody(c, child, nil, nil)
	require.NotNil(t, ids)
	require.Equal(t, uint64(9), ids.windowNumber)
	parent.Extra[codexFingerprintConvergenceExtraKey] = false
	ids = resolveCodexFingerprintIDsWithBody(c, child, nil, nil)
	require.NotNil(t, ids)
	require.Zero(t, ids.windowNumber)
}

func TestCodexTurnStateHTTPBuildersUseCredentialNamespace(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		name := "forward"
		if passthrough {
			name = "passthrough"
		}
		t.Run(name, func(t *testing.T) {
			svc := &OpenAIGatewayService{}
			account := newTestOAuthAccount(9106, nil)
			account.Credentials = map[string]any{"chatgpt_account_id": "same-upstream"}
			same := newTestOAuthAccount(9107, nil)
			same.Credentials = map[string]any{"chatgpt_account_id": "same-upstream"}
			other := newTestOAuthAccount(9108, nil)
			other.Credentials = map[string]any{"chatgpt_account_id": "other-upstream"}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			c.Request.Header.Set("session-id", "stable-session")
			svc.relayOpenAICodexTurnState(c, account, http.Header{
				http.CanonicalHeaderKey(openAICodexTurnStateHeader): {"http-minted"},
			})
			c.Request.Header.Set(openAICodexTurnStateHeader, "http-minted")
			for _, target := range []*Account{same, other} {
				var req *http.Request
				var err error
				if passthrough {
					req, err = svc.buildUpstreamRequestOpenAIPassthrough(context.Background(), c, target,
						[]byte(`{"model":"gpt-5","input":"hello"}`), "token")
				} else {
					req, err = svc.buildUpstreamRequest(context.Background(), c, target,
						[]byte(`{"model":"gpt-5","input":"hello"}`), "token", true, "", true)
				}
				require.NoError(t, err)
				if target == same {
					require.Equal(t, "http-minted", req.Header.Get(openAICodexTurnStateHeader))
				} else {
					require.Empty(t, req.Header.Get(openAICodexTurnStateHeader))
				}
			}
		})
	}
}

func codexIdentityUpstreamResponse(stream bool, blob string) *http.Response {
	headers := http.Header{}
	if blob != "" {
		headers.Set(openAICodexTurnStateHeader, blob)
	}
	body := `{"id":"resp_identity","object":"response","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`
	headers.Set("Content-Type", "application/json")
	if stream {
		headers.Set("Content-Type", "text/event-stream")
		body = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_identity\"}}\n\n" +
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" +
			"data: {\"type\":\"response.completed\",\"response\":" + body + "}\n\n"
	}
	return &http.Response{StatusCode: http.StatusOK, Header: headers, Body: io.NopCloser(strings.NewReader(body))}
}

func TestCodexTurnStateHTTPForwardShadowAndFailover(t *testing.T) {
	for _, tc := range []struct {
		name        string
		stream      bool
		passthrough bool
	}{
		{name: "json"},
		{name: "staged-sse", stream: true},
		{name: "passthrough-sse", stream: true, passthrough: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := codexIdentityAccount()
			child := codexIdentityAccount()
			child.ID = parent.ID + 1
			child.ParentAccountID = &parent.ID
			child.Credentials = map[string]any{}
			child.Extra["openai_passthrough"] = tc.passthrough
			other := codexIdentityAccount()
			other.ID = parent.ID + 2
			other.Credentials["chatgpt_account_id"] = "other-upstream"
			other.Extra["openai_passthrough"] = tc.passthrough
			upstream := &httpUpstreamRecorder{responses: []*http.Response{
				codexIdentityUpstreamResponse(tc.stream, "minted-by-parent"),
				codexIdentityUpstreamResponse(tc.stream, ""),
				codexIdentityUpstreamResponse(tc.stream, ""),
			}}
			cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
			if tc.stream {
				cfg.Gateway.OpenAIFirstOutputTimeoutSeconds = 1
			}
			svc := &OpenAIGatewayService{
				cfg: cfg, httpUpstream: upstream, toolCorrector: NewCodexToolCorrector(),
				responseHeaderFilter: compileResponseHeaderFilter(cfg),
				accountRepo:          &stubQuotaAccountRepo{accounts: map[int64]*Account{parent.ID: parent}},
			}
			body, err := json.Marshal(map[string]any{"model": "gpt-5.4", "input": "hello", "stream": tc.stream})
			require.NoError(t, err)
			headers := http.Header{}
			headers.Set("session-id", "shared-downstream-session")
			c := codexIdentityHTTPContext("/v1/responses", headers)
			_, err = svc.Forward(context.Background(), c, child, body)
			require.NoError(t, err)
			require.Equal(t, "minted-by-parent", c.Writer.Header().Get(openAICodexTurnStateHeader))
			require.Equal(t, "minted-by-parent", svc.guardOpenAICodexTurnStateValue(nil, parent, "minted-by-parent"))
			require.Empty(t, svc.guardOpenAICodexTurnStateValue(nil, other, "minted-by-parent"))
			headers.Set(openAICodexTurnStateHeader, "minted-by-parent")
			c = codexIdentityHTTPContext("/v1/responses", headers)
			_, err = svc.Forward(context.Background(), c, child, body)
			require.NoError(t, err)
			require.Equal(t, "minted-by-parent", upstream.lastReq.Header.Get(openAICodexTurnStateHeader))
			// Reuse the context as the real handler does across account attempts.
			_, err = svc.Forward(context.Background(), c, other, body)
			require.NoError(t, err)
			require.Empty(t, upstream.lastReq.Header.Get(openAICodexTurnStateHeader))
			require.Empty(t, c.Writer.Header().Get(openAICodexTurnStateHeader))
			require.Len(t, upstream.requests, 3)
		})
	}
}

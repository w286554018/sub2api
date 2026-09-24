package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func codexOuterDuplicateBody() []byte {
	metadata := func(suffix string) string {
		embedded, _ := json.Marshal(`{"session_id":"raw-session-` + suffix +
			`","thread_id":"raw-thread-` + suffix + `","keep":1e+06,"keep":null}`)
		return `{"session_id":"raw-session-` + suffix + `","session_id":"raw-extra-` + suffix +
			`","thread_id":"raw-thread-` + suffix + `","x-codex-installation-id":"raw-install-` + suffix +
			`","x-codex-turn-metadata":` + string(embedded) + `,"x-codex-turn-metadata":` + string(embedded) +
			`,"x-codex-ws-stream-request-start-ms":"1","x-codex-ws-stream-request-start-ms":"2"` +
			`,"keep":1e+06,"keep":null,"note":"raw-session-a"}`
	}
	return []byte(`{"model":"gpt-5.4","instructions":"duplicates","stream":true,"input":"hi",` +
		`"client_metadata":` + metadata("a") + `,"client_metadata":` + metadata("b") +
		`,"prompt_cache_key":"raw-session-a","prompt_cache_key":"raw-session-b"}`)
}

func requireCodexOuterDuplicatesScoped(t *testing.T, body []byte, preserve bool) {
	t.Helper()
	require.True(t, gjson.ValidBytes(body), string(body))
	metadataCount, cacheCount := 0, 0
	gjson.ParseBytes(body).ForEach(func(key, value gjson.Result) bool {
		switch key.Str {
		case "prompt_cache_key":
			cacheCount++
			require.NotEmpty(t, value.Str)
			require.False(t, strings.HasPrefix(value.Str, "raw-"), value.Raw)
		case "client_metadata":
			metadataCount++
			if preserve {
				require.Contains(t, value.Raw, `"keep":1e+06,"keep":null`)
				require.Contains(t, value.Raw, `"note":"raw-session-a"`)
			}
			embeddedCount := 0
			value.ForEach(func(name, member gjson.Result) bool {
				switch name.Str {
				case "session_id", "thread_id", "x-codex-installation-id":
					require.False(t, strings.HasPrefix(member.Str, "raw-"), member.Raw)
				case openAIWSTurnMetadataHeader:
					embeddedCount++
					metadata := gjson.Parse(member.Str)
					require.False(t, strings.HasPrefix(metadata.Get("session_id").Str, "raw-"), member.Str)
					require.False(t, strings.HasPrefix(metadata.Get("thread_id").Str, "raw-"), member.Str)
					require.Contains(t, member.Str, `"keep":1e+06,"keep":null`)
				}
				return true
			})
			if preserve {
				require.Equal(t, 2, embeddedCount)
			}
		}
		return true
	})
	if preserve {
		require.Equal(t, 2, metadataCount)
		require.Equal(t, 2, cacheCount)
	} else {
		require.Positive(t, metadataCount)
		require.Positive(t, cacheCount)
	}
}

func TestCodexOuterDuplicateHTTPForward(t *testing.T) {
	for _, mode := range []string{"off", "device", "session", "full"} {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/enabled=%v", mode, enabled), func(t *testing.T) {
				account := codexIdentityAccount()
				account.Extra[codexFingerprintModeExtraKey] = mode
				account.Extra[codexFingerprintConvergenceExtraKey] = enabled
				account.Extra["openai_passthrough"] = true
				_, body := codexIdentityForward(t, account, "/v1/responses", nil, codexOuterDuplicateBody(), false)
				requireCodexOuterDuplicatesScoped(t, body, true)
			})
		}
	}
}

func TestCodexOuterDuplicateWebSocketIngress(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{OpenAIWSIngressModeCtxPool, OpenAIWSIngressModePassthrough} {
		t.Run(mode, func(t *testing.T) {
			upstream := newCodexWSIntegrationUpstream(t, nil)
			svc := newCodexWSIntegrationService(t, upstream, false)
			account := codexWSIntegrationAccount(9760, "outer-duplicate-owner", true, mode)
			frame := append([]byte(`{"type":"response.create",`), codexOuterDuplicateBody()[1:]...)
			headers := codexWSIntegrationHeaders()
			headers.Set(openAICodexTurnStateHeader, "header-state")
			started := time.Now().UnixMilli()
			runCodexWSIntegrationIngress(t, svc, account, headers, frame)
			_, sent := upstream.snapshot()
			require.Len(t, sent, 1)
			requireCodexOuterDuplicatesScoped(t, sent[0].payload, true)
			gjson.ParseBytes(sent[0].payload).ForEach(func(key, value gjson.Result) bool {
				if key.Str != "client_metadata" {
					return true
				}
				require.Equal(t, "header-state", value.Get(openAICodexTurnStateHeader).String())
				stamps := 0
				value.ForEach(func(name, member gjson.Result) bool {
					if name.Str == codexWSStreamRequestStartKey {
						stamps++
						require.Equal(t, gjson.String, member.Type)
						require.GreaterOrEqual(t, member.Int(), started)
					}
					return true
				})
				require.Equal(t, 2, stamps)
				return true
			})
		})
	}
}

func TestCodexOuterDuplicateMetadataHeaderProjection(t *testing.T) {
	account := codexIdentityAccount()
	raw := `{"session_id":"raw-session","tool_namespaces_info":["first"],"keep":1e+06,"tool_namespaces_info":["last"],"keep":null}`
	headers := http.Header{}
	headers.Set(openAIWSTurnMetadataHeader, raw)
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprintf("passthrough=%v", passthrough), func(t *testing.T) {
			got, _ := codexIdentityForward(t, account, "/v1/responses", headers,
				[]byte(`{"model":"gpt-5.4","instructions":"headers","input":"hi","stream":true}`), passthrough)
			projected := got.Get(openAIWSTurnMetadataHeader)
			require.True(t, gjson.Valid(projected))
			require.NotContains(t, projected, "tool_namespaces_info")
			require.Contains(t, projected, `"keep":1e+06`)
			require.Contains(t, projected, `"keep":null`)
		})
	}
}

func TestCodexOuterDuplicateWSFillOnlyPreservesEveryExplicitValue(t *testing.T) {
	body := []byte(`{"client_metadata":{"state":"explicit","state":null,"state":" "},"client_metadata":{"keep":1e+06}}`)
	got := setCodexWSClientMetadataString(body, "state", "fallback", true)
	require.Equal(t, `{"client_metadata":{"state":"explicit","state":"fallback","state":"fallback"},"client_metadata":{"keep":1e+06,"state":"fallback"}}`, string(got))
	for _, invalid := range []string{`{"client_metadata":`, `[{}]`, `null`} {
		require.Equal(t, invalid, string(setCodexWSClientMetadataString([]byte(invalid), "state", "fallback", true)))
	}
}

func TestCodexOuterDuplicateWSStateKeepsLiteralUTF8(t *testing.T) {
	state := "a<b>&c \u00e9"
	body := []byte(`{"client_metadata":{"state":null,"state":""},"client_metadata":{}}`)
	got := setCodexWSClientMetadataString(body, "state", state, true)
	require.Equal(t, `{"client_metadata":{"state":"`+state+`","state":"`+state+`"},"client_metadata":{"state":"`+state+`"}}`, string(got))
	require.NotContains(t, string(got), `\u003c`)
	require.NotContains(t, string(got), `\u00e9`)
}

func TestCodexOuterDuplicateEmbeddedKeepsLiteralUTF8(t *testing.T) {
	account := codexIdentityAccount()
	metadata := `{"session_id":"raw-session","literal":"a<b>&c ` + "\u00e9" + `"}`
	encoded, err := marshalOpenAIUpstreamJSON(metadata)
	require.NoError(t, err)
	body := []byte(`{"client_metadata":{"x-codex-turn-metadata":` + string(encoded) + `}}`)
	scoped, _, err := applyCodexAccountIdentityClientMetadataRaw(body, account, 81)
	require.NoError(t, err)
	ids := resolveCodexFingerprintIDsFromRequest(account, nil)
	scoped, _, err = applyCodexFingerprintClientMetadataRaw(scoped, ids)
	require.NoError(t, err)
	embedded := gjson.GetBytes(scoped, "client_metadata."+openAIWSTurnMetadataHeader).String()
	require.Equal(t, "a<b>&c \u00e9", gjson.Get(embedded, "literal").String())
	require.NotContains(t, embedded, "\u00e9")
	require.Contains(t, embedded, `\u00e9`)
}

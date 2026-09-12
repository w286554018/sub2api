package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAlphaSearchAuxiliaryMCPProjectionPreservesUnknownDuplicates(t *testing.T) {
	raw := `{ "session_id":"search-session", "thread_id":"search-session", "turn_id":"turn", "installation_id":"old", "installation_id":"other", "window_number":4, "tool_namespaces_info":{}, "tool_namespaces_info":[], "agent_name":"agent", "parent_turn_id":"parent", "root_turn_id":"root", "request_kind":"kind", "compaction":{}, "history_ingest_requested":true, "forked_from_ordinal_exclusive":8, "context_window_id":"window", "window_id":"window", "codex_version":"old", "codex_version":"older", "model":"wrong", "model":"wrong-again", "future":1.00e+03, "future":"\u0061" }`
	headers := http.Header{}
	headers.Set(openAIWSTurnMetadataHeader, raw)
	c := codexIdentityHTTPContext("/v1/alpha/search", headers)
	account := codexIdentityAccount()
	account.Credentials["model_mapping"] = map[string]any{"custom-model": "gpt-5.4"}
	body := []byte(`{ "id":"search-session", "id":"custom-id", "id":"search-session", "model":"custom-model", "commands":{}, "future":1.00e+03, "future":"\u0061", "store":false, "store":true }`)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{"output":"fixture"}`)),
	}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	result, err := svc.ForwardAlphaSearch(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, result.WebSearchCalls)
	require.Equal(t, "custom-model", result.Model)
	require.Equal(t, "gpt-5.4", result.UpstreamModel)
	require.Empty(t, upstream.lastReq.Header.Get("Content-Encoding"))
	meta := upstream.lastReq.Header.Get(openAIWSTurnMetadataHeader)
	session := gjson.Get(meta, "session_id").String()
	require.NotEmpty(t, session)
	require.NotEqual(t, "search-session", session)
	counts := map[string]int{}
	gjson.Parse(meta).ForEach(func(key, value gjson.Result) bool {
		counts[key.Str]++
		switch key.Str {
		case "session_id", "thread_id", "turn_id", "future":
		case "codex_version":
			require.Equal(t, upstream.lastReq.Header.Get("Version"), value.Str)
		case "model":
			require.Equal(t, "gpt-5.4", value.Str)
		default:
			t.Errorf("unexpected Responses-only MCP field: %s", key.Str)
		}
		return true
	})
	require.Equal(t, 2, counts["model"])
	require.Equal(t, 2, counts["codex_version"])
	require.Contains(t, meta, `"future":1.00e+03, "future":"\u0061"`)
	var ids []string
	gjson.ParseBytes(upstream.lastBody).ForEach(func(key, value gjson.Result) bool {
		if key.Str == "id" {
			ids = append(ids, value.Str)
		}
		require.NotEqual(t, "store", key.Str)
		return true
	})
	require.Equal(t, []string{session, "custom-id", session}, ids)
	require.Contains(t, string(upstream.lastBody), `"future":1.00e+03, "future":"\u0061"`)
	require.Equal(t, int64(len(upstream.lastBody)), upstream.lastReq.ContentLength)
	replay, err := upstream.lastReq.GetBody()
	require.NoError(t, err)
	defer replay.Close()
	replayed, err := io.ReadAll(replay)
	require.NoError(t, err)
	require.Equal(t, upstream.lastBody, replayed)
	require.Equal(t, raw, c.GetHeader(openAIWSTurnMetadataHeader))
}

func TestAlphaSearchAuxiliarySessionRequiresExactUnambiguousSource(t *testing.T) {
	for _, tc := range []struct {
		name, body, metadata string
		sync                 bool
	}{
		{"same", `{"id":"session","model":"gpt-5.4"}`, `{"session_id":"session"}`, true},
		{"same duplicate metadata", `{"id":"session","model":"gpt-5.4"}`, `{"session_id":"session","session_id":"session"}`, true},
		{"custom", `{"id":"custom","model":"gpt-5.4"}`, `{"session_id":"session"}`, false},
		{"no trim proof", `{"id":" session ","model":"gpt-5.4"}`, `{"session_id":"session"}`, false},
		{"number", `{"id":42,"model":"gpt-5.4"}`, `{"session_id":"42"}`, false},
		{"numeric metadata", `{"id":"42","model":"gpt-5.4"}`, `{"session_id":42}`, false},
		{"missing id", `{"model":"gpt-5.4"}`, `{"session_id":"session"}`, false},
		{"missing metadata", `{"id":"session","model":"gpt-5.4"}`, `{}`, false},
		{"invalid metadata", `{"id":"session","model":"gpt-5.4"}`, `{"session_id":"session"`, false},
		{"ambiguous metadata", `{"id":"session","model":"gpt-5.4"}`, `{"session_id":"session","session_id":"other"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			headers := http.Header{}
			headers.Set(openAIWSTurnMetadataHeader, tc.metadata)
			c := codexIdentityHTTPContext("/v1/alpha/search", headers)
			req, err := (&OpenAIGatewayService{}).buildOpenAIAlphaSearchRequest(context.Background(), c, codexIdentityAccount(), []byte(tc.body), "fixture")
			require.NoError(t, err)
			defer req.Body.Close()
			got, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			if tc.sync {
				id := gjson.GetBytes(got, "id").String()
				require.NotEqual(t, "session", id)
				require.Equal(t, gjson.Get(req.Header.Get(openAIWSTurnMetadataHeader), "session_id").String(), id)
			} else {
				require.Equal(t, tc.body, string(got))
			}
			require.Empty(t, req.Header.Get("Content-Encoding"))
			require.False(t, gjson.Get(req.Header.Get(openAIWSTurnMetadataHeader), "model").Exists())
			require.False(t, gjson.Get(req.Header.Get(openAIWSTurnMetadataHeader), "codex_version").Exists())
		})
	}
}

func TestAlphaSearchAuxiliaryProjectionAndPATCompressionGates(t *testing.T) {
	for _, mode := range []string{"off", "device", "session", "full"} {
		for _, enabled := range []bool{false, true} {
			name := mode + "/disabled"
			if enabled {
				name = mode + "/enabled"
			}
			t.Run(name, func(t *testing.T) {
				account := codexIdentityAccount()
				account.Extra[codexFingerprintModeExtraKey] = mode
				account.Extra[codexFingerprintConvergenceExtraKey] = enabled
				headers := http.Header{}
				headers.Set(openAIWSTurnMetadataHeader, `{"session_id":"session","installation_id":"device","model":"old","codex_version":"old"}`)
				c := codexIdentityHTTPContext("/v1/alpha/search", headers)
				alpha := []byte(`{"id":"custom","model":"gpt-5.4","commands":{}}`)
				svc := &OpenAIGatewayService{}
				req, err := svc.buildOpenAIAlphaSearchRequest(context.Background(), c, account, alpha, "fixture")
				require.NoError(t, err)
				defer req.Body.Close()
				wire, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				require.Equal(t, alpha, wire)
				require.Empty(t, req.Header.Get("Content-Encoding"))
				meta := gjson.Parse(req.Header.Get(openAIWSTurnMetadataHeader))
				profile := mode == "device" && enabled
				if profile {
					require.False(t, meta.Get("installation_id").Exists())
					require.Equal(t, "gpt-5.4", meta.Get("model").String())
					require.Equal(t, req.Header.Get("Version"), meta.Get("codex_version").String())
				} else {
					require.NotEmpty(t, meta.Get("installation_id").String())
					require.Equal(t, "old", meta.Get("model").String())
					require.Equal(t, "old", meta.Get("codex_version").String())
				}
				responses, err := buildOpenAIAlphaSearchResponsesWebSearchBody(alpha, "gpt-5.4")
				require.NoError(t, err)
				patReq, err := svc.buildOpenAIAlphaSearchResponsesWebSearchRequest(context.Background(), c, account, alpha, responses, "at-fixture")
				require.NoError(t, err)
				defer patReq.Body.Close()
				patWire, err := io.ReadAll(patReq.Body)
				require.NoError(t, err)
				if profile {
					require.Equal(t, "zstd", patReq.Header.Get("Content-Encoding"))
				} else {
					require.Empty(t, patReq.Header.Get("Content-Encoding"))
				}
				require.Equal(t, responses, auxiliaryDecodeRequest(t, patReq, patWire))
				require.Equal(t, int64(len(patWire)), patReq.ContentLength)
			})
		}
	}
}

func TestAlphaSearchAuxiliaryPATForwardCredentialGateAndBilling(t *testing.T) {
	for _, sourceEnabled := range []bool{false, true} {
		name := "source-disabled"
		if sourceEnabled {
			name = "source-enabled"
		}
		t.Run(name, func(t *testing.T) {
			parent := codexIdentityAccount()
			parent.Credentials["access_token"] = "at-fixture"
			parent.Credentials["auth_mode"] = OpenAIAuthModePersonalAccessToken
			parent.Extra[codexFingerprintConvergenceExtraKey] = sourceEnabled
			child := codexIdentityAccount()
			child.ID++
			child.ParentAccountID = &parent.ID
			child.Credentials["access_token"] = "at-fixture"
			child.Credentials["auth_mode"] = OpenAIAuthModePersonalAccessToken
			child.Extra[codexFingerprintConvergenceExtraKey] = !sourceEnabled
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}},
				Body: io.NopCloser(strings.NewReader(alphaSearchResponsesSSE("search result"))),
			}}
			svc := &OpenAIGatewayService{
				cfg: &config.Config{}, httpUpstream: upstream,
				accountRepo: &stubQuotaAccountRepo{accounts: map[int64]*Account{parent.ID: parent}},
			}
			alpha := []byte(`{"id":"session","model":"gpt-5.4","commands":{"search_query":[{"q":"fixture"}]}}`)
			c := codexIdentityHTTPContext("/v1/alpha/search", nil)
			result, err := svc.ForwardAlphaSearch(context.Background(), c, child, alpha)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, 1, result.WebSearchCalls)
			require.Equal(t, "/v1/responses", result.UpstreamEndpoint)
			require.Equal(t, chatgptCodexURL, upstream.lastReq.URL.String())
			if sourceEnabled {
				require.Equal(t, "zstd", upstream.lastReq.Header.Get("Content-Encoding"))
			} else {
				require.Empty(t, upstream.lastReq.Header.Get("Content-Encoding"))
			}
			wire := auxiliaryDecodeRequest(t, upstream.lastReq, upstream.lastBody)
			require.Equal(t, "gpt-5.4", gjson.GetBytes(wire, "model").String())
			require.Equal(t, "web_search", gjson.GetBytes(wire, "tools.0.type").String())
			require.Contains(t, gjson.GetBytes(wire, "input.0.content.0.text").String(), `"q":"fixture"`)
		})
	}
}

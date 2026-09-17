package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	openaipkg "github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// These regressions retain the original read-only audit's observable contracts.
func TestAuditHTTPCompatibilityHeaders(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		name := "forward"
		if passthrough {
			name = "passthrough"
		}
		t.Run(name, func(t *testing.T) {
			headers := http.Header{}
			headers.Set("x-openai-memgen-request", "true")
			headers.Set("x-responsesapi-include-timing-metrics", "true")
			got, _ := codexIdentityForward(t, codexIdentityAccount(), "/v1/responses", headers,
				[]byte(`{"model":"gpt-5.4","stream":true,"input":"hello"}`), passthrough)
			for _, key := range []string{"x-openai-memgen-request", "x-responsesapi-include-timing-metrics"} {
				require.Equal(t, "true", got.Get(key), "explicit compatibility header %s", key)
			}
		})
	}
}

func TestAuditUnknownHTTPStateWithLiveForeignSeed(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, expired := range []bool{false, true} {
			name := "forward/unknown"
			if passthrough {
				name = "passthrough/unknown"
			}
			if expired {
				name += "-expired"
			}
			t.Run(name, func(t *testing.T) {
				svc := &OpenAIGatewayService{}
				accountA := newTestOAuthAccount(9911, nil)
				accountA.Credentials = map[string]any{"chatgpt_account_id": "upstream-a"}
				accountB := newTestOAuthAccount(9912, nil)
				accountB.Credentials = map[string]any{"chatgpt_account_id": "upstream-b"}
				c, _ := newTurnStateTestContext(t, 9913, "same-session")
				minted := http.Header{}
				minted.Set(openAICodexTurnStateHeader, "known-state-a")
				svc.relayOpenAICodexTurnState(c, accountA, minted)
				state := "unknown-state-b"
				if expired {
					svc.openaiCodexTurnStateOrigins.Store(openAICodexTurnStateKey(state),
						openAICodexTurnStateOrigin{
							owner:     openAICodexTurnStateOwner(c, accountB),
							expiresAt: time.Now().Add(-time.Minute),
						})
				}
				c.Request.Header.Set(openAICodexTurnStateHeader, state)
				var request *http.Request
				var err error
				body := []byte(`{"model":"gpt-5.4","input":"hello"}`)
				if passthrough {
					request, err = svc.buildUpstreamRequestOpenAIPassthrough(
						context.Background(), c, accountB, body, "test-token")
				} else {
					request, err = svc.buildUpstreamRequest(
						context.Background(), c, accountB, body, "test-token", true, "", true)
				}
				require.NoError(t, err)
				defer request.Body.Close()
				require.Equal(t, state, request.Header.Get(openAICodexTurnStateHeader),
					"an unrelated live session owner cannot prove ownership of an unknown blob")
			})
		}
	}
}

func TestAuditDefaultUATrailer(t *testing.T) {
	version := "0.153.4"
	require.True(t, strings.HasSuffix(buildCodexCLIUserAgent(version),
		" ("+openaipkg.CodexDefaultOriginator+"; "+version+")"))
	require.True(t, strings.HasSuffix(buildCodexCLIUserAgent(""),
		" ("+openaipkg.CodexDefaultOriginator+"; "+codexCLIVersion+")"))
}

func auditDecodeWire(t *testing.T, recorder *httpUpstreamRecorder) []byte {
	t.Helper()
	require.NotNil(t, recorder.lastReq)
	if recorder.lastReq.Header.Get("Content-Encoding") != "zstd" {
		return recorder.lastBody
	}
	decoder, err := zstd.NewReader(nil)
	require.NoError(t, err)
	defer decoder.Close()
	body, err := decoder.DecodeAll(recorder.lastBody, nil)
	require.NoError(t, err)
	return body
}

func TestAuditImagesDeviceWire(t *testing.T) {
	body := []byte(`{"model":"gpt-image-2","prompt":"draw a square","n":1}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("api_key", &APIKey{ID: 9921})
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1},\"output\":[{\"type\":\"image_generation_call\",\"result\":\"aW1hZ2U=\",\"output_format\":\"png\"}]}}\n\n")),
	}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	_, err = svc.ForwardImages(withOpenAIImagesForceResponses(context.Background()), c, codexIdentityAccount(), body, parsed, "")
	require.NoError(t, err)
	wire := auditDecodeWire(t, upstream)
	require.NotEmpty(t, gjson.GetBytes(wire, "client_metadata.x-codex-installation-id").String())
	require.Empty(t, upstream.lastReq.Header.Get("OpenAI-Beta"))
}

func TestAuditAlphaSearchDeviceIdentity(t *testing.T) {
	body := []byte(`{"id":"search-session","model":"gpt-5.4","commands":{"search_query":[{"q":"fixture"}]}}`)
	headers := http.Header{}
	headers.Set("X-Codex-Turn-Metadata",
		`{"session_id":"search-session","thread_id":"search-session","turn_id":"turn","installation_id":"device","codex_version":"0.0.1","model":"wrong-model"}`)
	c := codexIdentityHTTPContext("/v1/alpha/search", headers)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"output":"fixture"}`)),
	}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	_, err := svc.ForwardAlphaSearch(context.Background(), c, codexIdentityAccount(), body)
	require.NoError(t, err)
	wire := auditDecodeWire(t, upstream)
	metadata := gjson.Parse(upstream.lastReq.Header.Get("X-Codex-Turn-Metadata"))
	require.Equal(t, gjson.GetBytes(wire, "id").String(), metadata.Get("session_id").String())
	require.False(t, metadata.Get("installation_id").Exists())
	require.Equal(t, upstream.lastReq.Header.Get("Version"), metadata.Get("codex_version").String())
	require.Equal(t, gjson.GetBytes(wire, "model").String(), metadata.Get("model").String())
}

func TestAuditPATSearchDeviceCompression(t *testing.T) {
	alpha := []byte(`{"id":"search-session","model":"gpt-5.4","commands":{"search_query":[{"q":"fixture"}]}}`)
	body, err := buildOpenAIAlphaSearchResponsesWebSearchBody(alpha, "gpt-5.4")
	require.NoError(t, err)
	c := codexIdentityHTTPContext("/v1/alpha/search", nil)
	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	req, err := svc.buildOpenAIAlphaSearchResponsesWebSearchRequest(
		context.Background(), c, codexIdentityAccount(), alpha, body, "at-test-token")
	require.NoError(t, err)
	defer req.Body.Close()
	require.Equal(t, "zstd", req.Header.Get("Content-Encoding"))
}

//go:build unit

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

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestSourceSyncDeepSeekCompactPreservesRewriteAndRequestTiming(t *testing.T) {
	body := []byte(`{"model":"deepseek-reasoner","stream":true,"input":[{"type":"message","role":"user","content":"hello"},{"type":"compaction_trigger"}]}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	MarkOpenAINativeCompactionV2(c)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"chatcmpl_compact","choices":[{"message":{"role":"assistant","content":"conversation summary"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`)),
	}}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	startTime := time.Now().Add(-time.Minute)

	result, err := svc.forwardResponsesViaRawChatCompletions(context.Background(), c, forceChatResponsesFallbackAccount(), body, startTime)
	require.NoError(t, err)
	require.GreaterOrEqual(t, result.Duration, time.Minute)
	require.False(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
	require.NotContains(t, string(upstream.lastBody), "compaction_trigger")
	require.Contains(t, string(upstream.lastBody), "summary of the conversation")
	require.Contains(t, rec.Body.String(), `"type":"compaction"`)
	require.Contains(t, rec.Body.String(), "conversation summary")
	require.Contains(t, rec.Body.String(), "event: response.completed")
	require.Len(t, upstream.requests, 1)
}

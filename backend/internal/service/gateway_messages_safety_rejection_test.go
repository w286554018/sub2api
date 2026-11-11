//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestForwardMessagesSafetyRejectionNeverFailsOver(t *testing.T) {
	const refusal = `{"type":"error","error":{"type":"permission_error","code":"cyber_policy","message":"blocked"}}`
	for _, test := range []struct {
		name       string
		status     int
		body       string
		wantStatus int
		stream     bool
	}{
		{"http403", http.StatusForbidden, refusal, http.StatusForbidden, true},
		{"http400", http.StatusBadRequest, refusal, http.StatusForbidden, false},
		{"sse_before_output", http.StatusOK, "event: error\ndata: " + refusal + "\n\n", http.StatusForbidden, true},
		{"sse_after_output", http.StatusOK, compatMessageStartEvent + compatTextBlockEvents + "event: error\ndata: " + refusal + "\n\n" + compatFinishEvents, http.StatusOK, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			stream := "false"
			if test.stream {
				stream = "true"
			}
			body := []byte(`{"model":"claude-sonnet-4-6","stream":` + stream + `,"messages":[{"role":"user","content":"hi"}]}`)
			parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
			require.NoError(t, err)
			upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
				StatusCode: test.status,
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader(test.body)),
			}}
			svc := newForwardPartialUsageServiceForTest(upstream)
			queued := &queuedHTTPUpstream{responses: []*http.Response{upstream.resp}}
			svc.httpUpstream = queued
			result, err := svc.Forward(context.Background(), c, newAnthropicOAuthAccountForPartialUsageTest(), parsed)
			require.ErrorContains(t, err, "upstream safety rejection")
			var failover *UpstreamFailoverError
			var sseErr *sseStreamErrorEventError
			require.NotErrorAs(t, err, &failover)
			require.NotErrorAs(t, err, &sseErr)
			require.Nil(t, result)
			require.Len(t, queued.requests, 1, "safety rejections must not retry the upstream")
			require.True(t, IsResponseCommitted(c))
			require.Equal(t, test.wantStatus, rec.Code)
			require.Contains(t, rec.Body.String(), `"type":"permission_error"`)
			require.NotContains(t, rec.Body.String(), "end_turn", "queued success events must not follow the rejection")
		})
	}
}

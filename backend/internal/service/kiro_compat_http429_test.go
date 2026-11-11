//go:build unit

package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestKiroCompatHTTP429PreservesExponentialCooldown(t *testing.T) {
	for _, endpoint := range []string{"chat_completions", "responses"} {
		t.Run(endpoint, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			repo := &kiroQuotaRecordingRepo{}
			cooldown := &stubKiroCooldownStore{mark429TTL: 3 * time.Minute}
			upstream := &queuedHTTPUpstream{responses: []*http.Response{
				newJSONResponse(http.StatusTooManyRequests, `{"message":"Rate exceeded"}`),
			}}
			svc := &GatewayService{
				accountRepo:         repo,
				httpUpstream:        upstream,
				kiroCooldownStore:   cooldown,
				tlsFPProfileService: &TLSFingerprintProfileService{},
				cfg:                 &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}},
				rateLimitService:    &RateLimitService{accountRepo: repo},
			}
			account := &Account{
				ID: 32, Name: "kiro-compat-429", Platform: PlatformKiro, Type: AccountTypeOAuth,
				Status: StatusActive, Schedulable: true, Concurrency: 1,
				Credentials: map[string]any{
					"access_token": "kiro-access-token",
					"profile_arn":  "arn:aws:codewhisperer:us-east-1:000000000000:profile/TEST",
				},
			}
			path := "/v1/chat/completions"
			body := `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}]}`
			if endpoint == "responses" {
				path = "/v1/responses"
				body = `{"model":"claude-sonnet-4-6","input":[{"role":"user","content":"hi"}]}`
			}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			parsed := &ParsedRequest{Group: &Group{Platform: PlatformKiro, KiroEndpointMode: KiroEndpointModeKRS}}
			before := time.Now()
			var result *ForwardResult
			var err error
			if endpoint == "chat_completions" {
				result, err = svc.ForwardAsChatCompletions(context.Background(), c, account, []byte(body), parsed)
			} else {
				result, err = svc.ForwardAsResponses(context.Background(), c, account, []byte(body), parsed)
			}
			var failoverErr *UpstreamFailoverError
			require.ErrorAs(t, err, &failoverErr)
			require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
			require.Nil(t, result)
			require.Len(t, upstream.requests, 1)
			require.Len(t, cooldown.mark429Keys, 1)
			require.Len(t, repo.rateLimitedUntil, 1, "generic 429 handling must not overwrite the Kiro cooldown")
			require.True(t, repo.rateLimitedUntil[0].After(before.Add(2*time.Minute)))
			require.Empty(t, repo.setErrorCalls)
			require.Zero(t, rec.Body.Len(), "failover must leave the response unwritten")
		})
	}
}

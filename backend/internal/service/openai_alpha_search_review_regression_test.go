package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const alphaSearchReviewCompletedSSE = `data: {"type":"response.completed","response":{"id":"resp_search","object":"response","status":"completed","error":null,"incomplete_details":null,"output":[{"type":"web_search_call","id":"ws_search","status":"completed"},{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"search result","annotations":[{"type":"url_citation","url":"https://example.com/news","title":"Example News"}]}]}]}}` + "\n\n"

func TestAlphaSearchReviewPATRequiresSuccessfulCompletion(t *testing.T) {
	gin.SetMode(gin.TestMode)
	delta := "data: " + `{"type":"response.output_text.delta","delta":"partial result"}` + "\n\n"
	failed := "data: " + `{"type":"response.failed","response":{"status":"failed","error":{"message":"fixture failure"}}}` + "\n\n"
	for _, tc := range []struct {
		name string
		body string
	}{
		{"failed", failed},
		{"partial_then_failed", delta + failed},
		{"error", "data: " + `{"type":"error","error":{"message":"fixture failure"}}` + "\n\n"},
		{"incomplete", "data: " + `{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}` + "\n\n"},
		{"cancelled", "data: " + `{"type":"response.cancelled","response":{"status":"cancelled"}}` + "\n\n"},
		{"empty", ""},
		{"eof_before_completion", delta},
		{"done_without_completion", delta + "data: [DONE]\n\n"},
		{"malformed_json", "data: {broken}\n\n"},
		{"malformed_before_completion", "data: {broken}\n\n" + alphaSearchReviewCompletedSSE},
		{"malformed_after_completion", alphaSearchReviewCompletedSSE + "data: {broken}\n\n"},
		{"null_event", "data: null\n\n" + alphaSearchReviewCompletedSSE},
		{"missing_type", "data: {}\n\n" + alphaSearchReviewCompletedSSE},
		{"missing_response", "data: " + `{"type":"response.completed"}` + "\n\n"},
		{"null_response", "data: " + `{"type":"response.completed","response":null}` + "\n\n"},
		{"scalar_response", "data: " + `{"type":"response.completed","response":"completed"}` + "\n\n"},
		{"missing_status", "data: " + `{"type":"response.completed","response":{"output":[]}}` + "\n\n"},
		{"failed_status", "data: " + `{"type":"response.completed","response":{"status":"failed","output":[]}}` + "\n\n"},
		{"incomplete_status", "data: " + `{"type":"response.completed","response":{"status":"incomplete","output":[]}}` + "\n\n"},
		{"completed_with_error", "data: " + `{"type":"response.completed","response":{"status":"completed","error":{"message":"fixture failure"},"output":[]}}` + "\n\n"},
		{"completed_with_incomplete_details", "data: " + `{"type":"response.completed","response":{"status":"completed","incomplete_details":{"reason":"max_output_tokens"},"output":[]}}` + "\n\n"},
		{"failed_then_completed", failed + alphaSearchReviewCompletedSSE},
		{"completed_then_failed", alphaSearchReviewCompletedSSE + failed},
		{"completion_frame_truncated", strings.TrimSuffix(alphaSearchReviewCompletedSSE, "\n\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := codexIdentityAccount()
			account.Credentials["access_token"] = "at-review-fixture"
			account.Credentials["auth_mode"] = OpenAIAuthModePersonalAccessToken
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/alpha/search", nil)
			responseBody := &passthroughCloseTrackingReadCloser{Reader: strings.NewReader(tc.body)}
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}},
				Body: responseBody,
			}}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}

			result, err := svc.ForwardAlphaSearch(context.Background(), c, account,
				[]byte(`{"model":"gpt-5.4","commands":{"search_query":[{"q":"fixture"}]}}`))

			assert.Error(t, err, "an unsuccessful Responses stream must not reach the billing handoff")
			assert.Nil(t, result)
			assert.False(t, c.Writer.Written(), "leave the handler able to send an upstream error")
			assert.Empty(t, recorder.Body.String(), "partial results must not escape as success")
			require.True(t, responseBody.closed)
			require.Equal(t, chatgptCodexURL, upstream.lastReq.URL.String())
		})
	}
}

func TestAlphaSearchReviewPATCompletedResponseBillsOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	deltas := "data: " + `{"type":"response.output_text.delta","delta":"search "}` + "\n\n" +
		"data: " + `{"type":"response.output_text.delta","delta":"result"}` + "\n\n" +
		"data: " + `{"type":"response.output_text.annotation.added","annotation":{"type":"url_citation","url":"https://example.com/news","title":"Example News"}}` + "\n\n"
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"completed_only", alphaSearchReviewCompletedSSE, `{"output":"search result","results":[{"type":"text_result","ref_id":"turn0search0","url":"https://example.com/news","title":"Example News"}]}`},
		{"deltas_and_done", deltas + alphaSearchReviewCompletedSSE + "data: [DONE]\n\n", `{"output":"search result","results":[{"type":"text_result","ref_id":"turn0search0","url":"https://example.com/news","title":"Example News"}]}`},
		{"crlf_and_comments", strings.ReplaceAll(": keepalive\n\n"+alphaSearchReviewCompletedSSE, "\n", "\r\n"), `{"output":"search result","results":[{"type":"text_result","ref_id":"turn0search0","url":"https://example.com/news","title":"Example News"}]}`},
		{"empty_completed_output", "data: " + `{"type":"response.completed","response":{"status":"completed","error":null,"incomplete_details":null,"output":[]}}` + "\n\n", `{"output":""}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := codexIdentityAccount()
			account.Credentials["access_token"] = "at-review-fixture"
			account.Credentials["auth_mode"] = OpenAIAuthModePersonalAccessToken
			account.Credentials["model_mapping"] = map[string]any{"search-model": "gpt-5.4"}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/alpha/search", nil)
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {"req-review"}},
				Body:       io.NopCloser(strings.NewReader(tc.body)),
			}}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}

			result, err := svc.ForwardAlphaSearch(context.Background(), c, account,
				[]byte(`{"model":"search-model","commands":{"search_query":[{"q":"fixture"}]}}`))

			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, 1, result.WebSearchCalls)
			require.Equal(t, "search-model", result.Model)
			require.Equal(t, "gpt-5.4", result.UpstreamModel)
			require.Equal(t, "/v1/responses", result.UpstreamEndpoint)
			require.Equal(t, "req-review", result.RequestID)
			require.Equal(t, http.StatusOK, recorder.Code)
			require.JSONEq(t, tc.want, recorder.Body.String())
			require.Equal(t, "zstd", upstream.lastReq.Header.Get("Content-Encoding"))
		})
	}
}

type alphaSearchReviewAccountRepo struct {
	stubQuotaAccountRepo
	lookupIDs []int64
	updateIDs []int64
}

func (r *alphaSearchReviewAccountRepo) GetByID(ctx context.Context, id int64) (*Account, error) {
	r.lookupIDs = append(r.lookupIDs, id)
	return r.stubQuotaAccountRepo.GetByID(ctx, id)
}

func (r *alphaSearchReviewAccountRepo) UpdateCredentials(ctx context.Context, id int64, credentials map[string]any) error {
	r.updateIDs = append(r.updateIDs, id)
	return r.stubQuotaAccountRepo.UpdateCredentials(ctx, id, credentials)
}

type alphaSearchReviewUpstream struct {
	httpUpstreamRecorder
	accountIDs    []int64
	concurrencies []int
}

func (u *alphaSearchReviewUpstream) Do(req *http.Request, proxyURL string, accountID int64, concurrency int) (*http.Response, error) {
	u.accountIDs = append(u.accountIDs, accountID)
	u.concurrencies = append(u.concurrencies, concurrency)
	return u.httpUpstreamRecorder.Do(req, proxyURL, accountID, concurrency)
}

func TestAlphaSearchReviewPATShadowUsesCredentialOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, sourceEnabled := range []bool{false, true} {
		name := "source_disabled"
		if sourceEnabled {
			name = "source_enabled"
		}
		t.Run(name, func(t *testing.T) {
			parent := codexIdentityAccount()
			parent.Credentials["access_token"] = "at-parent-fixture"
			parent.Credentials["auth_mode"] = OpenAIAuthModePersonalAccessToken
			parent.Credentials["model_mapping"] = map[string]any{"search-model": "gpt-5.5"}
			parent.Extra[codexFingerprintModeExtraKey] = "off"
			parent.Extra[codexFingerprintConvergenceExtraKey] = sourceEnabled
			child := codexIdentityAccount()
			child.ID++
			child.Concurrency = 7
			child.ParentAccountID = &parent.ID
			child.Credentials = map[string]any{"model_mapping": map[string]any{"search-model": "gpt-5.4"}}
			child.Extra[codexFingerprintConvergenceExtraKey] = !sourceEnabled
			repo := &alphaSearchReviewAccountRepo{stubQuotaAccountRepo: stubQuotaAccountRepo{
				accounts: map[int64]*Account{parent.ID: parent},
			}}
			upstream := &alphaSearchReviewUpstream{httpUpstreamRecorder: httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}},
				Body: io.NopCloser(strings.NewReader(alphaSearchReviewCompletedSSE)),
			}}}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, accountRepo: repo, httpUpstream: upstream}
			c := codexIdentityHTTPContext("/v1/alpha/search", nil)

			result, err := svc.ForwardAlphaSearch(context.Background(), c, child,
				[]byte(`{"id":"search-session","model":"search-model","commands":{"search_query":[{"q":"fixture"}]}}`))

			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, chatgptCodexURL, upstream.lastReq.URL.String())
			require.Equal(t, "/v1/responses", result.UpstreamEndpoint)
			require.Equal(t, 1, result.WebSearchCalls)
			require.Equal(t, "search-model", result.Model)
			require.Equal(t, "gpt-5.4", result.UpstreamModel)
			require.Equal(t, []int64{child.ID}, upstream.accountIDs)
			require.Equal(t, []int{7}, upstream.concurrencies)
			require.Equal(t, "Bearer at-parent-fixture", upstream.lastReq.Header.Get("Authorization"))
			require.Equal(t, "identity-upstream", upstream.lastReq.Header.Get("ChatGPT-Account-ID"))
			require.Equal(t, []int64{parent.ID}, repo.lookupIDs, "reuse the prepared credential source")
			require.Empty(t, repo.updateIDs)
			require.Equal(t, map[string]any{"model_mapping": map[string]any{"search-model": "gpt-5.4"}}, child.Credentials)
			if sourceEnabled {
				require.Equal(t, "zstd", upstream.lastReq.Header.Get("Content-Encoding"))
			} else {
				require.Empty(t, upstream.lastReq.Header.Get("Content-Encoding"))
			}
			wire := auxiliaryDecodeRequest(t, upstream.lastReq, upstream.lastBody)
			require.Equal(t, "gpt-5.4", gjson.GetBytes(wire, "model").String())
			require.Equal(t, "web_search", gjson.GetBytes(wire, "tools.0.type").String())
		})
	}
}

func TestAlphaSearchReviewAuthModeRefreshesEachAttempt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, firstPAT := range []bool{false, true} {
		name := "oauth_to_pat"
		if firstPAT {
			name = "pat_to_oauth"
		}
		t.Run(name, func(t *testing.T) {
			repo := &alphaSearchReviewAccountRepo{stubQuotaAccountRepo: stubQuotaAccountRepo{accounts: map[int64]*Account{}}}
			upstream := &alphaSearchReviewUpstream{}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, accountRepo: repo, httpUpstream: upstream}
			c := codexIdentityHTTPContext("/v1/alpha/search", nil)
			for attempt, pat := range []bool{firstPAT, !firstPAT} {
				parent := codexIdentityAccount()
				parent.ID += int64(attempt * 10)
				parent.Credentials["access_token"] = "owner-oauth-fixture"
				parent.Credentials["chatgpt_account_id"] = name + "-" + string(rune('a'+attempt))
				parent.Extra[codexFingerprintConvergenceExtraKey] = pat
				child := codexIdentityAccount()
				child.ID = parent.ID + 1
				child.ParentAccountID = &parent.ID
				child.Credentials = map[string]any{"model_mapping": map[string]any{"search-model": "gpt-5.4"}}
				child.Extra[codexFingerprintConvergenceExtraKey] = !pat
				wantURL, wantToken := chatgptCodexAlphaSearchURL, "Bearer owner-oauth-fixture"
				responseBody := `{"output":"standalone result"}`
				if pat {
					parent.Credentials["access_token"] = "at-owner-fixture"
					parent.Credentials["auth_mode"] = OpenAIAuthModePersonalAccessToken
					wantURL, wantToken = chatgptCodexURL, "Bearer at-owner-fixture"
					responseBody = alphaSearchReviewCompletedSSE
				} else {
					// Stale child auth metadata must not override the current owner.
					child.Credentials["auth_mode"] = OpenAIAuthModePersonalAccessToken
				}
				repo.accounts[parent.ID] = parent
				upstream.resp = &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(responseBody))}
				if attempt == 0 {
					upstream.resp.StatusCode = http.StatusUnauthorized
					upstream.resp.Body = io.NopCloser(strings.NewReader(`{"error":{"message":"fixture denied"}}`))
				}

				result, err := svc.ForwardAlphaSearch(context.Background(), c, child,
					[]byte(`{"id":"shared-search-session","model":"search-model","commands":{"search_query":[{"q":"fixture"}]}}`))

				if attempt == 0 {
					var failover *UpstreamFailoverError
					require.ErrorAs(t, err, &failover)
					require.Equal(t, http.StatusUnauthorized, failover.StatusCode)
					require.Nil(t, result)
					require.False(t, c.Writer.Written())
				} else {
					require.NoError(t, err)
					require.NotNil(t, result)
					require.Equal(t, 1, result.WebSearchCalls)
					require.Equal(t, "search-model", result.Model)
					require.Equal(t, "gpt-5.4", result.UpstreamModel)
				}
				require.Equal(t, wantURL, upstream.lastReq.URL.String())
				require.Equal(t, wantToken, upstream.lastReq.Header.Get("Authorization"))
				require.Equal(t, parent.GetChatGPTAccountID(), upstream.lastReq.Header.Get("ChatGPT-Account-ID"))
				require.Equal(t, child.ID, upstream.accountIDs[attempt])
				require.Len(t, repo.lookupIDs, attempt+1)
				if pat {
					require.Equal(t, "zstd", upstream.lastReq.Header.Get("Content-Encoding"))
				} else {
					require.Empty(t, upstream.lastReq.Header.Get("Content-Encoding"))
				}
			}
		})
	}
}

func TestAlphaSearchReviewPATShadowBackfillsOwnerMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var whoamiCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		whoamiCalls.Add(1)
		assert.Equal(t, "Bearer at-parent-fixture", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"email":"fixture@example.com","chatgpt_user_id":"user-parent","chatgpt_account_id":"acct-parent","chatgpt_plan_type":"plus","chatgpt_account_is_fedramp":true}`)
	}))
	defer server.Close()
	oldURL := openAICodexPATWhoamiURL
	openAICodexPATWhoamiURL = server.URL
	t.Cleanup(func() { openAICodexPATWhoamiURL = oldURL })
	parent := codexIdentityAccount()
	parent.Credentials = map[string]any{"access_token": "at-parent-fixture", "auth_mode": OpenAIAuthModePersonalAccessToken}
	child := codexIdentityAccount()
	child.ID++
	child.ParentAccountID = &parent.ID
	child.Credentials = map[string]any{"model_mapping": map[string]any{"search-model": "gpt-5.4"}}
	repo := &alphaSearchReviewAccountRepo{stubQuotaAccountRepo: stubQuotaAccountRepo{accounts: map[int64]*Account{parent.ID: parent}}}
	upstream := &alphaSearchReviewUpstream{httpUpstreamRecorder: httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(alphaSearchReviewCompletedSSE)),
	}}}
	svc := &OpenAIGatewayService{
		cfg: &config.Config{}, accountRepo: repo, httpUpstream: upstream,
		openAITokenProvider: NewOpenAITokenProvider(nil, nil, NewOpenAIOAuthService(nil, nil)),
	}
	c := codexIdentityHTTPContext("/v1/alpha/search", nil)

	result, err := svc.ForwardAlphaSearch(context.Background(), c, child,
		[]byte(`{"model":"search-model","commands":{"search_query":[{"q":"fixture"}]}}`))

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, int32(1), whoamiCalls.Load())
	require.Equal(t, []int64{parent.ID}, repo.lookupIDs)
	require.Equal(t, []int64{parent.ID}, repo.updateIDs)
	require.Equal(t, "acct-parent", parent.GetChatGPTAccountID())
	require.Equal(t, "acct-parent", upstream.lastReq.Header.Get("ChatGPT-Account-ID"))
	require.Equal(t, "true", upstream.lastReq.Header.Get("X-OpenAI-Fedramp"))
	require.Equal(t, []int64{child.ID}, upstream.accountIDs)
	require.Equal(t, map[string]any{"model_mapping": map[string]any{"search-model": "gpt-5.4"}}, child.Credentials)
	require.Equal(t, "/v1/responses", result.UpstreamEndpoint)
	require.Equal(t, "gpt-5.4", result.UpstreamModel)
}

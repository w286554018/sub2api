//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const intelligentPromptRegressionText = "End with a separate line: ANSWER: 12"

func intelligentClaudeStreamResponse(text string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			`data: {"type":"content_block_delta","delta":{"text":"` + text + `"}}` + "\n\n" +
				`data: {"type":"message_stop"}` + "\n\n",
		)),
	}
}

func TestAccountTestService_ClaudeUsesConfiguredIntelligentPrompt(t *testing.T) {
	account := &Account{
		ID: 1401, Name: "claude-prompt", Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://anthropic.example"},
	}
	repo := &mockAccountRepoForGemini{accountsByID: map[int64]*Account{account.ID: account}}
	upstream := &httpUpstreamRecorder{resp: intelligentClaudeStreamResponse("ANSWER: 12")}
	svc := &AccountTestService{
		accountRepo: repo, httpUpstream: upstream,
		cfg:                 &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	c, _ := newTestContext()

	err := svc.TestAccountConnection(c, account.ID, "claude-sonnet-4-6", intelligentPromptRegressionText, AccountTestModeDefault)

	require.NoError(t, err)
	require.Equal(t, intelligentPromptRegressionText, gjson.GetBytes(upstream.lastBody, "messages.0.content.0.text").String())
	require.GreaterOrEqual(t, gjson.GetBytes(upstream.lastBody, "max_tokens").Int(), int64(1024))
}

func TestAccountTestPayloadBuildersKeepConnectivityFallbacks(t *testing.T) {
	claudePayload, err := createTestPayloadWithPrompt("claude-sonnet-4-6", "")
	require.NoError(t, err)
	claudeBody, err := json.Marshal(claudePayload)
	require.NoError(t, err)
	require.Equal(t, "hi", gjson.GetBytes(claudeBody, "messages.0.content.0.text").String())

	openAIPayload := createOpenAITestPayloadWithPrompt("gpt-5.4", true, "")
	openAIBody, err := json.Marshal(openAIPayload)
	require.NoError(t, err)
	require.Equal(t, "hi", gjson.GetBytes(openAIBody, "input.0.content.0.text").String())
	require.False(t, gjson.GetBytes(openAIBody, "store").Bool())
}

func TestAccountTestService_BedrockUsesConfiguredIntelligentPrompt(t *testing.T) {
	account := &Account{
		ID: 1402, Name: "bedrock-prompt", Platform: PlatformAnthropic, Type: AccountTypeBedrock, Concurrency: 1,
		Credentials: map[string]any{
			"auth_mode": "apikey", "api_key": "bedrock-test", "aws_region": "us-east-1",
		},
	}
	repo := &mockAccountRepoForGemini{accountsByID: map[int64]*Account{account.ID: account}}
	upstream := &httpUpstreamRecorder{resp: newJSONResponse(http.StatusOK, `{"content":[{"text":"ANSWER: 12"}]}`)}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
	c, _ := newTestContext()

	err := svc.TestAccountConnection(c, account.ID, "claude-sonnet-4-6", intelligentPromptRegressionText, AccountTestModeDefault)

	require.NoError(t, err)
	require.Equal(t, intelligentPromptRegressionText, gjson.GetBytes(upstream.lastBody, "messages.0.content.0.text").String())
	require.GreaterOrEqual(t, gjson.GetBytes(upstream.lastBody, "max_tokens").Int(), int64(1024))
}

func TestAccountTestService_KiroUsesConfiguredIntelligentPrompt(t *testing.T) {
	account := &Account{
		ID: 1403, Name: "kiro-prompt", Platform: PlatformKiro, Type: AccountTypeOAuth, Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "kiro-access-token",
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/PROMPTTEST",
		},
	}
	repo := &mockAccountRepoForGemini{accountsByID: map[int64]*Account{account.ID: account}}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{
		newJSONResponse(http.StatusUnauthorized, `{"message":"stop after capture"}`),
	}}
	svc := &AccountTestService{
		accountRepo: repo, kiroTokenProvider: NewKiroTokenProvider(nil, nil, nil),
		httpUpstream: upstream, tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	c, _ := newTestContext()

	err := svc.TestAccountConnection(c, account.ID, "claude-sonnet-4-6", intelligentPromptRegressionText, AccountTestModeDefault)

	require.Error(t, err)
	require.Len(t, upstream.requests, 1)
	body, readErr := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, readErr)
	require.Contains(t, string(body), intelligentPromptRegressionText)
}

func TestAccountTestService_OpenAIResponsesUsesConfiguredIntelligentPrompt(t *testing.T) {
	account := &Account{
		ID: 1404, Name: "openai-responses-prompt", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://compat-upstream.example/v1"},
		Extra:       map[string]any{openai_compat.ExtraKeyResponsesSupported: true},
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			`data: {"type":"response.output_text.delta","delta":"ANSWER: 12"}` + "\n\n" +
				`data: {"type":"response.completed"}` + "\n\n",
		)),
	}}
	svc := &AccountTestService{
		httpUpstream: upstream,
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
	}
	c, _ := newTestContext()

	err := svc.testOpenAIAccountConnection(c, account, "gpt-5.4", intelligentPromptRegressionText, AccountTestModeDefault)

	require.NoError(t, err)
	require.Equal(t, intelligentPromptRegressionText, gjson.GetBytes(upstream.lastBody, "input.0.content.0.text").String())
}

func TestAccountTestService_AntigravityUsesConfiguredPromptAndPracticalOutputLimit(t *testing.T) {
	for _, model := range []string{"gemini-3.1-pro-preview", "claude-sonnet-4-6"} {
		t.Run(model, func(t *testing.T) {
			account := &Account{
				ID: 1405, Name: "antigravity-prompt", Platform: PlatformAntigravity, Type: AccountTypeOAuth, Concurrency: 1,
				Credentials: map[string]any{
					"access_token": "antigravity-token", "project_id": "project-prompt-test",
					"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
				},
			}
			repo := &mockAccountRepoForGemini{accountsByID: map[int64]*Account{account.ID: account}}
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body: io.NopCloser(strings.NewReader(
					`data: {"response":{"candidates":[{"content":{"parts":[{"text":"ANSWER: 12"}]}}]}}` + "\n\n",
				)),
			}}
			gateway := &AntigravityGatewayService{
				tokenProvider:  NewAntigravityTokenProvider(repo, nil, nil),
				httpUpstream:   upstream,
				settingService: NewSettingService(&antigravitySettingRepoStub{}, &config.Config{}),
				accountRepo:    repo,
			}
			svc := &AccountTestService{
				accountRepo: repo, antigravityGatewayService: gateway,
				httpUpstream: upstream, tlsFPProfileService: &TLSFingerprintProfileService{},
			}
			c, _ := newTestContext()

			err := svc.TestAccountConnection(c, account.ID, model, intelligentPromptRegressionText, AccountTestModeDefault)

			require.NoError(t, err)
			require.Contains(t, string(upstream.lastBody), intelligentPromptRegressionText)
			require.Greater(t, gjson.GetBytes(upstream.lastBody, "request.generationConfig.maxOutputTokens").Int(), int64(1))
		})
	}
}

func TestAntigravityConnectivityProbeKeepsDotAndOneTokenFallback(t *testing.T) {
	svc := &AntigravityGatewayService{}
	for _, build := range []struct {
		name string
		fn   func() ([]byte, error)
	}{
		{name: "gemini", fn: func() ([]byte, error) {
			return svc.buildGeminiTestRequest("project-test", "gemini-pro-agent")
		}},
		{name: "claude", fn: func() ([]byte, error) {
			return svc.buildClaudeTestRequest("project-test", "claude-sonnet-4-6")
		}},
	} {
		t.Run(build.name, func(t *testing.T) {
			body, err := build.fn()
			require.NoError(t, err)
			require.Equal(t, ".", gjson.GetBytes(body, "request.contents.0.parts.0.text").String())
			require.Equal(t, int64(1), gjson.GetBytes(body, "request.generationConfig.maxOutputTokens").Int())
		})
	}
}

func TestAccountTestService_IntelligentSyntheticProbeIsNotSemanticallyEvaluated(t *testing.T) {
	account := &Account{
		ID: 1406, Name: "synthetic", Platform: PlatformAnthropic, Type: AccountTypeOAuth,
		Extra: map[string]any{"synthetic_ui_test": true},
	}
	repo := &mockAccountRepoForGemini{accountsByID: map[int64]*Account{account.ID: account}}
	svc := &AccountTestService{accountRepo: repo}
	record := &IntelligentTestRecord{
		AccountID: account.ID,
		ConfigSnapshot: &IntelligentTestConfig{
			Prompt: intelligentPromptRegressionText, Evaluator: "exact_answer", ExpectedAnswer: "12", TimeoutSeconds: 60,
		},
	}

	err := svc.RunIntelligentTest(context.Background(), record)

	require.Error(t, err)
	require.Equal(t, IntelligentTestStatusRequestError, record.Status)
	require.Equal(t, "not_evaluated", record.Evaluation["execution_status"])
	require.Equal(t, "synthetic", record.Evaluation["execution_reason"])
}

func TestAccountTestService_IntelligentImageProbeIsNotSemanticallyEvaluated(t *testing.T) {
	account := &Account{
		ID: 1407, Name: "gemini-image", Platform: PlatformGemini, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{"api_key": "gemini-test", "base_url": "https://generativelanguage.googleapis.com"},
	}
	repo := &mockAccountRepoForGemini{accountsByID: map[int64]*Account{account.ID: account}}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			`data: {"candidates":[{"content":{"parts":[{"text":"generated"},{"inlineData":{"mimeType":"image/png","data":"QUJD"}}]}}]}` + "\n\n" +
				`data: [DONE]` + "\n\n",
		)),
	}}
	svc := &AccountTestService{
		accountRepo: repo, httpUpstream: upstream,
		cfg:                 &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	record := &IntelligentTestRecord{
		AccountID: account.ID,
		Model:     "gemini-2.5-flash-image",
		ConfigSnapshot: &IntelligentTestConfig{
			Prompt: intelligentPromptRegressionText, Evaluator: "exact_answer", ExpectedAnswer: "12", TimeoutSeconds: 60,
		},
	}

	err := svc.RunIntelligentTest(context.Background(), record)

	require.Error(t, err)
	require.Equal(t, IntelligentTestStatusRequestError, record.Status)
	require.Equal(t, "not_evaluated", record.Evaluation["execution_status"])
	require.Equal(t, "image", record.Evaluation["execution_reason"])
}

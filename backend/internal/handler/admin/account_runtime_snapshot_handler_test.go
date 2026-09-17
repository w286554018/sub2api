package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountHandlerGetRuntimeSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	updatedAt := time.Date(2026, 9, 17, 9, 0, 0, 0, time.UTC)
	account := &service.Account{
		ID:          88,
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeOAuth,
		Concurrency: 2,
		UpdatedAt:   updatedAt,
		Credentials: map[string]any{"access_token": "secret-token"},
		Extra: map[string]any{
			"openai_oauth_responses_websockets_v2_mode": service.OpenAIWSIngressModeCtxPool,
			"openai_passthrough":                        true,
		},
	}
	svc := newStubAdminService()
	svc.getAccountResult = account
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	handler := NewAccountHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	handler.SetOpenAIAccountRuntimeSnapshotService(service.NewOpenAIAccountRuntimeSnapshotService(svc, service.NewOpenAIWSProtocolResolver(cfg), nil))
	r := gin.New()
	r.GET("/accounts/:id/runtime", handler.GetRuntime)

	res := httptest.NewRecorder()
	r.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/accounts/88/runtime", nil))
	require.Equal(t, http.StatusOK, res.Code, res.Body.String())
	require.NotContains(t, res.Body.String(), "secret-token")

	var payload struct {
		Data service.OpenAIAccountRuntimeSnapshot `json:"data"`
	}
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &payload))
	require.Equal(t, int64(88), payload.Data.AccountID)
	require.Equal(t, "computed", payload.Data.Source)
	require.False(t, payload.Data.Observed)
	require.Equal(t, "oauth", payload.Data.AuthType)
	require.Equal(t, service.OpenAIWSIngressModeCtxPool, payload.Data.Configured.WebSocketMode)
	require.Equal(t, string(service.OpenAIUpstreamTransportResponsesWebsocketV2), payload.Data.Effective.Transport)
	require.True(t, payload.Data.Effective.Passthrough)
}

func TestAccountHandlerGetRuntimeRejectsInvalidAndUnsupported(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name       string
		path       string
		account    *service.Account
		wantStatus int
		wantReason string
	}{
		{name: "invalid id", path: "/accounts/bad/runtime", wantStatus: http.StatusBadRequest, wantReason: "Invalid account ID"},
		{name: "non-positive id", path: "/accounts/0/runtime", wantStatus: http.StatusBadRequest, wantReason: "Invalid account ID"},
		{name: "unsupported", path: "/accounts/9/runtime", account: &service.Account{ID: 9, Platform: service.PlatformAnthropic, Type: service.AccountTypeOAuth}, wantStatus: http.StatusBadRequest, wantReason: "OPENAI_ACCOUNT_RUNTIME_UNSUPPORTED"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newStubAdminService()
			svc.getAccountResult = tt.account
			handler := NewAccountHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			handler.SetOpenAIAccountRuntimeSnapshotService(service.NewOpenAIAccountRuntimeSnapshotService(svc, nil, nil))
			r := gin.New()
			r.GET("/accounts/:id/runtime", handler.GetRuntime)

			res := httptest.NewRecorder()
			r.ServeHTTP(res, httptest.NewRequest(http.MethodGet, tt.path, nil))
			require.Equal(t, tt.wantStatus, res.Code, res.Body.String())
			if tt.wantReason != "" {
				require.Contains(t, res.Body.String(), tt.wantReason)
			}
		})
	}
}

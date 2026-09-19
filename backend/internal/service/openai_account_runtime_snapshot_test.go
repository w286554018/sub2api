package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type runtimeSnapshotAccountReader struct {
	account *Account
	err     error
}

func (r runtimeSnapshotAccountReader) GetAccount(context.Context, int64) (*Account, error) {
	return r.account, r.err
}

type runtimeSnapshotPluginRouter bool

func (r runtimeSnapshotPluginRouter) ShouldRouteOpenAIOAuth(*Account) bool { return bool(r) }

func TestOpenAIAccountRuntimeSnapshotServiceGetOpenAIOAuth(t *testing.T) {
	proxyID := int64(42)
	loadFactor := 7
	updatedAt := time.Date(2026, 9, 17, 8, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	account := &Account{
		ID:          10,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 3,
		LoadFactor:  &loadFactor,
		ProxyID:     &proxyID,
		UpdatedAt:   updatedAt,
		Credentials: map[string]any{
			"access_token":  "secret-access-token",
			"refresh_token": "secret-refresh-token",
		},
		Extra: map[string]any{
			"openai_passthrough":                        true,
			"openai_oauth_responses_websockets_v2_mode": "passthrough",
			codexFingerprintModeExtraKey:                "device",
			codexFingerprintConvergenceExtraKey:         true,
			codexFingerprintSeedExtraKey:                "11111111-1111-4111-8111-111111111111",
			"openai_device_id":                          "device-secret",
		},
	}
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.IngressModeDefault = OpenAIWSIngressModeCtxPool

	svc := NewOpenAIAccountRuntimeSnapshotService(runtimeSnapshotAccountReader{account: account}, NewOpenAIWSProtocolResolver(cfg), nil)
	snapshot, err := svc.Get(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, "computed", snapshot.Source)
	require.False(t, snapshot.Observed)
	require.Equal(t, int64(10), snapshot.AccountID)
	require.Equal(t, "oauth", snapshot.AuthType)
	require.Equal(t, updatedAt.UTC(), snapshot.AccountRevision)
	require.True(t, snapshot.Configured.Passthrough)
	require.Equal(t, OpenAIWSIngressModePassthrough, snapshot.Configured.WebSocketMode)
	require.Equal(t, 3, snapshot.Configured.Concurrency)
	require.Equal(t, &loadFactor, snapshot.Configured.LoadFactor)
	require.Equal(t, &proxyID, snapshot.Configured.ProxyID)
	require.Equal(t, string(OpenAIUpstreamTransportResponsesWebsocketV2), snapshot.Effective.Transport)
	require.Equal(t, "ws_v2_mode_passthrough", snapshot.Effective.TransportReason)
	require.False(t, snapshot.Effective.PluginRouted)
	require.Equal(t, "none", snapshot.Effective.PluginMode)
	require.Equal(t, "device", snapshot.Effective.FingerprintMode)
	require.True(t, snapshot.Effective.FingerprintConvergence)
	require.True(t, snapshot.Effective.DeviceWireProfile)
	require.Equal(t, "fixed_proxy", snapshot.Effective.ProxyMode)
	require.Equal(t, 7, snapshot.Effective.LoadFactor)

	payload, err := json.Marshal(snapshot)
	require.NoError(t, err)
	text := string(payload)
	require.NotContains(t, text, "secret-access-token")
	require.NotContains(t, text, "secret-refresh-token")
	require.NotContains(t, text, "11111111-1111-4111-8111-111111111111")
	require.NotContains(t, text, "device-secret")
}

func TestOpenAIAccountRuntimeSnapshotServicePluginRouteOverridesTransport(t *testing.T) {
	account := &Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1}
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	svc := NewOpenAIAccountRuntimeSnapshotService(
		runtimeSnapshotAccountReader{account: account},
		NewOpenAIWSProtocolResolver(cfg),
		runtimeSnapshotPluginRouter(true),
	)
	snapshot, err := svc.Get(context.Background(), account.ID)
	require.NoError(t, err)
	require.True(t, snapshot.Effective.PluginRouted)
	require.Equal(t, "openai_oauth_outbound", snapshot.Effective.PluginMode)
	require.Equal(t, "plugin_http_bridge", snapshot.Effective.Transport)
	require.Equal(t, "openai_oauth_plugin_routed", snapshot.Effective.TransportReason)
	require.Equal(t, string(OpenAIUpstreamTransportResponsesWebsocketV2), snapshot.Effective.CoreTransport)
}

func TestOpenAIAccountRuntimeSnapshotServiceUsesGlobalWebSocketModeDefault(t *testing.T) {
	account := &Account{ID: 13, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1}
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.IngressModeDefault = OpenAIWSIngressModeOff

	svc := NewOpenAIAccountRuntimeSnapshotService(
		runtimeSnapshotAccountReader{account: account},
		NewOpenAIWSProtocolResolver(cfg),
		nil,
	)
	snapshot, err := svc.Get(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, OpenAIWSIngressModeOff, snapshot.Configured.WebSocketMode)
	require.Equal(t, string(OpenAIUpstreamTransportHTTPSSE), snapshot.Effective.CoreTransport)
	require.Equal(t, "account_mode_off", snapshot.Effective.CoreTransportReason)
}

func TestOpenAIAccountRuntimeSnapshotServiceUnsupportedAccount(t *testing.T) {
	svc := NewOpenAIAccountRuntimeSnapshotService(runtimeSnapshotAccountReader{account: &Account{ID: 12, Platform: PlatformAnthropic, Type: AccountTypeOAuth}}, nil, nil)
	_, err := svc.Get(context.Background(), 12)
	require.ErrorIs(t, err, ErrOpenAIAccountRuntimeUnsupported)
}

func TestOpenAIAccountRuntimeAuthType(t *testing.T) {
	tests := []struct {
		name    string
		account *Account
		want    string
	}{
		{name: "api key", account: &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, want: "api_key"},
		{name: "setup token", account: &Account{Platform: PlatformOpenAI, Type: AccountTypeSetupToken}, want: "setup_token"},
		{name: "agent identity", account: &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"auth_mode": OpenAIAuthModeAgentIdentity}}, want: "agent_identity"},
		{name: "pat", account: &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"auth_mode": OpenAIAuthModePersonalAccessToken}}, want: "personal_access_token"},
		{name: "oauth", account: &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}, want: "oauth"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, openAIAccountRuntimeAuthType(tt.account))
			require.False(t, strings.Contains(tt.want, "token:"))
		})
	}
}

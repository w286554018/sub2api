package service

import (
	"context"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var ErrOpenAIAccountRuntimeUnsupported = infraerrors.BadRequest("OPENAI_ACCOUNT_RUNTIME_UNSUPPORTED", "account runtime snapshot is only available for OpenAI accounts")

type OpenAIAccountRuntimeAccountReader interface {
	GetAccount(ctx context.Context, id int64) (*Account, error)
}

type OpenAIAccountRuntimePluginRouter interface {
	ShouldRouteOpenAIOAuth(account *Account) bool
}

type OpenAIAccountRuntimeSnapshotService struct {
	accounts   OpenAIAccountRuntimeAccountReader
	wsResolver OpenAIWSProtocolResolver
	plugins    OpenAIAccountRuntimePluginRouter
}

func NewOpenAIAccountRuntimeSnapshotService(
	accounts OpenAIAccountRuntimeAccountReader,
	wsResolver OpenAIWSProtocolResolver,
	plugins OpenAIAccountRuntimePluginRouter,
) *OpenAIAccountRuntimeSnapshotService {
	return &OpenAIAccountRuntimeSnapshotService{
		accounts:   accounts,
		wsResolver: wsResolver,
		plugins:    plugins,
	}
}

type OpenAIAccountRuntimeSnapshot struct {
	Source          string                                 `json:"source"`
	Observed        bool                                   `json:"observed"`
	AccountID       int64                                  `json:"account_id"`
	Platform        string                                 `json:"platform"`
	Type            string                                 `json:"type"`
	AuthType        string                                 `json:"auth_type"`
	AccountRevision time.Time                              `json:"account_revision"`
	Configured      OpenAIAccountRuntimeConfiguredSnapshot `json:"configured"`
	Effective       OpenAIAccountRuntimeEffectiveSnapshot  `json:"effective"`
}

type OpenAIAccountRuntimeConfiguredSnapshot struct {
	Passthrough   bool   `json:"passthrough"`
	WebSocketMode string `json:"websocket_mode"`
	ForceHTTP     bool   `json:"force_http"`
	Concurrency   int    `json:"concurrency"`
	LoadFactor    *int   `json:"load_factor,omitempty"`
	ProxyID       *int64 `json:"proxy_id,omitempty"`
}

type OpenAIAccountRuntimeEffectiveSnapshot struct {
	Transport              string `json:"transport"`
	TransportReason        string `json:"transport_reason"`
	CoreTransport          string `json:"core_transport"`
	CoreTransportReason    string `json:"core_transport_reason"`
	PluginRouted           bool   `json:"plugin_routed"`
	PluginMode             string `json:"plugin_mode"`
	Passthrough            bool   `json:"passthrough"`
	FingerprintMode        string `json:"fingerprint_mode"`
	FingerprintConvergence bool   `json:"fingerprint_convergence"`
	DeviceWireProfile      bool   `json:"device_wire_profile"`
	ProxyMode              string `json:"proxy_mode"`
	ProxyID                *int64 `json:"proxy_id,omitempty"`
	Concurrency            int    `json:"concurrency"`
	LoadFactor             int    `json:"load_factor"`
}

func (s *OpenAIAccountRuntimeSnapshotService) Get(ctx context.Context, accountID int64) (*OpenAIAccountRuntimeSnapshot, error) {
	if s == nil || s.accounts == nil {
		return nil, infraerrors.New(500, "OPENAI_ACCOUNT_RUNTIME_UNAVAILABLE", "account runtime snapshot service is unavailable")
	}
	account, err := s.accounts.GetAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if account == nil {
		return nil, ErrAccountNotFound
	}
	if !account.IsOpenAI() {
		return nil, ErrOpenAIAccountRuntimeUnsupported
	}

	decision := openAIWSHTTPDecision("resolver_unavailable")
	if s.wsResolver != nil {
		decision = s.wsResolver.Resolve(account)
	}
	configuredMode := OpenAIWSIngressModeOff
	if s.wsResolver != nil {
		configuredMode = s.wsResolver.ConfiguredMode(account)
	}

	pluginRouted := s.plugins != nil && s.plugins.ShouldRouteOpenAIOAuth(account)
	transport := string(decision.Transport)
	transportReason := decision.Reason
	pluginMode := "none"
	if pluginRouted {
		transport = "plugin_http_bridge"
		transportReason = "openai_oauth_plugin_routed"
		pluginMode = "openai_oauth_outbound"
	}

	proxyMode := "direct"
	if account.ProxyID != nil && *account.ProxyID > 0 {
		proxyMode = "fixed_proxy"
	}

	mode := account.GetCodexFingerprintMode()
	convergence := codexFingerprintConvergenceEnabled(account)
	return &OpenAIAccountRuntimeSnapshot{
		Source:          "computed",
		Observed:        false,
		AccountID:       account.ID,
		Platform:        account.Platform,
		Type:            account.Type,
		AuthType:        openAIAccountRuntimeAuthType(account),
		AccountRevision: account.UpdatedAt.UTC(),
		Configured: OpenAIAccountRuntimeConfiguredSnapshot{
			Passthrough:   account.IsOpenAIPassthroughEnabled(),
			WebSocketMode: configuredMode,
			ForceHTTP:     account.IsOpenAIWSForceHTTPEnabled(),
			Concurrency:   account.Concurrency,
			LoadFactor:    account.LoadFactor,
			ProxyID:       account.ProxyID,
		},
		Effective: OpenAIAccountRuntimeEffectiveSnapshot{
			Transport:              transport,
			TransportReason:        transportReason,
			CoreTransport:          string(decision.Transport),
			CoreTransportReason:    decision.Reason,
			PluginRouted:           pluginRouted,
			PluginMode:             pluginMode,
			Passthrough:            account.IsOpenAIPassthroughEnabled(),
			FingerprintMode:        string(mode),
			FingerprintConvergence: convergence,
			DeviceWireProfile:      mode == codexFingerprintDevice && convergence,
			ProxyMode:              proxyMode,
			ProxyID:                account.ProxyID,
			Concurrency:            account.Concurrency,
			LoadFactor:             account.EffectiveLoadFactor(),
		},
	}, nil
}

func openAIAccountRuntimeAuthType(account *Account) string {
	if account == nil {
		return "unknown"
	}
	if account.IsOpenAIApiKey() {
		return "api_key"
	}
	if account.Type == AccountTypeSetupToken {
		return "setup_token"
	}
	if account.IsOpenAIAgentIdentity() {
		return "agent_identity"
	}
	if account.IsOpenAIPersonalAccessToken() {
		return "personal_access_token"
	}
	if account.IsOpenAIOAuth() {
		return "oauth"
	}
	return strings.TrimSpace(account.Type)
}

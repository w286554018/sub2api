package service

import (
	"bytes"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type openAICompatBridgeSession struct {
	SessionID       string
	ContextWindowID string
	ExpiresAt       time.Time
}

type openAICompatBridgeTurnMetadata struct {
	InstallationID      string `json:"installation_id"`
	SessionID           string `json:"session_id"`
	ThreadID            string `json:"thread_id"`
	AgentName           string `json:"agent_name"`
	TurnID              string `json:"turn_id"`
	WindowID            string `json:"window_id"`
	WindowNumber        uint64 `json:"window_number"`
	ContextWindowID     string `json:"context_window_id"`
	RequestKind         string `json:"request_kind"`
	TurnStartedAtUnixMs int64  `json:"turn_started_at_unix_ms"`
}

const openAICompatBridgeAgentName = "/root"

func openAICompatBridgeSessionKey(c *gin.Context, account *Account, promptCacheKey string) string {
	key := strings.TrimSpace(promptCacheKey)
	if account == nil || key == "" {
		return ""
	}
	namespace := codexAccountIdentityNamespace(codexAccountIdentitySource(c, account))
	if namespace == "" {
		namespace = "id:" + strconv.FormatInt(account.ID, 10)
	}
	apiKeyID := int64(0)
	if c != nil {
		apiKeyID = getAPIKeyIDFromContext(c)
	}
	return strings.Join([]string{namespace, strconv.FormatInt(apiKeyID, 10), key}, "\x00")
}

func (s *OpenAIGatewayService) openAICompatBridgeSession(c *gin.Context, account *Account, promptCacheKey string) (openAICompatBridgeSession, bool) {
	if s == nil {
		return openAICompatBridgeSession{}, false
	}
	key := openAICompatBridgeSessionKey(c, account, promptCacheKey)
	if key == "" {
		return openAICompatBridgeSession{}, false
	}
	now := time.Now()
	s.sweepOpenAICompatBridgeSessions(now)
	ttl := s.openAIWSResponseStickyTTL()
	fresh := openAICompatBridgeSession{
		SessionID:       uuid.Must(uuid.NewV7()).String(),
		ContextWindowID: uuid.Must(uuid.NewV7()).String(),
		ExpiresAt:       now.Add(ttl),
	}
	for {
		raw, loaded := s.openaiCompatBridgeSessions.LoadOrStore(key, fresh)
		if !loaded {
			return fresh, true
		}
		existing, ok := raw.(openAICompatBridgeSession)
		expired := !ok || existing.SessionID == "" ||
			(!existing.ExpiresAt.IsZero() && now.After(existing.ExpiresAt))
		if expired {
			if s.openaiCompatBridgeSessions.CompareAndSwap(key, raw, fresh) {
				return fresh, true
			}
			continue
		}
		renewed := existing
		renewed.ExpiresAt = now.Add(ttl)
		if s.openaiCompatBridgeSessions.CompareAndSwap(key, raw, renewed) {
			return existing, true
		}
	}
}

func (s *OpenAIGatewayService) sweepOpenAICompatBridgeSessions(now time.Time) {
	if s.openaiCompatBridgeSessionWrites.Add(1)%256 != 0 {
		return
	}
	s.openaiCompatBridgeSessions.Range(func(key, value any) bool {
		session, ok := value.(openAICompatBridgeSession)
		if !ok {
			s.openaiCompatBridgeSessions.Delete(key)
		} else if !session.ExpiresAt.IsZero() && now.After(session.ExpiresAt) {
			// A concurrent request may have renewed the session since Range read it.
			s.openaiCompatBridgeSessions.CompareAndDelete(key, session)
		}
		return true
	})
}

func (s *OpenAIGatewayService) injectOpenAICompatBridgeIdentity(
	c *gin.Context,
	account *Account,
	reqBody map[string]any,
	promptCacheKey string,
) (restore func(), injected bool) {
	restore = func() {}
	if s == nil || c == nil || c.Request == nil || reqBody == nil ||
		!codexDeviceWireProfileEnabled(c, account) {
		return restore, false
	}
	session, ok := s.openAICompatBridgeSession(c, account, promptCacheKey)
	if !ok {
		return restore, false
	}
	installationID := ""
	if ids := resolveCodexFingerprintIDsWithBody(c, account, nil, nil); ids != nil {
		installationID = ids.installationID
	}
	turnID := uuid.Must(uuid.NewV7()).String()
	windowID := session.SessionID + ":0"
	payload, err := marshalOpenAIUpstreamJSON(openAICompatBridgeTurnMetadata{
		InstallationID:      installationID,
		SessionID:           session.SessionID,
		ThreadID:            session.SessionID,
		AgentName:           openAICompatBridgeAgentName,
		TurnID:              turnID,
		WindowID:            windowID,
		WindowNumber:        0,
		ContextWindowID:     session.ContextWindowID,
		RequestKind:         "turn",
		TurnStartedAtUnixMs: time.Now().UnixMilli(),
	})
	if err != nil {
		return restore, false
	}
	turnMetadata := string(bytes.TrimSpace(payload))

	original := c.Request.Header
	inbound := original.Clone()
	if inbound == nil {
		inbound = make(http.Header)
	}
	inbound.Set("session-id", session.SessionID)
	inbound.Set("thread-id", session.SessionID)
	inbound.Set("x-codex-window-id", windowID)
	inbound.Set(openAIWSTurnMetadataHeader, turnMetadata)
	c.Request.Header = inbound
	restore = func() { c.Request.Header = original }

	reqBody["prompt_cache_key"] = session.SessionID
	clientMetadata, _ := reqBody["client_metadata"].(map[string]any)
	if clientMetadata == nil {
		clientMetadata = make(map[string]any)
	}
	clientMetadata["session_id"] = session.SessionID
	clientMetadata["thread_id"] = session.SessionID
	clientMetadata["turn_id"] = turnID
	clientMetadata["x-codex-window-id"] = windowID
	clientMetadata[openAIWSTurnMetadataHeader] = turnMetadata
	reqBody["client_metadata"] = clientMetadata
	if _, ok := reqBody["tool_choice"]; !ok {
		reqBody["tool_choice"] = "auto"
	}
	return restore, true
}

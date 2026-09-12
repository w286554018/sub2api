package service

import (
	"crypto/sha256"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func codexDeviceWireProfileEnabled(c *gin.Context, account *Account) bool {
	return codexDeviceWireProfileEnabledFor(account, codexAccountIdentitySource(c, account))
}

func codexDeviceWireProfileEnabledFor(account, credentialAccount *Account) bool {
	return account != nil && account.GetCodexFingerprintMode() == codexFingerprintDevice &&
		codexFingerprintConvergenceEnabled(credentialAccount)
}

func applyCodexDeviceWireProfile(c *gin.Context, account *Account, headers http.Header, websocket bool) {
	if headers == nil || !codexDeviceWireProfileEnabled(c, account) {
		return
	}
	if !websocket {
		stripOpenAILegacyResponsesBeta(headers)
		if c != nil && c.Request != nil &&
			c.Request.Method == http.MethodPost &&
			GetOpenAIClientTransport(c) != OpenAIClientTransportWS &&
			c.Request.URL != nil &&
			strings.HasSuffix(c.Request.URL.Path, "/responses") &&
			!isOpenAIResponsesCompactPath(c) {
			if value := strings.TrimSpace(c.GetHeader("x-codex-inference-call-id")); value != "" {
				headers.Set("x-codex-inference-call-id", uuid.NewString())
			}
		}
	}
	if !websocket && isOpenAIResponsesCompactPath(c) {
		headers.Del("x-client-request-id")
	} else if ids := stagedCodexFingerprintIDs(c, account); ids != nil &&
		ids.mode == codexFingerprintDevice && ids.installationID != "" {
		headers.Del("x-codex-installation-id")
	}
	stripCodexTurnMetadataFields(headers, "tool_namespaces_info")
	if websocket {
		headers.Del(openAICodexTurnStateHeader)
	}
}

func applyCodexCompactPromptCacheKey(c *gin.Context, account *Account, body map[string]any) bool {
	if body == nil {
		return false
	}
	key, ok := body["prompt_cache_key"].(string)
	if !ok || strings.TrimSpace(key) == "" {
		if _, exists := body["prompt_cache_key"]; exists && !codexDeviceWireProfileEnabled(c, account) {
			delete(body, "prompt_cache_key")
			return true
		}
		return false
	}
	if !codexDeviceWireProfileEnabled(c, account) {
		delete(body, "prompt_cache_key")
		return true
	}
	kind := "prompt-cache"
	if session := compactPromptCacheSessionEvidence(c); session != "" &&
		session == key && !codexConvergencePromptCacheKeyPattern.MatchString(key) {
		kind = "session"
	}
	scoped := scopeCodexAccountIdentityValue(
		codexAccountIdentitySource(c, account),
		getAPIKeyIDFromContext(c),
		kind,
		key,
	)
	if scoped == key {
		return false
	}
	body["prompt_cache_key"] = scoped
	return true
}

func stripCodexCompactPromptCacheKeyWhenProfileOff(c *gin.Context, account *Account, body []byte) ([]byte, bool) {
	if codexDeviceWireProfileEnabled(c, account) ||
		!gjson.GetBytes(body, "prompt_cache_key").Exists() {
		return body, false
	}
	next, err := sjson.DeleteBytes(body, "prompt_cache_key")
	if err != nil {
		return body, false
	}
	return next, true
}

func filterCodexCompactDeviceFields(c *gin.Context, account *Account, body []byte) []byte {
	if !isOpenAIResponsesCompactPath(c) || codexDeviceWireProfileEnabled(c, account) {
		return body
	}
	raw := string(body)
	next := deleteCodexJSONMembers(raw, func(name string, _ gjson.Result) bool {
		return name == "prompt_cache_key" || name == "access_programs"
	})
	if next == raw {
		return body
	}
	return []byte(next)
}

func compactPromptCacheSessionEvidence(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	if session := strings.TrimSpace(c.GetHeader("session-id")); session != "" {
		return session
	}
	value := gjson.Parse(c.GetHeader(openAIWSTurnMetadataHeader)).Get("session_id")
	if value.Type != gjson.String || strings.TrimSpace(value.Str) == "" {
		return ""
	}
	return value.Str
}

func stripCodexTurnMetadataFields(headers http.Header, fields ...string) {
	raw := headers.Get(openAIWSTurnMetadataHeader)
	if !gjson.Valid(raw) || !gjson.Parse(raw).IsObject() {
		return
	}
	raw = deleteCodexJSONMembers(raw, func(name string, _ gjson.Result) bool {
		for _, field := range fields {
			if name == field {
				return true
			}
		}
		return false
	})
	headers.Set(openAIWSTurnMetadataHeader, raw)
}

func codexIdentitySeedKind(kind string) string {
	switch kind {
	case "session", "prompt-cache":
		return "thread"
	default:
		return kind
	}
}

var codexConvergenceIdentityFields = []struct {
	name string
	kind string
}{
	{name: "root_turn_id", kind: "turn"},
	{name: "parent_turn_id", kind: "turn"},
	{name: "parent_thread_id", kind: "thread"},
	{name: "x-codex-parent-thread-id", kind: "thread"},
	{name: "forked_from_thread_id", kind: "thread"},
	{name: "context_window_id", kind: "context_window"},
}

func applyCodexConvergenceIdentityFields(values map[string]any, account *Account, apiKeyID int64) bool {
	if values == nil || !codexFingerprintConvergenceEnabled(account) {
		return false
	}
	changed := false
	for _, field := range codexConvergenceIdentityFields {
		raw, ok := values[field.name].(string)
		if !ok || strings.TrimSpace(raw) == "" {
			continue
		}
		next := scopeCodexAccountIdentityValue(account, apiKeyID, field.kind, raw)
		if next != raw {
			values[field.name] = next
			changed = true
		}
	}
	return changed
}

func preserveCodexConvergenceRootTurn(metadata map[string]any, ids *codexFingerprintIDs) {
	if metadata == nil || ids == nil || !ids.convergence || ids.turnID == "" {
		return
	}
	if ids.mode != codexFingerprintSession && ids.mode != codexFingerprintFull {
		return
	}
	turn, _ := metadata["turn_id"].(string)
	root, _ := metadata["root_turn_id"].(string)
	if strings.TrimSpace(turn) != "" && root == turn {
		metadata["root_turn_id"] = ids.turnID
	}
}

const codexConvergenceUUIDPattern = `[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`

var codexConvergenceWindowIDPattern = regexp.MustCompile(`^(` + codexConvergenceUUIDPattern + `):([0-9]+)$`)

var codexConvergencePromptCacheKeyPattern = regexp.MustCompile(`^([A-Za-z0-9_-]{1,64}):(` + codexConvergenceUUIDPattern + `)$`)

func deriveCodexConvergenceIdentityValue(account *Account, seed, raw string) (string, bool) {
	if !codexFingerprintConvergenceEnabled(account) {
		return "", false
	}
	parsed, err := uuid.Parse(raw)
	if err != nil || parsed.Version() != 7 || parsed.String() != raw {
		return "", false
	}
	hash := sha256.Sum256([]byte(seed))
	var derived uuid.UUID
	copy(derived[:], hash[:16])
	copy(derived[0:6], parsed[0:6])
	derived[6] = (derived[6] & 0x0f) | 0x70
	derived[8] = (derived[8] & 0x3f) | 0x80
	return derived.String(), true
}

func stageCodexConvergenceBodyIdentityMap(c *gin.Context, account *Account, body map[string]any) {
	if !codexFingerprintConvergenceEnabled(account) || body == nil {
		stageCodexConvergenceBodyIdentity(c, account, nil)
		return
	}
	clientMetadata, _ := body["client_metadata"].(map[string]any)
	staged := map[string]string{}
	for _, pair := range codexConvergenceBodyToHeader {
		value, _ := clientMetadata[pair[0]].(string)
		setCodexConvergenceStagedValue(staged, pair[1], value)
	}
	if staged["x-codex-parent-thread-id"] == "" {
		parent, _ := clientMetadata["parent_thread_id"].(string)
		setCodexConvergenceStagedValue(staged, "x-codex-parent-thread-id", parent)
	}
	embedded, _ := clientMetadata[openAIWSTurnMetadataHeader].(string)
	fillCodexConvergenceIdentityFrom(staged, gjson.Parse(embedded))
	stageCodexConvergenceBodyIdentity(c, account, staged)
}

func stageCodexConvergenceBodyIdentityRaw(c *gin.Context, account *Account, body []byte) {
	if !codexFingerprintConvergenceEnabled(account) || len(body) == 0 {
		stageCodexConvergenceBodyIdentity(c, account, nil)
		return
	}
	clientMetadata := gjson.GetBytes(body, "client_metadata")
	staged := map[string]string{}
	fillCodexConvergenceIdentityFrom(staged, clientMetadata)
	fillCodexConvergenceIdentityFrom(staged, codexConvergenceEmbeddedMetadata(clientMetadata))
	stageCodexConvergenceBodyIdentity(c, account, staged)
}

func codexConvergenceMetadataString(metadata gjson.Result, name string) string {
	value := metadata.Get(name)
	if !metadata.IsObject() || value.Type != gjson.String {
		return ""
	}
	return strings.TrimSpace(value.Str)
}

func codexConvergenceEmbeddedMetadata(metadata gjson.Result) gjson.Result {
	value := metadata.Get(openAIWSTurnMetadataHeader)
	if value.Type != gjson.String {
		return gjson.Result{}
	}
	return gjson.Parse(value.Str)
}

var codexConvergenceBodyToHeader = [][2]string{
	{"session_id", "session-id"},
	{"thread_id", "thread-id"},
	{"x-codex-parent-thread-id", "x-codex-parent-thread-id"},
	{"x-openai-subagent", "x-openai-subagent"},
}

func fillCodexConvergenceIdentityFrom(staged map[string]string, metadata gjson.Result) {
	if !metadata.IsObject() {
		return
	}
	for _, pair := range codexConvergenceBodyToHeader {
		if staged[pair[1]] == "" {
			setCodexConvergenceStagedValue(staged, pair[1], codexConvergenceMetadataString(metadata, pair[0]))
		}
	}
	if staged["x-codex-parent-thread-id"] == "" {
		setCodexConvergenceStagedValue(staged, "x-codex-parent-thread-id", codexConvergenceMetadataString(metadata, "parent_thread_id"))
	}
}

func setCodexConvergenceStagedValue(staged map[string]string, name, value string) {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		staged[name] = trimmed
	}
}

const codexConvergenceStagedBodyIdentityContextKey = "codex_convergence_body_identity"

type codexConvergenceStagedBodyIdentity struct {
	accountID int64
	headers   map[string]string
}

func stageCodexConvergenceBodyIdentity(c *gin.Context, account *Account, staged map[string]string) {
	if c == nil {
		return
	}
	if account == nil || len(staged) == 0 {
		c.Set(codexConvergenceStagedBodyIdentityContextKey, nil)
		return
	}
	c.Set(codexConvergenceStagedBodyIdentityContextKey, codexConvergenceStagedBodyIdentity{
		accountID: account.ID,
		headers:   staged,
	})
}

func stagedCodexConvergenceBodyIdentity(c *gin.Context, account *Account) map[string]string {
	if c == nil || account == nil {
		return nil
	}
	value, ok := c.Get(codexConvergenceStagedBodyIdentityContextKey)
	if !ok {
		return nil
	}
	staged, ok := value.(codexConvergenceStagedBodyIdentity)
	if !ok || staged.accountID != account.ID {
		return nil
	}
	return staged.headers
}

var codexConvergenceInboundHeaders = []struct {
	name string
	kind string
}{
	{name: "session-id", kind: "session"},
	{name: "thread-id", kind: "thread"},
	{name: "x-codex-parent-thread-id", kind: "thread"},
	{name: "x-openai-subagent", kind: ""},
}

func codexConvergenceTurnMetadataIdentity(headers http.Header) map[string]string {
	raw := strings.TrimSpace(headers.Get(openAIWSTurnMetadataHeader))
	if raw == "" {
		return nil
	}
	metadata := gjson.Parse(raw)
	if !metadata.IsObject() {
		return nil
	}
	values := map[string]string{}
	fillCodexConvergenceIdentityFrom(values, metadata)
	return values
}

func applyCodexIdentityToWSPayload(c *gin.Context, account *Account, payload []byte) ([]byte, error) {
	stageCodexFingerprintIDs(c, nil)
	source := codexAccountIdentitySource(c, account)
	stageCodexConvergenceBodyIdentity(c, source, nil)
	var ids *codexFingerprintIDs
	if codexFingerprintConvergenceEnabled(source) {
		ids = resolveCodexFingerprintIDsWithBody(c, account, nil, gjson.GetBytes(payload, "client_metadata"))
	}
	next, _, err := applyCodexAccountIdentityClientMetadataRaw(payload, source, getAPIKeyIDFromContext(c))
	if err != nil {
		return payload, err
	}
	if ids != nil {
		rewritten, changed, err := applyCodexFingerprintClientMetadataRaw(next, ids)
		if err != nil {
			return payload, err
		}
		if changed {
			next = rewritten
		}
	}
	stageCodexFingerprintIDs(c, ids)
	stageCodexConvergenceBodyIdentityRaw(c, source, next)
	return next, nil
}

func applyCodexFingerprintConvergenceHeaders(c *gin.Context, account *Account, headers http.Header) {
	if headers == nil || !codexFingerprintConvergenceEnabled(account) || codexAccountIdentityNamespace(account) == "" {
		return
	}
	apiKeyID := getAPIKeyIDFromContext(c)
	var inbound http.Header
	if c != nil && c.Request != nil {
		inbound = c.Request.Header
	}
	staged := stagedCodexConvergenceBodyIdentity(c, account)
	fromTurnMetadata := codexConvergenceTurnMetadataIdentity(headers)
	for _, field := range codexConvergenceInboundHeaders {
		if headers.Get(field.name) != "" {
			continue
		}
		if inbound != nil {
			if raw := strings.TrimSpace(inbound.Get(field.name)); raw != "" {
				if field.kind == "" {
					headers.Set(field.name, raw)
				} else {
					headers.Set(field.name, scopeCodexAccountIdentityValue(account, apiKeyID, field.kind, raw))
				}
				continue
			}
		}
		if value := staged[field.name]; value != "" {
			headers.Set(field.name, value)
			continue
		}
		if value := fromTurnMetadata[field.name]; value != "" {
			headers.Set(field.name, value)
		}
	}
	if threadID := strings.TrimSpace(headers.Get("thread-id")); threadID != "" {
		headers.Set("x-client-request-id", threadID)
	}
	if strings.TrimSpace(headers.Get("session-id")) != "" {
		headers.Del("session_id")
		headers.Del("conversation_id")
	}
}

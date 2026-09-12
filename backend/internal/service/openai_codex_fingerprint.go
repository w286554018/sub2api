package service

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// codexFingerprintIDsContextKey 是暂存在 gin context 的收敛 ID 集合键。
// 由 Forward（非透传）或 forwardOpenAIPassthrough（透传）解析后写入，请求
// 构造器读取用于出站头改写——请求体与出站头必须共享同一份 IDs，保证
// turn_id 等随机字段一致。
const codexFingerprintIDsContextKey = "codex_fingerprint_ids"

// stageCodexFingerprintIDs 将本 attempt 解析出的收敛 ID 暂存到 gin context。
// 必须无条件覆写（含 nil）：failover 从收敛账号切到 off 账号时，上一账号的
// IDs 不得残留并被误应用到新账号的出站头（typed-nil 由应用侧 nil 守卫吸收）。
func stageCodexFingerprintIDs(c *gin.Context, ids *codexFingerprintIDs) {
	if c != nil {
		c.Set(codexFingerprintIDsContextKey, ids)
	}
}

func stagedCodexFingerprintIDs(c *gin.Context, account *Account) *codexFingerprintIDs {
	if c == nil || account == nil || !account.UsesOpenAICodexProtocol() {
		return nil
	}
	value, ok := c.Get(codexFingerprintIDsContextKey)
	if !ok {
		return nil
	}
	ids, ok := value.(*codexFingerprintIDs)
	if !ok || ids == nil || ids.accountID != account.ID {
		return nil
	}
	return ids
}

// applyStagedCodexFingerprintHeaders 读取 context 暂存的收敛 ID 并改写出站头。
// 非透传与透传两个请求构造器共用本函数，防止应用语义漂移。仅解析该
// snapshot 的 OAuth 账号可读取，避免 stale context 跨账号 failover 泄漏。
func applyStagedCodexFingerprintHeaders(c *gin.Context, account *Account, h http.Header) {
	applyCodexFingerprintHeaders(h, stagedCodexFingerprintIDs(c, account))
}

func applyStagedCodexFingerprintClientMetadata(c *gin.Context, account *Account, reqBody map[string]any) bool {
	return applyCodexFingerprintClientMetadata(reqBody, stagedCodexFingerprintIDs(c, account))
}

// codexFingerprintMode 控制 OAuth 账号出站请求的设备指纹收敛强度。
// 多人共享同一 OAuth 账号时，每个用户的 Codex 客户端会携带各自不同的
// installation_id / session_id / thread_id，上游据此判定设备数和会话数。
// 收敛模式将这些标识改写为账号级恒定值，减少上游可见的设备/会话指纹。
type codexFingerprintMode string

const (
	// codexFingerprintOff 不做任何收敛，原样透传客户端标识。
	// 这是默认值：收敛是显式 opt-in 的（见 GetCodexFingerprintMode）。
	codexFingerprintOff codexFingerprintMode = "off"
	// codexFingerprintDevice 仅收敛 installation_id 为账号级恒定值。
	// 上游看到 1 台设备 + 多会话（每用户各自的 session）。
	codexFingerprintDevice codexFingerprintMode = "device"
	// codexFingerprintSession 收敛 installation_id + session_id，
	// thread_id 按客户端原始 session-id 确定性派生（每个真实 Codex 会话一个独立线程）。
	// 上游看到 1 台设备 + 1 会话 + N 线程，最接近正常用户 spawn 子代理的模式。
	codexFingerprintSession codexFingerprintMode = "session"
	// codexFingerprintFull 收敛所有标识：installation_id + session_id + thread_id。
	// 上游看到 1 台设备 + 1 会话 + 1 线程，最激进。
	codexFingerprintFull codexFingerprintMode = "full"
)

const (
	codexFingerprintModeExtraKey        = "codex_fingerprint_mode"
	codexFingerprintSeedExtraKey        = "codex_fingerprint_seed"
	codexFingerprintConvergenceExtraKey = "codex_experimental_fingerprint_convergence"
)

func canonicalCodexFingerprintSeed(value any) (string, bool) {
	raw, ok := value.(string)
	if !ok {
		return "", false
	}
	trimmed := strings.TrimSpace(raw)
	parsed, err := uuid.Parse(trimmed)
	if err != nil || parsed == uuid.Nil || trimmed != parsed.String() {
		return "", false
	}
	return trimmed, true
}

func newCodexFingerprintSeed() string {
	return uuid.NewString()
}

func stripCodexFingerprintSeed(extra map[string]any) map[string]any {
	if extra == nil {
		return nil
	}
	stripped := maps.Clone(extra)
	delete(stripped, codexFingerprintSeedExtraKey)
	return stripped
}

func codexFingerprintModeFromExtra(extra map[string]any) codexFingerprintMode {
	if extra == nil {
		return codexFingerprintOff
	}
	raw, _ := extra[codexFingerprintModeExtraKey].(string)
	switch codexFingerprintMode(strings.TrimSpace(raw)) {
	case codexFingerprintOff, codexFingerprintDevice, codexFingerprintSession, codexFingerprintFull:
		return codexFingerprintMode(strings.TrimSpace(raw))
	default:
		return codexFingerprintOff
	}
}

func codexFingerprintModeRequiresSeed(mode codexFingerprintMode) bool {
	switch mode {
	case codexFingerprintDevice, codexFingerprintSession, codexFingerprintFull:
		return true
	default:
		return false
	}
}

func codexFingerprintSeed(extra map[string]any) (string, bool) {
	if extra == nil {
		return "", false
	}
	return canonicalCodexFingerprintSeed(extra[codexFingerprintSeedExtraKey])
}

func prepareCodexFingerprintExtraForCreate(platform, accountType string, extra map[string]any) map[string]any {
	prepared := stripCodexFingerprintSeed(extra)
	if platform != PlatformOpenAI || (accountType != AccountTypeOAuth && accountType != AccountTypeSetupToken) || !codexFingerprintModeRequiresSeed(codexFingerprintModeFromExtra(prepared)) {
		return prepared
	}
	if prepared == nil {
		prepared = make(map[string]any, 1)
	}
	prepared[codexFingerprintSeedExtraKey] = newCodexFingerprintSeed()
	return prepared
}

func prepareCodexFingerprintExtraForUpdate(account *Account, extra map[string]any) map[string]any {
	prepared := stripCodexFingerprintSeed(extra)
	if account == nil || !account.IsOpenAIOAuthLike() {
		return prepared
	}
	if seed, ok := codexFingerprintSeed(account.Extra); ok {
		if prepared == nil {
			prepared = make(map[string]any, 1)
		}
		prepared[codexFingerprintSeedExtraKey] = seed
		return prepared
	}
	if codexFingerprintModeRequiresSeed(codexFingerprintModeFromExtra(prepared)) {
		if prepared == nil {
			prepared = make(map[string]any, 1)
		}
		prepared[codexFingerprintSeedExtraKey] = newCodexFingerprintSeed()
	}
	return prepared
}

func sanitizedCodexFingerprintExtraUpdates(updates map[string]any) map[string]any {
	if updates == nil {
		return nil
	}
	sanitized := maps.Clone(updates)
	delete(sanitized, codexFingerprintSeedExtraKey)
	return sanitized
}

// ShouldEnsureCodexFingerprintSeedForExtraUpdates reports whether a JSONB key-level
// extra update is enabling Codex fingerprint convergence and therefore must atomically
// preserve or create the system-managed per-account seed in the repository update.
func ShouldEnsureCodexFingerprintSeedForExtraUpdates(updates map[string]any) bool {
	if updates == nil {
		return false
	}
	return codexFingerprintModeRequiresSeed(codexFingerprintModeFromExtra(updates))
}

// GetCodexFingerprintMode 从账号 extra JSON 读取指纹收敛模式。
//
// **收敛是显式 opt-in**：未设置、空值或非法值一律按 off 处理，只有管理员
// 明确配置 device / session / full 才收敛。
//
// 历史：v0.1.175（#5553）把缺省值当作 session，导致升级后存量 OAuth 账号
// （普遍没有这个 extra 键）的每个非透传请求都被静默改写 installation /
// session / thread / turn / window 五类标识；#5555、#5556、#5582 报告的额度
// 缩水都卡在该版本边界，并有"回退 v0.1.173 即恢复"与"新账号开收敛后降额"
// 的 A/B 实测。上游的配额判定策略不可观测，因此这里取兼容安全的一侧：
// 不显式 opt-in 就保持 v0.1.175 之前的客户端身份（#5610）。
func (a *Account) GetCodexFingerprintMode() codexFingerprintMode {
	if a == nil || !a.IsOpenAIOAuthLike() {
		return codexFingerprintOff
	}
	return codexFingerprintModeFromExtra(a.Extra)
}

// deriveStableUUIDv4 从种子确定性派生一个 UUIDv4 格式的字符串。
// 同一种子永远返回同一值。
func deriveStableUUIDv4(seed string) string {
	h := sha256.Sum256([]byte(seed))
	b := h[:16]
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 1
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		binary.BigEndian.Uint32(b[0:4]),
		binary.BigEndian.Uint16(b[4:6]),
		binary.BigEndian.Uint16(b[6:8]),
		binary.BigEndian.Uint16(b[8:10]),
		b[10:16])
}

// resolveConvergedInstallationID 返回账号级恒定的 installation_id。
// 优先使用管理员配置的真实 device_id，无则从系统管理的账号随机种子确定性派生。
func resolveConvergedInstallationID(account *Account, seed string) string {
	if account == nil {
		return ""
	}
	if deviceID := account.GetOpenAIDeviceID(); deviceID != "" {
		return deviceID
	}
	if seed == "" {
		return ""
	}
	return deriveStableUUIDv4("sub2api:codex-install-id:v2:" + seed)
}

// resolveConvergedSessionID 返回账号级恒定的 session_id。
func resolveConvergedSessionID(seed string) string {
	if seed == "" {
		return ""
	}
	return deriveStableUUIDv4("sub2api:codex-session-id:v2:" + seed)
}

// resolveConvergedThreadID 按客户端原始 session-id 确定性派生 thread_id。
// 每个真实 Codex 会话（不同客户端启动实例）获得一个独立线程，
// 模拟正常用户 spawn 子代理或开多窗口的模式。
func resolveConvergedThreadID(seed, clientSessionID string) string {
	if seed == "" || clientSessionID == "" {
		return ""
	}
	return deriveStableUUIDv4("sub2api:codex-thread-id:v2:" + seed + ":" + clientSessionID)
}

// codexFingerprintIDs 收敛后的完整 ID 集合。
// 由 resolveCodexFingerprintIDs 一次性生成，同一个实例在头改写和体改写之间共享，
// 确保所有载体中的 turn_id 等随机字段一致。体改写时还会补记原始
// client_metadata.session_id，用于识别 root prompt_cache_key 的默认值。
type codexFingerprintIDs struct {
	accountID                     int64
	mode                          codexFingerprintMode
	installationID                string
	sessionID                     string
	threadID                      string
	turnID                        string
	windowID                      string
	windowNumber                  uint64
	turnStartedAtUnixMs           int64
	originalBodySessionID         string
	originalBodySessionIDCaptured bool
	convergence                   bool
	scopedClientSessionID         string
}

// resolveCodexFingerprintIDs 按收敛模式计算出站 ID 集合。
// clientSessionID 是客户端原始的 session-id 头值（连字符形式），用于 session 模式下
// 的 thread_id 派生——每个真实 Codex 会话得到一个独立线程。
// 返回 nil 表示 off 模式，不需要改写。
// 注意：包含随机生成的 turn_id，调用方必须只调用一次并共享结果给头改写和体改写。
func resolveCodexFingerprintIDs(account *Account, clientSessionID string, mode codexFingerprintMode) *codexFingerprintIDs {
	return resolveCodexFingerprintIDsWithWindow(account, clientSessionID, mode, "")
}

func resolveCodexFingerprintIDsWithWindow(account *Account, clientSessionID string, mode codexFingerprintMode, clientWindowNumber string) *codexFingerprintIDs {
	return resolveCodexFingerprintIDsWithEvidence(account, clientSessionID, mode, clientWindowNumber, codexFingerprintConvergenceEnabled(account))
}

func resolveCodexFingerprintIDsWithEvidence(account *Account, clientSessionID string, mode codexFingerprintMode, clientWindowNumber string, convergence bool) *codexFingerprintIDs {
	if account == nil || mode == codexFingerprintOff {
		return nil
	}
	seed, ok := codexFingerprintSeed(account.Extra)
	if !ok {
		return nil
	}

	ids := &codexFingerprintIDs{
		accountID:           account.ID,
		mode:                mode,
		turnStartedAtUnixMs: time.Now().UnixMilli(),
		convergence:         convergence,
	}

	ids.installationID = resolveConvergedInstallationID(account, seed)
	if ids.installationID == "" {
		return nil
	}
	if convergence {
		ids.windowNumber, _ = strconv.ParseUint(codexWindowNumberOrDefault(clientWindowNumber), 10, 64)
	}
	windowNumber := strconv.FormatUint(ids.windowNumber, 10)

	switch mode {
	case codexFingerprintDevice:
		return ids

	case codexFingerprintSession:
		ids.sessionID = resolveConvergedSessionID(seed)
		ids.threadID = resolveConvergedThreadID(seed, clientSessionID)
		if ids.threadID == "" {
			ids.threadID = ids.sessionID
		}
		ids.turnID = uuid.Must(uuid.NewV7()).String()
		ids.windowID = ids.threadID + ":" + windowNumber
		return ids

	case codexFingerprintFull:
		ids.sessionID = resolveConvergedSessionID(seed)
		ids.threadID = ids.sessionID
		ids.turnID = uuid.Must(uuid.NewV7()).String()
		ids.windowID = ids.threadID + ":" + windowNumber
		return ids
	}

	return nil
}

func codexFingerprintConvergenceEnabled(account *Account) bool {
	if account == nil || !account.IsOpenAIOAuthLike() || account.Extra == nil {
		return false
	}
	switch value := account.Extra[codexFingerprintConvergenceExtraKey].(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(strings.TrimSpace(value), "true") || strings.TrimSpace(value) == "1"
	case float64:
		return value != 0
	default:
		return false
	}
}

func codexWindowNumberOrDefault(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || !codexWindowNumberPattern.MatchString(raw) {
		return "0"
	}
	return raw
}

// extractClientSessionID 从请求头中提取客户端原始的会话标识。
// 优先取 session-id（连字符形式，Codex CLI 标准），回退到 session_id（下划线形式）。
// 返回的值尚未被 isolateOpenAISessionID 改写，是客户端的真实标识。
func extractClientSessionID(h http.Header) string {
	if v := strings.TrimSpace(h.Get("session-id")); v != "" {
		return v
	}
	return strings.TrimSpace(h.Get("session_id"))
}

// extractClientWindowNumber extracts the current context-window sequence from
// the wire carriers used by Codex. Invalid values are ignored so an inbound
// header cannot force an overflow or an invented window shape.
func extractClientWindowNumber(h http.Header) string {
	if h == nil {
		return ""
	}
	for _, name := range [...]string{"x-codex-window-id", "window_id"} {
		if match := codexConvergenceWindowIDPattern.FindStringSubmatch(strings.TrimSpace(h.Get(name))); match != nil {
			return match[2]
		}
	}
	metadata := gjson.Parse(h.Get(openAIWSTurnMetadataHeader))
	if number := codexWindowNumberFromJSON(metadata.Get("window_number")); number != "" {
		return number
	}
	if match := codexConvergenceWindowIDPattern.FindStringSubmatch(metadata.Get("window_id").String()); match != nil {
		return match[2]
	}
	return ""
}

var codexWindowNumberPattern = regexp.MustCompile(`^[0-9]{1,19}$`)

func codexWindowNumberFromJSON(value gjson.Result) string {
	var raw string
	switch value.Type {
	case gjson.Number:
		raw = value.Raw
	case gjson.String:
		raw = strings.TrimSpace(value.Str)
	}
	if codexWindowNumberPattern.MatchString(raw) {
		return raw
	}
	return ""
}

func codexFingerprintSessionEvidence(clientMetadata any, allowEmbedded bool) string {
	var sessionID, embedded string
	switch metadata := clientMetadata.(type) {
	case map[string]any:
		sessionID, _ = metadata["session_id"].(string)
		embedded, _ = metadata[openAIWSTurnMetadataHeader].(string)
	case map[string]string:
		sessionID = metadata["session_id"]
		embedded = metadata[openAIWSTurnMetadataHeader]
	case gjson.Result:
		sessionID = codexConvergenceMetadataString(metadata, "session_id")
		embedded = codexConvergenceMetadataString(metadata, openAIWSTurnMetadataHeader)
	}
	if sessionID = strings.TrimSpace(sessionID); sessionID != "" {
		return sessionID
	}
	if allowEmbedded {
		return codexConvergenceMetadataString(gjson.Parse(embedded), "session_id")
	}
	return ""
}

func codexFingerprintWindowNumberEvidence(clientMetadata any) string {
	var windowID, embedded string
	switch metadata := clientMetadata.(type) {
	case map[string]any:
		windowID, _ = metadata["x-codex-window-id"].(string)
		embedded, _ = metadata[openAIWSTurnMetadataHeader].(string)
	case map[string]string:
		windowID = metadata["x-codex-window-id"]
		embedded = metadata[openAIWSTurnMetadataHeader]
	case gjson.Result:
		windowID = codexConvergenceMetadataString(metadata, "x-codex-window-id")
		embedded = codexConvergenceMetadataString(metadata, openAIWSTurnMetadataHeader)
	}
	if match := codexConvergenceWindowIDPattern.FindStringSubmatch(strings.TrimSpace(windowID)); match != nil {
		return match[2]
	}
	metadata := gjson.Parse(embedded)
	if number := codexWindowNumberFromJSON(metadata.Get("window_number")); number != "" {
		return number
	}
	if match := codexConvergenceWindowIDPattern.FindStringSubmatch(metadata.Get("window_id").String()); match != nil {
		return match[2]
	}
	return ""
}

// resolveCodexFingerprintIDsFromRequest 从客户端原始请求头中提取 session-id，
// 结合账号配置一次性解析收敛 ID 集合。调用方应将返回的 ids 同时传给
// applyCodexFingerprintHeaders 和 applyCodexFingerprintClientMetadata。
func resolveCodexFingerprintIDsFromRequest(account *Account, clientHeaders http.Header) *codexFingerprintIDs {
	return resolveCodexFingerprintIDsWithBody(nil, account, clientHeaders, nil)
}

// resolveCodexFingerprintIDsWithBody resolves identity evidence before the
// request body is account-scoped. This keeps body-only and header-based Codex
// clients on the same derived session/thread.
func resolveCodexFingerprintIDsWithBody(c *gin.Context, account *Account, clientHeaders http.Header, rawClientMetadata any) *codexFingerprintIDs {
	if account == nil {
		return nil
	}
	mode := account.GetCodexFingerprintMode()
	if mode == codexFingerprintOff {
		return nil
	}
	if clientHeaders == nil && c != nil && c.Request != nil {
		clientHeaders = c.Request.Header
	}
	clientSessionID := ""
	source := codexAccountIdentitySource(c, account)
	convergence := codexFingerprintConvergenceEnabled(source)
	if clientHeaders != nil {
		clientSessionID = extractClientSessionID(clientHeaders)
	}
	if clientSessionID == "" && convergence {
		clientSessionID = codexFingerprintSessionEvidence(rawClientMetadata, true)
	}
	if clientSessionID == "" && convergence && clientHeaders != nil {
		clientSessionID = codexConvergenceMetadataString(
			gjson.Parse(clientHeaders.Get(openAIWSTurnMetadataHeader)),
			"session_id",
		)
	}
	clientWindowNumber := codexFingerprintWindowNumberEvidence(rawClientMetadata)
	if clientWindowNumber == "" && clientHeaders != nil {
		clientWindowNumber = extractClientWindowNumber(clientHeaders)
	}
	ids := resolveCodexFingerprintIDsWithEvidence(account, clientSessionID, mode, clientWindowNumber, convergence)
	if ids == nil {
		return nil
	}
	if convergence && clientSessionID != "" && c != nil {
		ids.scopedClientSessionID = scopeCodexAccountIdentityValue(
			source,
			getAPIKeyIDFromContext(c),
			"session",
			clientSessionID,
		)
	}
	return ids
}

// applyCodexFingerprintHeaders 按预计算的收敛 ID 改写出站 HTTP 头中的设备指纹。
// 在 buildUpstreamRequest 的白名单透传之后、enforceCodexIdentityHeaders 之前调用。
func applyCodexFingerprintHeaders(h http.Header, ids *codexFingerprintIDs) {
	if h == nil || ids == nil {
		return
	}

	// 所有非 off 模式都收敛 installation_id
	h.Set("x-codex-installation-id", ids.installationID)

	if ids.mode == codexFingerprintDevice {
		rewriteCodexTurnMetadataFields(h, map[string]any{
			"installation_id": ids.installationID,
		}, ids)
		return
	}

	// session / full 模式：改写所有相关头
	h.Set("x-codex-window-id", ids.windowID)
	h.Set("x-client-request-id", ids.threadID)
	// 连字符形式和下划线形式都改写，保证一致
	h.Set("session-id", ids.sessionID)
	h.Set("session_id", ids.sessionID)
	h.Set("thread-id", ids.threadID)

	rewriteCodexTurnMetadataFields(h, map[string]any{
		"installation_id":         ids.installationID,
		"session_id":              ids.sessionID,
		"thread_id":               ids.threadID,
		"turn_id":                 ids.turnID,
		"window_id":               ids.windowID,
		"window_number":           ids.windowNumber,
		"turn_started_at_unix_ms": ids.turnStartedAtUnixMs,
	}, ids)
}

// rewriteCodexTurnMetadataFields 解析 x-codex-turn-metadata 头中的 JSON，
// 替换指定字段后回写。合法对象保留未指定字段（如 sandbox、thread_source）；
// 非法/非对象值重建为最小合法 metadata，避免 flat 与 embedded identity 分裂。
func rewriteCodexTurnMetadataFields(h http.Header, fields map[string]any, ids *codexFingerprintIDs) {
	raw := h.Get(openAIWSTurnMetadataHeader)
	if strings.TrimSpace(raw) == "" {
		return
	}
	h.Set(openAIWSTurnMetadataHeader, rewriteCodexFingerprintTurnMetadata(raw, fields, ids))
}

func rewriteCodexFingerprintTurnMetadata(raw string, fields map[string]any, ids *codexFingerprintIDs) string {
	return rewriteCodexTurnMetadataJSON(raw, true, func(metadata map[string]any) map[string]any {
		updates := maps.Clone(fields)
		root, _ := metadata["root_turn_id"].(string)
		preserveCodexConvergenceRootTurn(metadata, ids)
		if next, _ := metadata["root_turn_id"].(string); next != root {
			updates["root_turn_id"] = next
		}
		return updates
	})
}

// applyCodexFingerprintClientMetadata 按预计算的收敛 ID 改写请求体中的 client_metadata。
// 使用与头改写相同的 ids 实例，确保 turn_id 等随机字段一致。
func applyCodexFingerprintClientMetadata(reqBody map[string]any, ids *codexFingerprintIDs) bool {
	if reqBody == nil || ids == nil {
		return false
	}

	captureCodexFingerprintOriginalBodySessionID(ids, reqBody["client_metadata"])
	existing, _ := reqBody["client_metadata"].(map[string]any)
	if existing == nil {
		existing = make(map[string]any)
	}

	modified := false
	if applyCodexFingerprintToClientMetadataMap(existing, ids) {
		reqBody["client_metadata"] = existing
		modified = true
	}
	if applyCodexFingerprintPromptCacheKey(reqBody, ids) {
		modified = true
	}
	return modified
}

// applyCodexFingerprintToClientMetadataMap 是 client_metadata 改写的共享核心，
// map 版（非透传，body 已解码）与 raw 字节版（透传热路径）都经由它，保证两条
// 路径的收敛语义永不漂移。
func applyCodexFingerprintToClientMetadataMap(existing map[string]any, ids *codexFingerprintIDs) bool {
	if existing == nil || ids == nil {
		return false
	}

	fields := codexFingerprintClientMetadataFields(existing, ids)
	for name, value := range fields {
		existing[name] = value
	}
	if ids.mode == codexFingerprintDevice {
		rewriteClientMetadataEmbeddedTurnMetadata(existing, map[string]any{
			"installation_id": ids.installationID,
		}, ids)
		return len(fields) > 0
	}

	rewriteClientMetadataEmbeddedTurnMetadata(existing, map[string]any{
		"installation_id":         ids.installationID,
		"session_id":              ids.sessionID,
		"thread_id":               ids.threadID,
		"turn_id":                 ids.turnID,
		"window_id":               ids.windowID,
		"window_number":           ids.windowNumber,
		"turn_started_at_unix_ms": ids.turnStartedAtUnixMs,
	}, ids)
	return true
}

func codexFingerprintClientMetadataFields(existing map[string]any, ids *codexFingerprintIDs) map[string]any {
	fields := map[string]any{}
	if ids.installationID != "" {
		fields["x-codex-installation-id"] = ids.installationID
	}
	if ids.mode == codexFingerprintDevice {
		return fields
	}
	root := map[string]any{"root_turn_id": existing["root_turn_id"], "turn_id": existing["turn_id"]}
	originalRoot, _ := root["root_turn_id"].(string)
	preserveCodexConvergenceRootTurn(root, ids)
	if nextRoot, _ := root["root_turn_id"].(string); nextRoot != originalRoot {
		fields["root_turn_id"] = nextRoot
	}
	fields["session_id"] = ids.sessionID
	fields["thread_id"] = ids.threadID
	fields["turn_id"] = ids.turnID
	fields["x-codex-window-id"] = ids.windowID
	return fields
}

func captureCodexFingerprintOriginalBodySessionID(ids *codexFingerprintIDs, clientMetadata any) {
	if ids == nil || ids.originalBodySessionIDCaptured {
		return
	}
	ids.originalBodySessionIDCaptured = true
	ids.originalBodySessionID = codexFingerprintSessionEvidence(clientMetadata, ids.convergence)
}

func shouldRewriteCodexFingerprintPromptCacheKey(ids *codexFingerprintIDs, promptCacheKey string) bool {
	if ids == nil || ids.sessionID == "" {
		return false
	}
	if ids.mode != codexFingerprintSession && ids.mode != codexFingerprintFull {
		return false
	}
	if ids.convergence && codexConvergencePromptCacheKeyPattern.MatchString(strings.TrimSpace(promptCacheKey)) {
		return false
	}
	if ids.originalBodySessionIDCaptured && ids.originalBodySessionID != "" {
		return promptCacheKey == ids.originalBodySessionID
	}
	if !ids.convergence {
		return false
	}
	return ids.scopedClientSessionID != "" && promptCacheKey == ids.scopedClientSessionID
}

func applyCodexFingerprintPromptCacheKey(reqBody map[string]any, ids *codexFingerprintIDs) bool {
	if reqBody == nil {
		return false
	}
	promptCacheKey, ok := reqBody["prompt_cache_key"].(string)
	if !ok || strings.TrimSpace(promptCacheKey) == "" || !shouldRewriteCodexFingerprintPromptCacheKey(ids, promptCacheKey) {
		return false
	}
	if promptCacheKey == ids.sessionID {
		return false
	}
	reqBody["prompt_cache_key"] = ids.sessionID
	return true
}

// Rewrite every carrier without decoding the whole request or collapsing
// duplicate metadata. Missing/non-object metadata uses the map path's repair.
func applyCodexFingerprintClientMetadataRaw(body []byte, ids *codexFingerprintIDs) ([]byte, bool, error) {
	if len(body) == 0 || ids == nil {
		return body, false, nil
	}
	// 非 JSON 对象的 body（数组/标量/畸形）没有 client_metadata 语义，
	// sjson 在这类根上写字段会改写整体结构，直接放行保持原样。
	root := gjson.ParseBytes(body)
	if !root.IsObject() || !gjson.ValidBytes(body) {
		captureCodexFingerprintOriginalBodySessionID(ids, nil)
		return body, false, nil
	}

	captureCodexFingerprintOriginalBodySessionID(ids, gjson.GetBytes(body, "client_metadata"))
	rewriteMetadata := func(raw string) string {
		next := rewriteCodexTurnMetadataJSON(raw, true, func(metadata map[string]any) map[string]any {
			return codexFingerprintClientMetadataFields(metadata, ids)
		})
		return rewriteCodexJSONMembers(next, func(name string, value gjson.Result) (string, bool) {
			if name != openAIWSTurnMetadataHeader || value.Type != gjson.String || value.Str == "" {
				return "", false
			}
			field := map[string]any{name: value.Str}
			applyCodexFingerprintToClientMetadataMap(field, ids)
			metadata, _ := field[name].(string)
			if metadata == value.Str {
				return "", false
			}
			encoded, err := marshalOpenAIUpstreamJSON(metadata)
			return string(encoded), err == nil
		})
	}
	raw := string(body)
	found := false
	next := rewriteCodexJSONMembers(raw, func(name string, value gjson.Result) (string, bool) {
		if name == "client_metadata" {
			found = true
			metadata := rewriteMetadata(value.Raw)
			return metadata, metadata != value.Raw
		}
		if name != "prompt_cache_key" || value.Type != gjson.String || strings.TrimSpace(value.Str) == "" ||
			!shouldRewriteCodexFingerprintPromptCacheKey(ids, value.Str) || value.Str == ids.sessionID {
			return "", false
		}
		encoded, err := marshalCodexTurnMetadataValue(ids.sessionID)
		return encoded, err == nil
	})
	if !found {
		var err error
		next, err = sjson.SetRaw(next, "client_metadata", rewriteMetadata("{}"))
		if err != nil {
			return body, false, fmt.Errorf("splice converged client_metadata: %w", err)
		}
	}
	return []byte(next), next != raw, nil
}

// rewriteClientMetadataEmbeddedTurnMetadata 改写 client_metadata 中内嵌的
// x-codex-turn-metadata JSON 字符串里的指定字段。非法/非对象值会重建，
// 避免 flat client_metadata 与 embedded metadata 暴露两套身份。
func rewriteClientMetadataEmbeddedTurnMetadata(clientMetadata map[string]any, fields map[string]any, ids *codexFingerprintIDs) {
	raw, ok := clientMetadata["x-codex-turn-metadata"].(string)
	if !ok || raw == "" {
		return
	}
	clientMetadata[openAIWSTurnMetadataHeader] = rewriteCodexFingerprintTurnMetadata(raw, fields, ids)
}

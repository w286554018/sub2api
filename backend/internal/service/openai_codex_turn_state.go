package service

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// openAICodexTurnStateHeader 是 Codex 的回合状态头。上游在响应头中铸造该
// 不透明 blob，客户端在同一回合的后续请求中原样回带（codex-rs 侧从
// /responses SSE、/responses/compact JSON 与 WS 握手三种响应中捕获，见
// codex-api/src/sse/responses.rs 与 endpoint/compact.rs）。
const openAICodexTurnStateHeader = "x-codex-turn-state"

// turn-state blob 是上游在"出站身份"（含 #5553 指纹收敛改写后的
// installation/session/thread 标识）下铸造的，同账号回放自洽；跨账号回放
// （failover 换号后客户端仍回带旧账号的 blob）是代理链独有、真实 Codex
// 永远不会产生的矛盾信号。溯源表按 blob 的哈希记录铸造凭据，
// 出站守卫只据该记录剥离已知异账号的回带值。
type openAICodexTurnStateOrigin struct {
	// accountID is retained for compatibility with legacy diagnostics/tests.
	accountID int64
	owner     string
	expiresAt time.Time
}

func openAICodexTurnStateKey(state string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(state)))
	return hex.EncodeToString(sum[:12])
}

func openAICodexTurnStateOwner(c *gin.Context, account *Account) string {
	source := codexAccountIdentitySource(c, account)
	if source == nil {
		return ""
	}
	if namespace := codexAccountIdentityNamespace(source); namespace != "" {
		return namespace
	}
	if source.ID > 0 {
		return "id:" + strconv.FormatInt(source.ID, 10)
	}
	return ""
}

// openAICodexTurnStateSeed remains as a compatibility index for older callers.
// New guarding uses the blob hash index above.
func openAICodexTurnStateSeed(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	sessionID := extractClientSessionID(c.Request.Header)
	if sessionID == "" {
		return ""
	}
	return strconv.FormatInt(getAPIKeyIDFromContext(c), 10) + "\x00" + sessionID
}

func (s *OpenAIGatewayService) noteOpenAICodexTurnStateProvenance(c *gin.Context, account *Account) {
	if s == nil || account == nil || account.ID <= 0 {
		return
	}
	if seed := openAICodexTurnStateSeed(c); seed != "" {
		s.openaiCodexTurnStateOrigins.Store(seed, openAICodexTurnStateOrigin{
			accountID: account.ID,
			owner:     openAICodexTurnStateOwner(c, account),
			expiresAt: time.Now().Add(s.openAIWSSessionStickyTTL()),
		})
	}
}

// relayOpenAICodexTurnState 将上游响应中的 turn-state 显式写入下游响应头，
// 并记录铸造账号。必须在响应头提交点调用（WriteHeader 之前、且确认本次
// 上游响应就是将要写回客户端的响应之后）。上游无该头时主动清除 writer 上
// 可能残留的上一 failover attempt 的值——否则换号后旧账号的 blob 会粘到
// 新账号的响应上，这正是本文件要防止的跨账号矛盾。
func (s *OpenAIGatewayService) relayOpenAICodexTurnState(c *gin.Context, account *Account, upstream http.Header) {
	if c == nil || c.Writer == nil {
		return
	}
	canonical := http.CanonicalHeaderKey(openAICodexTurnStateHeader)
	state := extractOpenAICodexTurnState(upstream)
	if state == "" {
		c.Writer.Header().Del(canonical)
		return
	}
	c.Writer.Header().Set(canonical, state)
	s.noteOpenAICodexTurnStateProvenance(c, account)
	s.noteOpenAICodexTurnStateOrigin(c, account, state)
}

// stageOpenAICodexTurnState 将上游 turn-state 暂存到延迟提交的响应头集合
// （首输出守卫路径先缓存头、见到首个输出事件才提交）。此处**不**记录铸造
// 账号：该 attempt 仍可能在首输出超时后 failover，暂存头会被整体丢弃，
// 客户端从未收到该 blob。溯源必须在真正提交时记录，见
// noteStagedOpenAICodexTurnStateCommitted。
func stageOpenAICodexTurnState(dst *http.Header, upstream http.Header) {
	if dst == nil {
		return
	}
	canonical := http.CanonicalHeaderKey(openAICodexTurnStateHeader)
	state := extractOpenAICodexTurnState(upstream)
	if state == "" {
		if *dst != nil {
			dst.Del(canonical)
		}
		return
	}
	if *dst == nil {
		*dst = http.Header{}
	}
	dst.Set(canonical, state)
}

// noteStagedOpenAICodexTurnStateCommitted 在暂存响应头真正写入下游时记录
// 铸造账号——只有此刻客户端才确定收到了该 blob，溯源表才与客户端持有的
// 值一致（否则被 failover 丢弃的 attempt 会污染溯源，导致后续误剥离）。
func (s *OpenAIGatewayService) noteStagedOpenAICodexTurnStateCommitted(c *gin.Context, account *Account, staged http.Header) {
	if staged == nil || strings.TrimSpace(staged.Get(openAICodexTurnStateHeader)) == "" {
		return
	}
	s.noteOpenAICodexTurnStateProvenance(c, account)
	s.noteOpenAICodexTurnStateOrigin(c, account, staged.Get(openAICodexTurnStateHeader))
}

func extractOpenAICodexTurnState(upstream http.Header) string {
	if upstream == nil {
		return ""
	}
	return strings.TrimSpace(upstream.Get(openAICodexTurnStateHeader))
}

func (s *OpenAIGatewayService) noteOpenAICodexTurnStateOrigin(c *gin.Context, account *Account, state string) {
	if s == nil || account == nil || strings.TrimSpace(state) == "" {
		return
	}
	owner := openAICodexTurnStateOwner(c, account)
	if owner == "" {
		return
	}
	s.openaiCodexTurnStateOrigins.Store(openAICodexTurnStateKey(state), openAICodexTurnStateOrigin{
		accountID: account.ID,
		owner:     owner,
		expiresAt: time.Now().Add(s.openAIWSSessionStickyTTL()),
	})
	s.sweepOpenAICodexTurnStateOrigins()
}

func (s *OpenAIGatewayService) noteOpenAICodexTurnStateFromWSEvent(c *gin.Context, account *Account, frame []byte) {
	if s == nil || account == nil || len(frame) == 0 ||
		gjson.GetBytes(frame, "type").String() != "response.metadata" {
		return
	}
	headers := gjson.GetBytes(frame, "headers")
	if !headers.IsObject() {
		return
	}
	headers.ForEach(func(key, value gjson.Result) bool {
		if strings.EqualFold(key.String(), openAICodexTurnStateHeader) {
			s.noteOpenAICodexTurnStateOrigin(c, account, value.String())
			return false
		}
		return true
	})
}

func (s *OpenAIGatewayService) lookupOpenAICodexTurnStateOrigin(state string) (openAICodexTurnStateOrigin, bool) {
	state = strings.TrimSpace(state)
	if s == nil || state == "" {
		return openAICodexTurnStateOrigin{}, false
	}
	raw, ok := s.openaiCodexTurnStateOrigins.Load(openAICodexTurnStateKey(state))
	if !ok {
		return openAICodexTurnStateOrigin{}, false
	}
	origin, ok := raw.(openAICodexTurnStateOrigin)
	if !ok || (!origin.expiresAt.IsZero() && time.Now().After(origin.expiresAt)) {
		s.openaiCodexTurnStateOrigins.Delete(openAICodexTurnStateKey(state))
		return openAICodexTurnStateOrigin{}, false
	}
	return origin, true
}

func (s *OpenAIGatewayService) openAICodexTurnStateMintedByOther(c *gin.Context, account *Account, state string) bool {
	origin, known := s.lookupOpenAICodexTurnStateOrigin(state)
	return account != nil && known && origin.owner != openAICodexTurnStateOwner(c, account)
}

// guardOpenAICodexTurnStateEcho 出站守卫：客户端回带的 turn-state 若已知由
// 其他账号铸造则剥离，同账号或无溯源记录时保持原样。只剥离、不注入——
// 门控模型的 292 票由 applyOpenAICodexTicket 在守卫之后覆盖写入；
// 非门控模型保持客户端回带（#5668）。
// /responses 路径的客户端是真实 Codex，会按自身回合语义自行回带；服务端
// 注入是 Claude 兼容桥（无法回带的客户端）的专属行为。
func (s *OpenAIGatewayService) guardOpenAICodexTurnStateEcho(c *gin.Context, account *Account, h http.Header) {
	if s == nil || h == nil || account == nil {
		return
	}
	state := strings.TrimSpace(h.Get(openAICodexTurnStateHeader))
	// A session's latest owner is not evidence for an unknown or expired blob.
	if s.openAICodexTurnStateMintedByOther(c, account, state) {
		h.Del(openAICodexTurnStateHeader)
	}
}

func (s *OpenAIGatewayService) guardOpenAICodexTurnStateValue(c *gin.Context, account *Account, state string) string {
	state = strings.TrimSpace(state)
	if state == "" || s.openAICodexTurnStateMintedByOther(c, account, state) {
		return ""
	}
	return state
}

func (s *OpenAIGatewayService) guardOpenAICodexWSFrameTurnState(c *gin.Context, account *Account, payload []byte) []byte {
	if s == nil || account == nil || !gjson.ValidBytes(payload) {
		return payload
	}
	raw := string(payload)
	next := rewriteCodexJSONMembers(raw, func(name string, metadata gjson.Result) (string, bool) {
		if name != "client_metadata" || !metadata.IsObject() {
			return "", false
		}
		clean := deleteCodexJSONMembers(metadata.Raw, func(key string, value gjson.Result) bool {
			return key == openAICodexTurnStateHeader && value.Type == gjson.String &&
				s.openAICodexTurnStateMintedByOther(c, account, value.Str)
		})
		return clean, clean != metadata.Raw
	})
	if next == raw {
		return payload
	}
	return []byte(next)
}

// sweepOpenAICodexTurnStateOrigins 机会式清扫过期溯源记录：每 256 次写入
// 全量遍历一轮，防止仅靠读侧惰性删除导致的慢泄漏（会话键无上界）。
func (s *OpenAIGatewayService) sweepOpenAICodexTurnStateOrigins() {
	if s.openaiCodexTurnStateWrites.Add(1)%256 != 0 {
		return
	}
	now := time.Now()
	s.openaiCodexTurnStateOrigins.Range(func(key, value any) bool {
		origin, ok := value.(openAICodexTurnStateOrigin)
		if !ok || (!origin.expiresAt.IsZero() && now.After(origin.expiresAt)) {
			s.openaiCodexTurnStateOrigins.Delete(key)
		}
		return true
	})
}

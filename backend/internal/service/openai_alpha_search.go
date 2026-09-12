package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	chatgptCodexAlphaSearchURL   = "https://chatgpt.com/backend-api/codex/alpha/search"
	openAIPlatformAlphaSearchURL = "https://api.openai.com/v1/alpha/search"
)

// ForwardAlphaSearch proxies Codex standalone web search without binding the
// evolving alpha request or response schema.
//
// A non-nil result bills one successful search. Standalone Alpha requires 2xx;
// the PAT Responses fallback also requires a validated successful completion.
// Passthrough upstream errors return (nil, nil) without billing.
func (s *OpenAIGatewayService) ForwardAlphaSearch(ctx context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
	if s == nil || c == nil || account == nil {
		return nil, fmt.Errorf("service, context, and account are required")
	}
	credentialSource, err := s.prepareCodexAccountIdentitySource(ctx, c, account)
	if err != nil {
		return nil, err
	}
	modelResult := gjson.GetBytes(body, "model")
	requestedModel := strings.TrimSpace(modelResult.String())
	if modelResult.Type != gjson.String || requestedModel == "" {
		return nil, fmt.Errorf("model is required")
	}

	upstreamModel := normalizeOpenAIModelForUpstream(account, account.GetMappedModel(requestedModel))
	if upstreamModel != "" && upstreamModel != requestedModel {
		body = ReplaceModelInBody(body, upstreamModel)
	}
	sanitizedBody, err := sanitizeOpenAIAlphaSearchBody(body)
	if err != nil {
		return nil, fmt.Errorf("sanitize alpha search request body: %w", err)
	}
	body = sanitizedBody

	token, _, err := s.GetAccessToken(ctx, credentialSource)
	if err != nil {
		return nil, err
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	if err := s.ensureOpenAIAlphaSearchAuthMetadata(ctx, credentialSource, token, proxyURL); err != nil {
		return nil, err
	}
	SetOpsUpstreamModel(c, upstreamModel)

	// Codex Personal Access Token（at-...）目前可访问 ChatGPT Codex
	// /responses，但会被 standalone /alpha/search 的 access enforcement
	// 拒绝为 no_matching_rule。对 PAT 账号使用等价的 hosted web_search
	// Responses 路径兜底，避免把可用账号误判为搜索不可用。
	if credentialSource.IsOpenAIPersonalAccessToken() {
		return s.forwardAlphaSearchViaResponsesWebSearch(ctx, c, account, body, token, proxyURL, requestedModel, upstreamModel)
	}

	req, err := s.buildOpenAIAlphaSearchRequest(ctx, c, account, body, token)
	if err != nil {
		return nil, err
	}

	upstreamStart := time.Now()
	resp, err := s.doOpenAIUpstream(req, proxyURL, account)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
	if err != nil {
		return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, true)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, fmt.Errorf("read alpha search response: %w", err)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		upstreamMessage := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(respBody)))
		if s.shouldFailoverOpenAIUpstreamResponse(account, resp.StatusCode, upstreamMessage, respBody) ||
			isOpenAIAlphaSearchEndpointUnsupported(account, resp.StatusCode) {
			resp.Body = io.NopCloser(bytes.NewReader(respBody))
			// alpha/search 是独立的工具端点，单次 401 不能证明账号的模型调用
			// 凭据全局失效。若沿用通用 401 逻辑，PAT 会因没有 refresh_token
			// 被永久标记为 error；历史导入且缺少 auth_mode 标记的 at- token 也会
			// 漏过 PAT 类型判断。这里仍允许本次请求换号，但不修改任何账号状态；
			// 真正的凭据失效由普通 Responses 请求或 whoami 校验判定。
			shouldDisable := false
			if shouldApplyOpenAIAlphaSearchAccountErrorSideEffects(resp.StatusCode) {
				shouldDisable = s.handleFailoverSideEffects(ctx, resp, account, respBody, openAIAlphaSearchSchedulingModel(account, requestedModel))
			}
			retryableOnSameAccount := !shouldDisable && account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode)
			if account.IsOpenAIOAuthLike() && resp.StatusCode == http.StatusTooManyRequests {
				return nil, s.newOpenAIAccountFailoverError(account, resp.StatusCode, resp.Header, respBody, upstreamMessage, shouldDisable, retryableOnSameAccount)
			}
			if isOpenAIHTTPUpstreamAccessStateError(resp.StatusCode, upstreamMessage, respBody) {
				return nil, newOpenAIUpstreamFailoverError(resp.StatusCode, resp.Header, respBody, upstreamMessage, retryableOnSameAccount)
			}
			return nil, &UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: respBody, RetryableOnSameAccount: retryableOnSameAccount}
		}
	}

	if !account.IsShadow() {
		s.UpdateCodexUsageSnapshotFromHeaders(ctx, account.ID, resp.Header)
	}
	writeOpenAIPassthroughResponseHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	c.Data(resp.StatusCode, contentType, respBody)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		// 非 2xx（错误/重定向）已原样透传给客户端：不是一次成功的搜索，不计费。
		return nil, nil
	}
	return &OpenAIForwardResult{
		RequestID:       strings.TrimSpace(resp.Header.Get("x-request-id")),
		UpstreamHeaders: resp.Header,
		Model:           requestedModel,
		UpstreamModel:   upstreamModel,
		Duration:        time.Since(upstreamStart),
		WebSearchCalls:  1,
	}, nil
}

func (s *OpenAIGatewayService) forwardAlphaSearchViaResponsesWebSearch(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	alphaBody []byte,
	token string,
	proxyURL string,
	requestedModel string,
	upstreamModel string,
) (*OpenAIForwardResult, error) {
	if upstreamModel == "" {
		upstreamModel = requestedModel
	}
	responsesBody, err := buildOpenAIAlphaSearchResponsesWebSearchBody(alphaBody, upstreamModel)
	if err != nil {
		return nil, err
	}
	req, err := s.buildOpenAIAlphaSearchResponsesWebSearchRequest(ctx, c, account, alphaBody, responsesBody, token)
	if err != nil {
		return nil, err
	}
	SetActualOpenAIUpstreamEndpoint(c, "/v1/responses")

	upstreamStart := time.Now()
	resp, err := s.doOpenAIUpstream(req, proxyURL, account)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
	if err != nil {
		return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, true)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, fmt.Errorf("read alpha search responses fallback response: %w", err)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		upstreamMessage := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(respBody)))
		if s.shouldFailoverOpenAIUpstreamResponse(account, resp.StatusCode, upstreamMessage, respBody) {
			resp.Body = io.NopCloser(bytes.NewReader(respBody))
			// 仍按 alpha/search 工具请求处理：PAT 的工具链路失败不能直接永久置错。
			shouldDisable := false
			if shouldApplyOpenAIAlphaSearchAccountErrorSideEffects(resp.StatusCode) {
				shouldDisable = s.handleFailoverSideEffects(ctx, resp, account, respBody, openAIAlphaSearchSchedulingModel(account, requestedModel))
			}
			retryableOnSameAccount := !shouldDisable && account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode)
			if account.IsOpenAIOAuthLike() && resp.StatusCode == http.StatusTooManyRequests {
				return nil, s.newOpenAIAccountFailoverError(account, resp.StatusCode, resp.Header, respBody, upstreamMessage, shouldDisable, retryableOnSameAccount)
			}
			if isOpenAIHTTPUpstreamAccessStateError(resp.StatusCode, upstreamMessage, respBody) {
				return nil, newOpenAIUpstreamFailoverError(resp.StatusCode, resp.Header, respBody, upstreamMessage, retryableOnSameAccount)
			}
			return nil, &UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: respBody, RetryableOnSameAccount: retryableOnSameAccount}
		}
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		writeOpenAIPassthroughResponseHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
		contentType := resp.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/json"
		}
		c.Data(resp.StatusCode, contentType, respBody)
		return nil, nil
	}

	if !account.IsShadow() {
		s.UpdateCodexUsageSnapshotFromHeaders(ctx, account.ID, resp.Header)
	}
	alphaRespBody, err := openAIAlphaSearchResponseFromResponsesSSE(respBody)
	if err != nil {
		return nil, err
	}
	c.Data(http.StatusOK, "application/json", alphaRespBody)
	return &OpenAIForwardResult{
		RequestID:        strings.TrimSpace(resp.Header.Get("x-request-id")),
		UpstreamHeaders:  resp.Header,
		Model:            requestedModel,
		UpstreamModel:    upstreamModel,
		UpstreamEndpoint: "/v1/responses",
		ResponseHeaders:  resp.Header.Clone(),
		Duration:         time.Since(upstreamStart),
		WebSearchCalls:   1,
	}, nil
}

func openAIAlphaSearchSchedulingModel(account *Account, requestedModel string) string {
	return canonicalOpenAIAccountSchedulingModel(account, requestedModel)
}

func (s *OpenAIGatewayService) buildOpenAIAlphaSearchResponsesWebSearchRequest(ctx context.Context, c *gin.Context, account *Account, alphaBody []byte, body []byte, token string) (*http.Request, error) {
	wireBody, contentEncoding, err := compressCodexRequestBody(c, account, chatgptCodexURL, body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, chatgptCodexURL, bytes.NewReader(wireBody))
	if err != nil {
		return nil, err
	}
	if contentEncoding != "" {
		req.Header.Set("Content-Encoding", contentEncoding)
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))

	credentialSource := codexAccountIdentitySource(c, account)
	authHeaders, err := s.buildOpenAIAuthenticationHeaders(ctx, credentialSource, token)
	if err != nil {
		return nil, fmt.Errorf("build openai authentication headers: %w", err)
	}
	for key, values := range authHeaders {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	req.Host = "chatgpt.com"
	if err := resolveAndSetOpenAIChatGPTAccountHeaders(ctx, s.accountRepo, req.Header, credentialSource); err != nil {
		return nil, fmt.Errorf("resolve chatgpt account headers: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	if turnMetadata := openAIAlphaSearchInboundHeader(c, "X-Codex-Turn-Metadata"); turnMetadata != "" {
		req.Header.Set("X-Codex-Turn-Metadata", turnMetadata)
	}
	canonical := resolveCodexOutboundIdentity("")
	if version := openAIAlphaSearchInboundHeader(c, "Version"); version != "" {
		req.Header.Set("Version", version)
	} else {
		req.Header.Set("Version", canonical.version)
	}
	if originator := openAIAlphaSearchInboundHeader(c, "Originator"); originator != "" {
		req.Header.Set("Originator", originator)
	} else {
		req.Header.Set("Originator", canonical.originator)
	}
	if customUA := account.GetOpenAIUserAgent(); customUA != "" {
		req.Header.Set("User-Agent", customUA)
	} else if userAgent := openAIAlphaSearchInboundHeader(c, "User-Agent"); userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	} else {
		req.Header.Set("User-Agent", canonical.userAgent)
	}
	if s.cfg != nil && s.cfg.Gateway.ForceCodexCLI {
		req.Header.Set("User-Agent", canonical.userAgent)
	}
	apiKeyID := getAPIKeyIDFromContext(c)
	if sessionID := strings.TrimSpace(gjson.GetBytes(alphaBody, "id").String()); sessionID != "" {
		isolated := isolateOpenAIUpstreamSessionID(apiKeyID, codexAccountIdentitySource(c, account), sessionID)
		req.Header.Set("Session_ID", isolated)
		req.Header.Set("Conversation_ID", isolated)
	}
	applyCodexAccountIdentityHeaders(req.Header, codexAccountIdentitySource(c, account), apiKeyID)
	enforceCodexIdentityHeadersWithUA(req.Header, s.codexIdentityOverrideUA(account))
	account.ApplyHeaderOverrides(req.Header)
	return req, nil
}

func buildOpenAIAlphaSearchResponsesWebSearchBody(alphaBody []byte, model string) ([]byte, error) {
	if strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("model is required")
	}
	tool := map[string]any{"type": "web_search"}
	if contextSize := strings.TrimSpace(gjson.GetBytes(alphaBody, "settings.search_context_size").String()); contextSize != "" {
		tool["search_context_size"] = contextSize
	}
	if userLocation := gjson.GetBytes(alphaBody, "settings.user_location"); userLocation.IsObject() {
		var loc map[string]any
		if err := json.Unmarshal([]byte(userLocation.Raw), &loc); err == nil && len(loc) > 0 {
			tool["user_location"] = loc
		}
	}
	payload := map[string]any{
		"model":  model,
		"stream": true,
		"store":  false,
		"input": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{
						"type": "input_text",
						"text": openAIAlphaSearchResponsesWebSearchPrompt(alphaBody),
					},
				},
			},
		},
		"tools": []any{tool},
	}
	return json.Marshal(payload)
}

func openAIAlphaSearchResponsesWebSearchPrompt(alphaBody []byte) string {
	var b strings.Builder
	_, _ = b.WriteString("Execute this Codex standalone web.run request for another model.\n")
	_, _ = b.WriteString("Use the hosted web_search tool when web/current information is needed.\n")
	_, _ = b.WriteString("Return concise source-backed results. Include titles, URLs, dates, and direct answers when available.\n")
	if commands := strings.TrimSpace(gjson.GetBytes(alphaBody, "commands").Raw); commands != "" {
		_, _ = b.WriteString("\nCommands JSON:\n")
		_, _ = b.WriteString(truncateOpenAIAlphaSearchPromptJSON(commands, 12000))
	}
	if settings := strings.TrimSpace(gjson.GetBytes(alphaBody, "settings").Raw); settings != "" {
		_, _ = b.WriteString("\n\nSearch settings JSON:\n")
		_, _ = b.WriteString(truncateOpenAIAlphaSearchPromptJSON(settings, 4000))
	}
	if input := strings.TrimSpace(gjson.GetBytes(alphaBody, "input").Raw); input != "" {
		_, _ = b.WriteString("\n\nRecent conversation/input JSON:\n")
		_, _ = b.WriteString(truncateOpenAIAlphaSearchPromptJSON(input, 8000))
	}
	if b.Len() == 0 {
		return "Execute the requested web search and return concise source-backed results."
	}
	return b.String()
}

func truncateOpenAIAlphaSearchPromptJSON(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "\n...<truncated>"
}

func (s *OpenAIGatewayService) buildOpenAIAlphaSearchRequest(ctx context.Context, c *gin.Context, account *Account, body []byte, token string) (*http.Request, error) {
	targetURL, err := s.openAIAlphaSearchURL(account)
	if err != nil {
		return nil, err
	}
	parsedURL, err := url.Parse(targetURL)
	if err != nil {
		return nil, fmt.Errorf("parse alpha search URL: %w", err)
	}
	if c != nil && c.Request != nil && c.Request.URL != nil {
		query := parsedURL.Query()
		for key, values := range c.Request.URL.Query() {
			for _, value := range values {
				query.Add(key, value)
			}
		}
		parsedURL.RawQuery = query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, parsedURL.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))

	credentialSource := codexAccountIdentitySource(c, account)
	authHeaders, err := s.buildOpenAIAuthenticationHeaders(ctx, credentialSource, token)
	if err != nil {
		return nil, fmt.Errorf("build openai authentication headers: %w", err)
	}
	for key, values := range authHeaders {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	if account.Type == AccountTypeOAuth {
		req.Host = "chatgpt.com"
		if err := resolveAndSetOpenAIChatGPTAccountHeaders(ctx, s.accountRepo, req.Header, credentialSource); err != nil {
			return nil, fmt.Errorf("resolve chatgpt account headers: %w", err)
		}

		if turnMetadata := openAIAlphaSearchInboundHeader(c, "X-Codex-Turn-Metadata"); turnMetadata != "" {
			req.Header.Set("X-Codex-Turn-Metadata", turnMetadata)
		}
		applyCodexAccountIdentityHeaders(req.Header, codexAccountIdentitySource(c, account), getAPIKeyIDFromContext(c))
		canonical := resolveCodexOutboundIdentity("")
		if version := openAIAlphaSearchInboundHeader(c, "Version"); version != "" {
			req.Header.Set("Version", version)
		} else {
			req.Header.Set("Version", canonical.version)
		}
		if originator := openAIAlphaSearchInboundHeader(c, "Originator"); originator != "" {
			req.Header.Set("Originator", originator)
		} else {
			req.Header.Set("Originator", canonical.originator)
		}
		if customUA := account.GetOpenAIUserAgent(); customUA != "" {
			req.Header.Set("User-Agent", customUA)
		} else if userAgent := openAIAlphaSearchInboundHeader(c, "User-Agent"); userAgent != "" {
			req.Header.Set("User-Agent", userAgent)
		} else {
			req.Header.Set("User-Agent", canonical.userAgent)
		}
		if s.cfg != nil && s.cfg.Gateway.ForceCodexCLI {
			req.Header.Set("User-Agent", canonical.userAgent)
		}
		enforceCodexIdentityHeadersWithUA(req.Header, s.codexIdentityOverrideUA(account))
	}

	account.ApplyHeaderOverrides(req.Header)
	stripOpenAIAlphaSearchResponsesHeaders(req.Header)
	applyCodexAlphaSearchWireProfile(c, account, req.Header, body)
	syncOpenAIAlphaSearchBodySession(c, req, body)
	return req, nil
}

// Search uses MCP metadata, with model/version taken from the final request.
// All duplicate members are visited; unknown metadata retains its wire spelling.
func applyCodexAlphaSearchWireProfile(c *gin.Context, account *Account, headers http.Header, body []byte) {
	applyCodexDeviceWireProfile(c, account, headers, false)
	if headers == nil || !codexDeviceWireProfileEnabled(c, account) {
		return
	}
	raw := headers.Get(openAIWSTurnMetadataHeader)
	if !gjson.Valid(raw) || !gjson.Parse(raw).IsObject() {
		return
	}
	next := deleteCodexJSONMembers(raw, func(name string, _ gjson.Result) bool {
		switch name {
		case "installation_id", "window_id", "window_number", "context_window_id",
			"agent_name", "parent_turn_id", "root_turn_id", "request_kind", "compaction",
			"history_ingest_requested", "forked_from_ordinal_exclusive", "tool_namespaces_info":
			return true
		default:
			return false
		}
	})
	next = rewriteCodexTurnMetadataJSON(next, false, func(metadata map[string]any) map[string]any {
		updates := make(map[string]any, 2)
		for name, value := range map[string]string{
			"codex_version": headers.Get("Version"),
			"model":         gjson.GetBytes(body, "model").String(),
		} {
			if _, exists := metadata[name]; exists && strings.TrimSpace(value) != "" {
				updates[name] = value
			}
		}
		return updates
	})
	if next != raw {
		headers.Set(openAIWSTurnMetadataHeader, next)
	}
}

// Only exact, unambiguous session evidence authorizes changing a search ID.
// Patch matching members individually so custom and duplicate IDs survive.
func syncOpenAIAlphaSearchBodySession(c *gin.Context, req *http.Request, body []byte) {
	if req == nil || !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return
	}
	inbound := openAIAlphaSearchMetadataSession(openAIAlphaSearchInboundHeader(c, openAIWSTurnMetadataHeader))
	outbound := openAIAlphaSearchMetadataSession(req.Header.Get(openAIWSTurnMetadataHeader))
	if inbound == "" || outbound == "" || inbound == outbound {
		return
	}
	encoded, err := marshalCodexTurnMetadataValue(outbound)
	if err != nil {
		return
	}
	raw := string(body)
	next := rewriteCodexJSONMembers(raw, func(name string, value gjson.Result) (string, bool) {
		return encoded, name == "id" && value.Type == gjson.String && value.Str == inbound
	})
	if next == raw {
		return
	}
	req.Body = io.NopCloser(strings.NewReader(next))
	req.ContentLength = int64(len(next))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(next)), nil }
}

func openAIAlphaSearchMetadataSession(raw string) string {
	if !gjson.Valid(raw) || !gjson.Parse(raw).IsObject() {
		return ""
	}
	session := ""
	valid := true
	gjson.Parse(raw).ForEach(func(key, value gjson.Result) bool {
		if key.Str != "session_id" {
			return true
		}
		if value.Type != gjson.String || strings.TrimSpace(value.Str) == "" ||
			(session != "" && value.Str != session) {
			valid = false
			return false
		}
		session = value.Str
		return true
	})
	if !valid {
		return ""
	}
	return session
}

// stripOpenAIAlphaSearchResponsesHeaders 让独立搜索请求与官方 Codex
// SearchClient 的线协议保持一致。alpha/search 不是 /responses 的子请求：官方
// 客户端仅在 Provider/Auth 基础头之外附加 x-codex-turn-metadata，不发送
// OpenAI-Beta、会话隔离或 Responses Lite 状态头。originator 与 User-Agent
// 属于官方默认客户端头，必须保留。
//
// alpha/search 使用专用构造器生成官方 SearchClient 的最小线协议形态；
// 该函数作为最后一道防线，避免账号 header 覆写或后续改动重新带入
// Responses 专用头，使 PAT 的 alpha/search 被上游按错误认证路径处理。
func stripOpenAIAlphaSearchResponsesHeaders(headers http.Header) {
	if headers == nil {
		return
	}
	for _, key := range []string{
		"OpenAI-Beta",
		"Session_ID",
		"Conversation_ID",
		"X-Codex-Beta-Features",
		"X-Codex-Turn-State",
		responsesLiteHeaderKey,
	} {
		headers.Del(key)
	}
}

func openAIAlphaSearchInboundHeader(c *gin.Context, key string) string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.GetHeader(key))
}

var openAIAlphaSearchUnsupportedBodyFields = [...]string{
	// Codex alpha/search 是 SearchRequest 独立协议，不是 /responses 子请求。
	// 新版 Codex/第三方代理可能把 Responses 公共字段误带到搜索请求里；ChatGPT
	// alpha/search 会对这些字段返回 Unknown parameter（例如 prompt_cache_key）。
	"prompt_cache_key",
	"prompt_cache_retention",
	"store",
}

func sanitizeOpenAIAlphaSearchBody(body []byte) ([]byte, error) {
	if len(body) == 0 {
		return body, nil
	}
	raw := string(body)
	next := deleteCodexJSONMembers(raw, func(name string, _ gjson.Result) bool {
		for _, field := range openAIAlphaSearchUnsupportedBodyFields {
			if name == field {
				return true
			}
		}
		return false
	})
	if next == raw {
		return body, nil
	}
	return []byte(next), nil
}

func (s *OpenAIGatewayService) ensureOpenAIAlphaSearchAuthMetadata(ctx context.Context, account *Account, token string, proxyURL string) error {
	if s == nil || account == nil || !account.IsOpenAIPersonalAccessToken() {
		return nil
	}
	if strings.TrimSpace(account.GetChatGPTAccountID()) != "" {
		return nil
	}
	var oauthService *OpenAIOAuthService
	if s.openAITokenProvider != nil {
		oauthService = s.openAITokenProvider.openAIOAuthService
	}
	if oauthService == nil {
		return nil
	}
	tokenInfo, err := oauthService.ValidateCodexPersonalAccessToken(ctx, token, proxyURL)
	if err != nil {
		return fmt.Errorf("validate Codex PAT metadata for alpha/search: %w", err)
	}
	credentials := shallowCopyMap(account.Credentials)
	for key, value := range oauthService.BuildAccountCredentials(tokenInfo) {
		credentials[key] = value
	}
	credentials = NormalizeOpenAIPersonalAccessTokenCredentials(account, tokenInfo, credentials)
	account.Credentials = shallowCopyMap(credentials)
	if s.accountRepo != nil {
		if err := persistAccountCredentials(ctx, s.accountRepo, account, credentials); err != nil {
			return fmt.Errorf("persist Codex PAT metadata for alpha/search: %w", err)
		}
	}
	return nil
}

// isOpenAIAlphaSearchEndpointUnsupported 识别「API key 上游没有实现
// /v1/alpha/search 端点」的响应。404/405 不在通用 failover 状态集里（模型
// 调用中的 404 通常是用户请求问题），但对这个独立工具端点而言，它几乎只
// 意味着所选上游（官方平台或第三方中转）不提供该端点——应换号重试，而
// 不是把 404 透传给客户端，否则混合分组里 OAuth 账号明明可以承接搜索，
// 请求却可能死在先被选中的 API key 账号上。
func isOpenAIAlphaSearchEndpointUnsupported(account *Account, statusCode int) bool {
	if account == nil || account.Type != AccountTypeAPIKey {
		return false
	}
	return statusCode == http.StatusNotFound || statusCode == http.StatusMethodNotAllowed
}

func shouldApplyOpenAIAlphaSearchAccountErrorSideEffects(statusCode int) bool {
	switch statusCode {
	case http.StatusUnauthorized, http.StatusNotFound, http.StatusMethodNotAllowed:
		// 401：工具端点的 access enforcement 不代表凭据全局失效；
		// 404/405：端点不存在只说明该上游不支持独立搜索，账号本身健康。
		// 两类都只换号，不写账号错误状态。
		return false
	default:
		return true
	}
}

func openAIAlphaSearchResponseFromResponsesSSE(body []byte) ([]byte, error) {
	output, results, err := parseOpenAIResponsesSSEForAlphaSearch(body)
	if err != nil {
		return nil, fmt.Errorf("parse alpha search responses fallback: %w", err)
	}
	resp := map[string]any{
		"output": output,
	}
	if len(results) > 0 {
		resp["results"] = results
	}
	return json.Marshal(resp)
}

func parseOpenAIResponsesSSEForAlphaSearch(body []byte) (string, []any, error) {
	text := strings.ReplaceAll(string(body), "\r\n", "\n")
	var output strings.Builder
	var completedResponse map[string]any
	results := make([]any, 0)
	seenURLs := make(map[string]struct{})

	blocks := strings.Split(text, "\n\n")
	for i, block := range blocks {
		data := openAIAlphaSearchSSEData(block)
		if data == "" || data == "[DONE]" {
			continue
		}
		if i == len(blocks)-1 {
			return "", nil, fmt.Errorf("unterminated SSE event: %w", io.ErrUnexpectedEOF)
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return "", nil, fmt.Errorf("invalid SSE event: %w", err)
		}
		eventType, _ := event["type"].(string)
		if eventType == "" {
			return "", nil, fmt.Errorf("SSE event is missing a string type")
		}
		switch eventType {
		case "response.failed", "response.incomplete", "response.cancelled", "response.canceled", "error":
			return "", nil, fmt.Errorf("unsuccessful terminal event: %s", eventType)
		case "response.output_text.delta":
			if delta, _ := event["delta"].(string); delta != "" {
				_, _ = output.WriteString(delta)
			}
		case "response.completed":
			response, ok := event["response"].(map[string]any)
			if !ok || response["status"] != "completed" || response["error"] != nil || response["incomplete_details"] != nil {
				return "", nil, fmt.Errorf("response.completed does not contain a successful response")
			}
			completedResponse = response
		}
		collectOpenAIAlphaSearchURLCitations(event, &results, seenURLs)
	}

	// HTTP 200, text deltas and [DONE] alone do not prove a billable completion.
	if completedResponse == nil {
		return "", nil, fmt.Errorf("missing successful response.completed event")
	}
	out := output.String()
	if strings.TrimSpace(out) == "" {
		out = extractOpenAIResponsesCompletedText(completedResponse)
		collectOpenAIAlphaSearchURLCitations(completedResponse, &results, seenURLs)
	}
	return out, results, nil
}

func openAIAlphaSearchSSEData(block string) string {
	var lines []string
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		lines = append(lines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func extractOpenAIResponsesCompletedText(response any) string {
	resp, ok := response.(map[string]any)
	if !ok {
		return ""
	}
	outputItems, _ := resp["output"].([]any)
	var b strings.Builder
	for _, item := range outputItems {
		itemMap, ok := item.(map[string]any)
		if !ok || itemMap["type"] != "message" {
			continue
		}
		contentItems, _ := itemMap["content"].([]any)
		for _, content := range contentItems {
			contentMap, ok := content.(map[string]any)
			if !ok {
				continue
			}
			if contentMap["type"] == "output_text" {
				if text, _ := contentMap["text"].(string); text != "" {
					_, _ = b.WriteString(text)
				}
			}
		}
	}
	return b.String()
}

func collectOpenAIAlphaSearchURLCitations(value any, results *[]any, seen map[string]struct{}) {
	switch typed := value.(type) {
	case map[string]any:
		if typed["type"] == "url_citation" {
			if urlValue, _ := typed["url"].(string); strings.TrimSpace(urlValue) != "" {
				urlValue = strings.TrimSpace(urlValue)
				if _, exists := seen[urlValue]; !exists {
					seen[urlValue] = struct{}{}
					result := map[string]any{
						"type":   "text_result",
						"ref_id": fmt.Sprintf("turn0search%d", len(*results)),
						"url":    urlValue,
					}
					if title, _ := typed["title"].(string); strings.TrimSpace(title) != "" {
						result["title"] = strings.TrimSpace(title)
					}
					*results = append(*results, result)
				}
			}
		}
		for _, child := range typed {
			collectOpenAIAlphaSearchURLCitations(child, results, seen)
		}
	case []any:
		for _, child := range typed {
			collectOpenAIAlphaSearchURLCitations(child, results, seen)
		}
	}
}

func (s *OpenAIGatewayService) openAIAlphaSearchURL(account *Account) (string, error) {
	if account == nil {
		return "", fmt.Errorf("account is required")
	}
	switch account.Type {
	case AccountTypeOAuth, AccountTypeSetupToken:
		return chatgptCodexAlphaSearchURL, nil
	case AccountTypeAPIKey:
		baseURL := account.GetOpenAIBaseURL()
		if baseURL == "" {
			return openAIPlatformAlphaSearchURL, nil
		}
		validatedURL, err := s.validateUpstreamBaseURL(baseURL)
		if err != nil {
			return "", err
		}
		return buildOpenAIEndpointURL(validatedURL, "/v1/alpha/search"), nil
	default:
		return "", fmt.Errorf("unsupported OpenAI account type: %s", account.Type)
	}
}

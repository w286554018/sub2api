package handler

import (
	"context"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *OpenAIGatewayHandler) cyberPolicyLogOnly(c *gin.Context, apiKey *service.APIKey) bool {
	return h != nil && c != nil && c.Request != nil && h.gatewayService.CyberPolicyLogOnly(c.Request.Context(), apiKey)
}

// Both HTTP and WebSocket admission skip existing blocks for trusted users.
func (h *OpenAIGatewayHandler) findBlockedCyberSessionForAPIKey(c *gin.Context, apiKey *service.APIKey, body []byte) string {
	if apiKey == nil || h.cyberPolicyLogOnly(c, apiKey) {
		return ""
	}
	return findBlockedCyberSessionKey(c.Request.Context(), h.gatewayService, apiKey.ID, c, body)
}

// Session identity is still resolved and observed by the caller.
// findBlockedCyberSessionForRequest also uses the content fingerprint fallback
// when the client supplied no explicit session identity, so an identical retry
// of a cyber-blocked request is rejected before account selection.
func (h *OpenAIGatewayHandler) findBlockedCyberSessionForRequest(c *gin.Context, apiKey *service.APIKey, identity service.CyberSessionIdentityResolution, body []byte) string {
	if apiKey == nil || h.cyberPolicyLogOnly(c, apiKey) {
		return ""
	}
	return h.gatewayService.FindCyberSessionBlockedForIdentityAndBody(c.Request.Context(), apiKey.ID, identity, body)
}

func findBlockedCyberSessionKey(ctx context.Context, gatewayService *service.OpenAIGatewayService, apiKeyID int64, c *gin.Context, body []byte) string {
	if gatewayService == nil {
		return ""
	}
	clientIP, userAgent := "", ""
	if c != nil {
		clientIP = strings.TrimSpace(ip.GetClientIP(c))
		userAgent = c.GetHeader("User-Agent")
	}
	return gatewayService.FindCyberSessionBlockedForRequest(ctx, apiKeyID, c, body, clientIP, userAgent)
}

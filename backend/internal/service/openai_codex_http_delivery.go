package service

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Observe accepted writes, not Gin's Written flag alone: an implicit header
// commit can set Written even when the following body write returns (0, err).
type openAICodexHTTPDeliveryWriter struct {
	gin.ResponseWriter
	headers     http.Header
	delivered   bool
	onDelivered func(http.Header)
}

func observeOpenAICodexHTTPDelivery(c *gin.Context, onDelivered func(http.Header)) func() {
	original := c.Writer
	w := &openAICodexHTTPDeliveryWriter{ResponseWriter: original, onDelivered: onDelivered}
	c.Writer = w
	return func() {
		if !w.delivered {
			original.Header().Del(openAICodexTurnStateHeader)
		}
		c.Writer = original
	}
}

func (s *OpenAIGatewayService) stageOpenAICodexHTTPResponseDelivery(c *gin.Context, account *Account, upstream http.Header) func() {
	if account != nil && account.Platform == PlatformGrok {
		s.relayOpenAICodexTurnState(c, account, upstream)
		return func() {}
	}
	headers := c.Writer.Header()
	stageOpenAICodexTurnState(&headers, upstream)
	return observeOpenAICodexHTTPDelivery(c, func(deliveredHeaders http.Header) {
		s.noteStagedOpenAICodexTurnStateCommitted(c, account, deliveredHeaders)
	})
}

func (w *openAICodexHTTPDeliveryWriter) snapshotHeaders() bool {
	if w.ResponseWriter.Written() {
		return false
	}
	w.headers = w.ResponseWriter.Header().Clone()
	return true
}

func (w *openAICodexHTTPDeliveryWriter) noteDelivery() {
	if !w.delivered {
		w.delivered = true
		w.onDelivered(w.headers)
	}
}

func (w *openAICodexHTTPDeliveryWriter) Write(p []byte) (int, error) {
	pendingHeaders := w.snapshotHeaders()
	n, err := w.ResponseWriter.Write(p)
	if n > 0 || (err == nil && pendingHeaders && w.ResponseWriter.Written()) {
		w.noteDelivery()
	}
	return n, err
}

func (w *openAICodexHTTPDeliveryWriter) WriteString(value string) (int, error) {
	pendingHeaders := w.snapshotHeaders()
	n, err := w.ResponseWriter.WriteString(value)
	if n > 0 || (err == nil && pendingHeaders && w.ResponseWriter.Written()) {
		w.noteDelivery()
	}
	return n, err
}

func (w *openAICodexHTTPDeliveryWriter) WriteHeader(status int) {
	pendingHeaders := w.snapshotHeaders()
	w.ResponseWriter.WriteHeader(status)
	if pendingHeaders && w.ResponseWriter.Written() {
		w.noteDelivery()
	}
}

func (w *openAICodexHTTPDeliveryWriter) WriteHeaderNow() {
	pendingHeaders := w.snapshotHeaders()
	w.ResponseWriter.WriteHeaderNow()
	if pendingHeaders && w.ResponseWriter.Written() {
		w.noteDelivery()
	}
}

func (w *openAICodexHTTPDeliveryWriter) Flush() {
	pendingHeaders := w.snapshotHeaders()
	w.ResponseWriter.Flush()
	if pendingHeaders && w.ResponseWriter.Written() {
		w.noteDelivery()
	}
}

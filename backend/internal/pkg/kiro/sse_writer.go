package kiro

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
)

// StreamState represents the state of the Anthropic SSE state machine.
type StreamState int

const (
	StateInit           StreamState = iota // Initial state
	StateMessageStarted                    // message_start sent
	StateInContentBlock                    // Inside a content block (deltas allowed)
	StateBetweenBlocks                     // Between content blocks
	StateMessageDelta                      // message_delta sent
	StateMessageStopped                    // message_stop sent (terminal)
	StateError                             // Error (terminal)
)

func (s StreamState) String() string {
	switch s {
	case StateInit:
		return "Init"
	case StateMessageStarted:
		return "MessageStarted"
	case StateInContentBlock:
		return "InContentBlock"
	case StateBetweenBlocks:
		return "BetweenBlocks"
	case StateMessageDelta:
		return "MessageDelta"
	case StateMessageStopped:
		return "MessageStopped"
	case StateError:
		return "Error"
	default:
		return fmt.Sprintf("Unknown(%d)", int(s))
	}
}

func (s StreamState) isTerminal() bool {
	return s == StateMessageStopped || s == StateError
}

// AnthropicSSEWriter serializes Anthropic-compatible SSE events with
// strict state machine validation. Each instance is per-request and not
// safe for concurrent use (no mutex needed — single goroutine per request).
type AnthropicSSEWriter struct {
	ctx        context.Context
	w          io.Writer
	state      StreamState
	blockIndex int    // current content block index (-1 = none)
	blockType  string // current block type: "text", "thinking", "tool_use"
	model      string
	requestID  string
	err        error      // sticky error: once set, all writes return it
	mu         sync.Mutex // only protects Err() reads from other goroutines
}

// NewAnthropicSSEWriter creates a new SSE writer with state machine validation.
func NewAnthropicSSEWriter(ctx context.Context, w io.Writer, model, requestID string) *AnthropicSSEWriter {
	return &AnthropicSSEWriter{
		ctx:        ctx,
		w:          w,
		state:      StateInit,
		blockIndex: -1,
		model:      model,
		requestID:  requestID,
	}
}

// State returns the current state machine state.
func (s *AnthropicSSEWriter) State() StreamState { return s.state }

// BlockIndex returns the current content block index (-1 if none).
func (s *AnthropicSSEWriter) BlockIndex() int { return s.blockIndex }

// Err returns the sticky error, if any.
func (s *AnthropicSSEWriter) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// --- Internal helpers ---

func (s *AnthropicSSEWriter) setErr(err error) error {
	if s.err == nil {
		s.mu.Lock()
		s.err = err
		s.mu.Unlock()
		s.state = StateError
	}
	return s.err
}

func (s *AnthropicSSEWriter) checkCtx() error {
	if s.err != nil {
		return s.err
	}
	if err := s.ctx.Err(); err != nil {
		return s.setErr(fmt.Errorf("context cancelled: %w", err))
	}
	return nil
}

func (s *AnthropicSSEWriter) checkTransition(event string) error {
	if err := s.checkCtx(); err != nil {
		return err
	}
	valid := false
	switch s.state {
	case StateInit:
		valid = event == "message_start"
	case StateMessageStarted:
		valid = event == "content_block_start" || event == "message_delta"
	case StateInContentBlock:
		valid = event == "content_block_delta" || event == "content_block_stop"
	case StateBetweenBlocks:
		valid = event == "content_block_start" || event == "message_delta"
	case StateMessageDelta:
		valid = event == "message_stop"
	}
	if !valid {
		return s.setErr(fmt.Errorf("illegal SSE transition: %q in state %s", event, s.state))
	}
	return nil
}

func (s *AnthropicSSEWriter) writeSSE(eventName string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return s.setErr(fmt.Errorf("json marshal: %w", err))
	}
	line := "event: " + eventName + "\ndata: " + string(payload) + "\n\n"
	if _, err := io.WriteString(s.w, line); err != nil {
		return s.setErr(fmt.Errorf("write: %w", err))
	}
	s.tryFlush(eventName)
	return nil
}

func (s *AnthropicSSEWriter) tryFlush(eventName string) {
	switch eventName {
	case "message_start", "content_block_start", "content_block_stop",
		"message_delta", "message_stop", "ping", "error":
		if f, ok := s.w.(http.Flusher); ok {
			f.Flush()
		}
	}
}

// --- Public API ---

// WriteMessageStart emits the message_start event. Must be called exactly once.
func (s *AnthropicSSEWriter) WriteMessageStart(inputTokens, cacheCreate, cacheRead int) error {
	if err := s.checkTransition("message_start"); err != nil {
		return err
	}
	usageMap := map[string]any{
		"input_tokens":  inputTokens,
		"output_tokens": 0,
	}
	if cacheCreate > 0 {
		usageMap["cache_creation_input_tokens"] = cacheCreate
	}
	if cacheRead > 0 {
		usageMap["cache_read_input_tokens"] = cacheRead
	}
	err := s.writeSSE("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            s.requestID,
			"type":          "message",
			"role":          "assistant",
			"content":       []any{},
			"model":         s.model,
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         usageMap,
		},
	})
	if err == nil {
		s.state = StateMessageStarted
	}
	return err
}

// StartTextBlock emits content_block_start for a text block.
func (s *AnthropicSSEWriter) StartTextBlock() error {
	if err := s.checkTransition("content_block_start"); err != nil {
		return err
	}
	s.blockIndex++
	s.blockType = "text"
	err := s.writeSSE("content_block_start", map[string]any{
		"type":          "content_block_start",
		"index":         s.blockIndex,
		"content_block": map[string]any{"type": "text", "text": ""},
	})
	if err == nil {
		s.state = StateInContentBlock
	}
	return err
}

// StartThinkingBlock emits content_block_start for a thinking block.
func (s *AnthropicSSEWriter) StartThinkingBlock() error {
	if err := s.checkTransition("content_block_start"); err != nil {
		return err
	}
	s.blockIndex++
	s.blockType = "thinking"
	err := s.writeSSE("content_block_start", map[string]any{
		"type":          "content_block_start",
		"index":         s.blockIndex,
		"content_block": map[string]any{"type": "thinking", "thinking": ""},
	})
	if err == nil {
		s.state = StateInContentBlock
	}
	return err
}

// StartToolUseBlock emits content_block_start for a tool_use block.
func (s *AnthropicSSEWriter) StartToolUseBlock(id, name string) error {
	if err := s.checkTransition("content_block_start"); err != nil {
		return err
	}
	s.blockIndex++
	s.blockType = "tool_use"
	err := s.writeSSE("content_block_start", map[string]any{
		"type":  "content_block_start",
		"index": s.blockIndex,
		"content_block": map[string]any{
			"type":  "tool_use",
			"id":    id,
			"name":  name,
			"input": map[string]any{},
		},
	})
	if err == nil {
		s.state = StateInContentBlock
	}
	return err
}

// WriteTextDelta emits a text_delta. Empty text is silently ignored.
func (s *AnthropicSSEWriter) WriteTextDelta(text string) error {
	if text == "" {
		return nil
	}
	if err := s.checkTransition("content_block_delta"); err != nil {
		return err
	}
	if s.blockType != "text" {
		return s.setErr(fmt.Errorf("text_delta in %s block", s.blockType))
	}
	return s.writeSSE("content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": s.blockIndex,
		"delta": map[string]any{"type": "text_delta", "text": text},
	})
}

// WriteThinkingDelta emits a thinking_delta. Empty text is silently ignored.
func (s *AnthropicSSEWriter) WriteThinkingDelta(thinking string) error {
	if thinking == "" {
		return nil
	}
	if err := s.checkTransition("content_block_delta"); err != nil {
		return err
	}
	if s.blockType != "thinking" {
		return s.setErr(fmt.Errorf("thinking_delta in %s block", s.blockType))
	}
	return s.writeSSE("content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": s.blockIndex,
		"delta": map[string]any{"type": "thinking_delta", "thinking": thinking},
	})
}

// WriteSignatureDelta emits a signature_delta. Empty signature is silently ignored.
func (s *AnthropicSSEWriter) WriteSignatureDelta(signature string) error {
	if signature == "" {
		return nil
	}
	if err := s.checkTransition("content_block_delta"); err != nil {
		return err
	}
	if s.blockType != "thinking" {
		return s.setErr(fmt.Errorf("signature_delta in %s block", s.blockType))
	}
	return s.writeSSE("content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": s.blockIndex,
		"delta": map[string]any{"type": "signature_delta", "signature": signature},
	})
}

// WriteInputJSONDelta emits an input_json_delta. Empty JSON is silently ignored.
func (s *AnthropicSSEWriter) WriteInputJSONDelta(partialJSON string) error {
	if partialJSON == "" {
		return nil
	}
	if err := s.checkTransition("content_block_delta"); err != nil {
		return err
	}
	if s.blockType != "tool_use" {
		return s.setErr(fmt.Errorf("input_json_delta in %s block", s.blockType))
	}
	return s.writeSSE("content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": s.blockIndex,
		"delta": map[string]any{"type": "input_json_delta", "partial_json": partialJSON},
	})
}

// StopContentBlock emits content_block_stop for the current block.
func (s *AnthropicSSEWriter) StopContentBlock() error {
	if err := s.checkTransition("content_block_stop"); err != nil {
		return err
	}
	err := s.writeSSE("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": s.blockIndex,
	})
	if err == nil {
		s.state = StateBetweenBlocks
		s.blockType = ""
	}
	return err
}

// WriteMessageDelta emits the message_delta event with stop_reason and usage.
func (s *AnthropicSSEWriter) WriteMessageDelta(stopReason string, outputTokens int) error {
	if err := s.checkTransition("message_delta"); err != nil {
		return err
	}
	err := s.writeSSE("message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": stopReason, "stop_sequence": nil},
		"usage": map[string]any{"output_tokens": outputTokens},
	})
	if err == nil {
		s.state = StateMessageDelta
	}
	return err
}

// WriteMessageStop emits the final message_stop event.
func (s *AnthropicSSEWriter) WriteMessageStop() error {
	if err := s.checkTransition("message_stop"); err != nil {
		return err
	}
	err := s.writeSSE("message_stop", map[string]any{"type": "message_stop"})
	if err == nil {
		s.state = StateMessageStopped
	}
	return err
}

// WritePing emits a ping event. Allowed in any non-terminal state.
func (s *AnthropicSSEWriter) WritePing() error {
	if err := s.checkCtx(); err != nil {
		return err
	}
	if s.state.isTerminal() {
		return s.setErr(fmt.Errorf("ping in terminal state %s", s.state))
	}
	return s.writeSSE("ping", map[string]any{"type": "ping"})
}

// WriteError emits an error event and transitions to terminal state.
func (s *AnthropicSSEWriter) WriteError(errType, message string) error {
	if s.state.isTerminal() {
		return s.err
	}
	_ = s.writeSSE("error", map[string]any{
		"type":  "error",
		"error": map[string]any{"type": errType, "message": message},
	})
	s.state = StateError
	if s.err == nil {
		s.mu.Lock()
		s.err = fmt.Errorf("sse error: %s: %s", errType, message)
		s.mu.Unlock()
	}
	return s.err
}

// FinishFromEOF auto-completes any missing closing events when the upstream
// ends normally (io.EOF). Call this after the Kiro event loop exits cleanly.
func (s *AnthropicSSEWriter) FinishFromEOF(stopReason string, outputTokens int) error {
	if s.err != nil {
		return s.err
	}
	if stopReason == "" {
		stopReason = "end_turn"
	}
	// Close open content block if any
	if s.state == StateInContentBlock {
		if err := s.StopContentBlock(); err != nil {
			return err
		}
	}
	// Send message_delta if not sent yet
	if s.state == StateMessageStarted || s.state == StateBetweenBlocks {
		if err := s.WriteMessageDelta(stopReason, outputTokens); err != nil {
			return err
		}
	}
	// Send message_stop
	if s.state == StateMessageDelta {
		return s.WriteMessageStop()
	}
	return nil
}

// --- Feature Flag ---

// SSEStateMachineEnabled returns true if the SSE state machine validation
// is enabled via the SUB2API_KIRO_SSE_STATE_MACHINE environment variable.
func SSEStateMachineEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("SUB2API_KIRO_SSE_STATE_MACHINE")))
	return v == "true" || v == "1" || v == "yes"
}

// --- Validating SSE Middleware ---

// SSEValidator wraps an existing writeEvent function with state machine
// validation. When the feature flag is on, it validates each SSE event
// against the state machine before letting it through. Invalid events
// are logged and dropped (fail-open for compatibility, log for detection).
type SSEValidator struct {
	state      StreamState
	blockIndex int
	blockType  string
	violations int
}

// NewSSEValidator creates a new SSE event validator.
func NewSSEValidator() *SSEValidator {
	return &SSEValidator{
		state:      StateInit,
		blockIndex: -1,
	}
}

// Violations returns the number of protocol violations detected.
func (v *SSEValidator) Violations() int { return v.violations }

// State returns the current state.
func (v *SSEValidator) State() StreamState { return v.state }

// ValidateEvent checks whether emitting the given SSE event is valid in
// the current state. It updates internal state and returns true if valid.
// If invalid, it logs the violation and returns false.
func (v *SSEValidator) ValidateEvent(eventName string, data map[string]any) bool {
	// ping and error are always allowed in non-terminal states
	if eventName == "ping" {
		return !v.state.isTerminal()
	}
	if eventName == "error" {
		v.state = StateError
		return true
	}

	sseType := ""
	if t, ok := data["type"].(string); ok {
		sseType = t
	}

	valid := false
	nextState := v.state

	switch v.state {
	case StateInit:
		if sseType == "message_start" {
			valid = true
			nextState = StateMessageStarted
		}
	case StateMessageStarted:
		if sseType == "content_block_start" {
			valid = true
			nextState = StateInContentBlock
			v.blockIndex++
			if cb, ok := data["content_block"].(map[string]any); ok {
				v.blockType, _ = cb["type"].(string)
			}
			// Validate index
			if idx, ok := data["index"]; ok {
				if idxF, ok2 := idx.(float64); ok2 && int(idxF) != v.blockIndex {
					log.Printf("[SSEValidator] block index mismatch: got %d, expected %d", int(idxF), v.blockIndex)
					v.violations++
				}
			}
		} else if sseType == "message_delta" {
			valid = true
			nextState = StateMessageDelta
		}
	case StateInContentBlock:
		if sseType == "content_block_delta" {
			valid = true
			// Validate delta type matches block type
			if delta, ok := data["delta"].(map[string]any); ok {
				deltaType, _ := delta["type"].(string)
				if !v.isDeltaTypeValid(deltaType) {
					log.Printf("[SSEValidator] delta type %q invalid in %q block", deltaType, v.blockType)
					v.violations++
				}
			}
		} else if sseType == "content_block_stop" {
			valid = true
			nextState = StateBetweenBlocks
			v.blockType = ""
		}
	case StateBetweenBlocks:
		if sseType == "content_block_start" {
			valid = true
			nextState = StateInContentBlock
			v.blockIndex++
			if cb, ok := data["content_block"].(map[string]any); ok {
				v.blockType, _ = cb["type"].(string)
			}
		} else if sseType == "message_delta" {
			valid = true
			nextState = StateMessageDelta
		}
	case StateMessageDelta:
		if sseType == "message_stop" {
			valid = true
			nextState = StateMessageStopped
		}
	}

	if !valid {
		log.Printf("[SSEValidator] illegal transition: %q (type=%q) in state %s", eventName, sseType, v.state)
		v.violations++
		return false
	}

	v.state = nextState
	return true
}

func (v *SSEValidator) isDeltaTypeValid(deltaType string) bool {
	switch v.blockType {
	case "text":
		return deltaType == "text_delta"
	case "thinking":
		return deltaType == "thinking_delta" || deltaType == "signature_delta"
	case "tool_use":
		return deltaType == "input_json_delta"
	}
	return true // unknown block type — don't block
}

package kiro

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSSEWriter_HappyPath_PlainText(t *testing.T) {
	var buf bytes.Buffer
	ctx := context.Background()
	w := NewAnthropicSSEWriter(ctx, &buf, "claude-opus-5", "msg_test_001")

	if err := w.WriteMessageStart(25, 0, 0); err != nil {
		t.Fatalf("WriteMessageStart: %v", err)
	}
	if w.State() != StateMessageStarted {
		t.Fatalf("state = %s, want MessageStarted", w.State())
	}

	if err := w.StartTextBlock(); err != nil {
		t.Fatalf("StartTextBlock: %v", err)
	}
	if w.BlockIndex() != 0 {
		t.Fatalf("blockIndex = %d, want 0", w.BlockIndex())
	}

	if err := w.WriteTextDelta("Hello world"); err != nil {
		t.Fatalf("WriteTextDelta: %v", err)
	}
	if err := w.StopContentBlock(); err != nil {
		t.Fatalf("StopContentBlock: %v", err)
	}
	if err := w.WriteMessageDelta("end_turn", 12); err != nil {
		t.Fatalf("WriteMessageDelta: %v", err)
	}
	if err := w.WriteMessageStop(); err != nil {
		t.Fatalf("WriteMessageStop: %v", err)
	}
	if w.State() != StateMessageStopped {
		t.Fatalf("state = %s, want MessageStopped", w.State())
	}

	output := buf.String()
	for _, want := range []string{
		"event: message_start",
		"event: content_block_start",
		"event: content_block_delta",
		"event: content_block_stop",
		"event: message_delta",
		"event: message_stop",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestSSEWriter_HappyPath_ThinkingThenText(t *testing.T) {
	var buf bytes.Buffer
	w := NewAnthropicSSEWriter(context.Background(), &buf, "claude-opus-5", "msg_test_002")

	_ = w.WriteMessageStart(30, 0, 0)
	_ = w.StartThinkingBlock()
	if w.BlockIndex() != 0 {
		t.Fatalf("thinking blockIndex = %d, want 0", w.BlockIndex())
	}
	_ = w.WriteThinkingDelta("Let me think...")
	_ = w.WriteSignatureDelta("sig_placeholder")
	_ = w.StopContentBlock()

	_ = w.StartTextBlock()
	if w.BlockIndex() != 1 {
		t.Fatalf("text blockIndex = %d, want 1", w.BlockIndex())
	}
	_ = w.WriteTextDelta("The answer is 42.")
	_ = w.StopContentBlock()
	_ = w.WriteMessageDelta("end_turn", 45)
	_ = w.WriteMessageStop()

	if w.Err() != nil {
		t.Fatalf("unexpected error: %v", w.Err())
	}
	output := buf.String()
	if !strings.Contains(output, "thinking_delta") {
		t.Error("missing thinking_delta")
	}
	if !strings.Contains(output, "signature_delta") {
		t.Error("missing signature_delta")
	}
	if !strings.Contains(output, "text_delta") {
		t.Error("missing text_delta")
	}
}

func TestSSEWriter_HappyPath_ToolUse(t *testing.T) {
	var buf bytes.Buffer
	w := NewAnthropicSSEWriter(context.Background(), &buf, "claude-opus-5", "msg_test_003")

	_ = w.WriteMessageStart(50, 0, 0)
	_ = w.StartTextBlock()
	_ = w.WriteTextDelta("Let me check.")
	_ = w.StopContentBlock()
	_ = w.StartToolUseBlock("toolu_001", "get_weather")
	if w.BlockIndex() != 1 {
		t.Fatalf("tool blockIndex = %d, want 1", w.BlockIndex())
	}
	_ = w.WriteInputJSONDelta(`{"location":"Paris"}`)
	_ = w.StopContentBlock()
	_ = w.WriteMessageDelta("tool_use", 35)
	_ = w.WriteMessageStop()

	if w.Err() != nil {
		t.Fatalf("unexpected error: %v", w.Err())
	}
	output := buf.String()
	if !strings.Contains(output, "input_json_delta") {
		t.Error("missing input_json_delta")
	}
	if !strings.Contains(output, `"stop_reason":"tool_use"`) {
		t.Error("missing tool_use stop_reason")
	}
}

func TestSSEWriter_IllegalTransition_DoubleMessageStart(t *testing.T) {
	var buf bytes.Buffer
	w := NewAnthropicSSEWriter(context.Background(), &buf, "claude-opus-5", "msg_test_err")

	_ = w.WriteMessageStart(10, 0, 0)
	err := w.WriteMessageStart(10, 0, 0)
	if err == nil {
		t.Fatal("expected error on double message_start")
	}
	if w.State() != StateError {
		t.Fatalf("state = %s, want Error", w.State())
	}
}

func TestSSEWriter_IllegalTransition_DeltaWithoutBlock(t *testing.T) {
	var buf bytes.Buffer
	w := NewAnthropicSSEWriter(context.Background(), &buf, "claude-opus-5", "msg_test_err2")

	_ = w.WriteMessageStart(10, 0, 0)
	err := w.WriteTextDelta("oops")
	if err == nil {
		t.Fatal("expected error on delta without block")
	}
	if w.State() != StateError {
		t.Fatalf("state = %s, want Error", w.State())
	}
}

func TestSSEWriter_IllegalTransition_WrongDeltaType(t *testing.T) {
	var buf bytes.Buffer
	w := NewAnthropicSSEWriter(context.Background(), &buf, "claude-opus-5", "msg_test_err3")

	_ = w.WriteMessageStart(10, 0, 0)
	_ = w.StartTextBlock()
	err := w.WriteThinkingDelta("wrong block type")
	if err == nil {
		t.Fatal("expected error on thinking_delta in text block")
	}
}

func TestSSEWriter_StickyError(t *testing.T) {
	var buf bytes.Buffer
	w := NewAnthropicSSEWriter(context.Background(), &buf, "claude-opus-5", "msg_test_sticky")

	_ = w.WriteMessageStart(10, 0, 0)
	_ = w.WriteMessageStart(10, 0, 0) // causes error

	// All subsequent calls should return the same error
	err1 := w.StartTextBlock()
	err2 := w.WriteTextDelta("test")
	err3 := w.WriteMessageStop()

	if err1 == nil || err2 == nil || err3 == nil {
		t.Fatal("expected sticky errors")
	}
	if err1 != err2 || err2 != err3 {
		t.Fatal("sticky errors should be identical")
	}
}

func TestSSEWriter_EmptyDeltaIgnored(t *testing.T) {
	var buf bytes.Buffer
	w := NewAnthropicSSEWriter(context.Background(), &buf, "claude-opus-5", "msg_test_empty")

	_ = w.WriteMessageStart(10, 0, 0)
	_ = w.StartTextBlock()
	before := buf.Len()
	_ = w.WriteTextDelta("") // should be ignored
	after := buf.Len()
	if after != before {
		t.Error("empty delta should not emit any event")
	}
	_ = w.StopContentBlock()
	_ = w.WriteMessageDelta("end_turn", 5)
	_ = w.WriteMessageStop()
	if w.Err() != nil {
		t.Fatalf("unexpected error: %v", w.Err())
	}
}

func TestSSEWriter_Ping(t *testing.T) {
	var buf bytes.Buffer
	w := NewAnthropicSSEWriter(context.Background(), &buf, "claude-opus-5", "msg_test_ping")

	_ = w.WriteMessageStart(10, 0, 0)
	_ = w.WritePing()
	_ = w.StartTextBlock()
	_ = w.WritePing() // ping inside block
	_ = w.WriteTextDelta("hi")
	_ = w.StopContentBlock()
	_ = w.WritePing() // ping between blocks
	_ = w.WriteMessageDelta("end_turn", 5)
	_ = w.WriteMessageStop()

	if w.Err() != nil {
		t.Fatalf("unexpected error: %v", w.Err())
	}
	if count := strings.Count(buf.String(), `"type":"ping"`); count != 3 {
		t.Fatalf("ping count = %d, want 3", count)
	}
}

func TestSSEWriter_PingInTerminalState(t *testing.T) {
	var buf bytes.Buffer
	w := NewAnthropicSSEWriter(context.Background(), &buf, "claude-opus-5", "msg_test_ping_err")

	_ = w.WriteMessageStart(10, 0, 0)
	_ = w.WriteMessageDelta("end_turn", 0)
	_ = w.WriteMessageStop()
	err := w.WritePing()
	if err == nil {
		t.Fatal("expected error on ping in terminal state")
	}
}

func TestSSEWriter_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var buf bytes.Buffer
	w := NewAnthropicSSEWriter(ctx, &buf, "claude-opus-5", "msg_test_ctx")

	_ = w.WriteMessageStart(10, 0, 0)
	cancel()
	err := w.StartTextBlock()
	if err == nil {
		t.Fatal("expected error after context cancel")
	}
	if w.State() != StateError {
		t.Fatalf("state = %s, want Error", w.State())
	}
}

func TestSSEWriter_FinishFromEOF(t *testing.T) {
	var buf bytes.Buffer
	w := NewAnthropicSSEWriter(context.Background(), &buf, "claude-opus-5", "msg_test_eof")

	_ = w.WriteMessageStart(10, 0, 0)
	_ = w.StartTextBlock()
	_ = w.WriteTextDelta("partial response")
	// Simulate EOF — block still open, no message_delta/stop sent
	if err := w.FinishFromEOF("end_turn", 20); err != nil {
		t.Fatalf("FinishFromEOF: %v", err)
	}
	if w.State() != StateMessageStopped {
		t.Fatalf("state = %s, want MessageStopped", w.State())
	}
	output := buf.String()
	if !strings.Contains(output, "content_block_stop") {
		t.Error("FinishFromEOF should auto-close block")
	}
	if !strings.Contains(output, "message_delta") {
		t.Error("FinishFromEOF should emit message_delta")
	}
	if !strings.Contains(output, "message_stop") {
		t.Error("FinishFromEOF should emit message_stop")
	}
}

func TestSSEWriter_WriteError(t *testing.T) {
	var buf bytes.Buffer
	w := NewAnthropicSSEWriter(context.Background(), &buf, "claude-opus-5", "msg_test_werr")

	_ = w.WriteMessageStart(10, 0, 0)
	_ = w.WriteError("overloaded_error", "Overloaded")

	if w.State() != StateError {
		t.Fatalf("state = %s, want Error", w.State())
	}
	output := buf.String()
	if !strings.Contains(output, "overloaded_error") {
		t.Error("missing error event")
	}
}

func TestSSEWriter_BlockIndexMonotonic(t *testing.T) {
	var buf bytes.Buffer
	w := NewAnthropicSSEWriter(context.Background(), &buf, "claude-opus-5", "msg_test_idx")

	_ = w.WriteMessageStart(10, 0, 0)

	for i := 0; i < 5; i++ {
		_ = w.StartTextBlock()
		if w.BlockIndex() != i {
			t.Fatalf("iteration %d: blockIndex = %d, want %d", i, w.BlockIndex(), i)
		}
		_ = w.WriteTextDelta("chunk")
		_ = w.StopContentBlock()
	}

	_ = w.WriteMessageDelta("end_turn", 50)
	_ = w.WriteMessageStop()
	if w.Err() != nil {
		t.Fatalf("unexpected error: %v", w.Err())
	}
}

func TestSSEWriter_MessageStartJSON(t *testing.T) {
	var buf bytes.Buffer
	w := NewAnthropicSSEWriter(context.Background(), &buf, "claude-opus-5", "msg_test_json")

	_ = w.WriteMessageStart(25, 10, 5)

	output := buf.String()
	// Extract JSON from the SSE data line
	lines := strings.Split(output, "\n")
	var dataLine string
	for _, l := range lines {
		if strings.HasPrefix(l, "data: ") {
			dataLine = strings.TrimPrefix(l, "data: ")
			break
		}
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(dataLine), &parsed); err != nil {
		t.Fatalf("parse data JSON: %v", err)
	}
	msg := parsed["message"].(map[string]any)
	if msg["model"] != "claude-opus-5" {
		t.Errorf("model = %v", msg["model"])
	}
	if msg["id"] != "msg_test_json" {
		t.Errorf("id = %v", msg["id"])
	}
	u := msg["usage"].(map[string]any)
	if u["input_tokens"] != float64(25) {
		t.Errorf("input_tokens = %v", u["input_tokens"])
	}
	if u["cache_creation_input_tokens"] != float64(10) {
		t.Errorf("cache_creation = %v", u["cache_creation_input_tokens"])
	}
	if u["cache_read_input_tokens"] != float64(5) {
		t.Errorf("cache_read = %v", u["cache_read_input_tokens"])
	}
}

// --- SSEValidator tests ---

func TestSSEValidator_HappyPath(t *testing.T) {
	v := NewSSEValidator()

	events := []struct {
		event string
		data  map[string]any
	}{
		{"message_start", map[string]any{"type": "message_start"}},
		{"content_block_start", map[string]any{"type": "content_block_start", "index": float64(0), "content_block": map[string]any{"type": "text"}}},
		{"content_block_delta", map[string]any{"type": "content_block_delta", "index": float64(0), "delta": map[string]any{"type": "text_delta"}}},
		{"content_block_stop", map[string]any{"type": "content_block_stop", "index": float64(0)}},
		{"message_delta", map[string]any{"type": "message_delta"}},
		{"message_stop", map[string]any{"type": "message_stop"}},
	}

	for i, e := range events {
		if !v.ValidateEvent(e.event, e.data) {
			t.Fatalf("step %d: event %q rejected in state %s", i, e.event, v.State())
		}
	}
	if v.Violations() != 0 {
		t.Fatalf("violations = %d, want 0", v.Violations())
	}
	if v.State() != StateMessageStopped {
		t.Fatalf("state = %s, want MessageStopped", v.State())
	}
}

func TestSSEValidator_IllegalTransition(t *testing.T) {
	v := NewSSEValidator()
	// Try delta before message_start
	if v.ValidateEvent("content_block_delta", map[string]any{"type": "content_block_delta"}) {
		t.Fatal("expected rejection")
	}
	if v.Violations() != 1 {
		t.Fatalf("violations = %d, want 1", v.Violations())
	}
}

func TestSSEValidator_DeltaTypeMismatch(t *testing.T) {
	v := NewSSEValidator()
	v.ValidateEvent("message_start", map[string]any{"type": "message_start"})
	v.ValidateEvent("content_block_start", map[string]any{"type": "content_block_start", "index": float64(0), "content_block": map[string]any{"type": "text"}})
	// Send thinking_delta in text block — should count as violation but still pass (fail-open)
	ok := v.ValidateEvent("content_block_delta", map[string]any{"type": "content_block_delta", "delta": map[string]any{"type": "thinking_delta"}})
	if !ok {
		t.Fatal("delta should still pass through (fail-open for compat)")
	}
	if v.Violations() != 1 {
		t.Fatalf("violations = %d, want 1", v.Violations())
	}
}

func TestSSEValidator_PingAllowed(t *testing.T) {
	v := NewSSEValidator()
	if !v.ValidateEvent("ping", map[string]any{"type": "ping"}) {
		t.Fatal("ping should be allowed in Init")
	}
	v.ValidateEvent("message_start", map[string]any{"type": "message_start"})
	if !v.ValidateEvent("ping", map[string]any{"type": "ping"}) {
		t.Fatal("ping should be allowed in MessageStarted")
	}
}

func TestSSEValidator_FeatureFlag(t *testing.T) {
	// Default should be off
	if SSEStateMachineEnabled() {
		t.Fatal("SSE state machine should be off by default")
	}
}
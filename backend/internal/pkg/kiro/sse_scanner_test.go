package kiro

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSSEScanner_SingleEvent(t *testing.T) {
	input := `event: message_start
data: {"type":"message_start","message":{"id":"msg_123"}}

`
	scanner := NewSSEScanner(strings.NewReader(input))

	require.True(t, scanner.Scan())
	assert.Equal(t, "message_start", scanner.Event())
	assert.JSONEq(t, `{"type":"message_start","message":{"id":"msg_123"}}`, string(scanner.Data()))

	assert.False(t, scanner.Scan())
	assert.NoError(t, scanner.Err())
}

func TestSSEScanner_MultipleEvents(t *testing.T) {
	input := `event: message_start
data: {"type":"message_start"}

event: content_block_start
data: {"type":"content_block_start","index":0}

event: content_block_delta
data: {"type":"content_block_delta","delta":{"text":"Hello"}}

`
	scanner := NewSSEScanner(strings.NewReader(input))

	// First event
	require.True(t, scanner.Scan())
	assert.Equal(t, "message_start", scanner.Event())
	assert.JSONEq(t, `{"type":"message_start"}`, string(scanner.Data()))

	// Second event
	require.True(t, scanner.Scan())
	assert.Equal(t, "content_block_start", scanner.Event())
	assert.JSONEq(t, `{"type":"content_block_start","index":0}`, string(scanner.Data()))

	// Third event
	require.True(t, scanner.Scan())
	assert.Equal(t, "content_block_delta", scanner.Event())
	assert.JSONEq(t, `{"type":"content_block_delta","delta":{"text":"Hello"}}`, string(scanner.Data()))

	assert.False(t, scanner.Scan())
	assert.NoError(t, scanner.Err())
}

func TestSSEScanner_MultilineData(t *testing.T) {
	input := `event: test
data: line1
data: line2
data: line3

`
	scanner := NewSSEScanner(strings.NewReader(input))

	require.True(t, scanner.Scan())
	assert.Equal(t, "test", scanner.Event())
	assert.Equal(t, "line1\nline2\nline3", string(scanner.Data()))

	assert.False(t, scanner.Scan())
}

func TestSSEScanner_NoEventType(t *testing.T) {
	input := `data: {"type":"ping"}

`
	scanner := NewSSEScanner(strings.NewReader(input))

	require.True(t, scanner.Scan())
	assert.Equal(t, "", scanner.Event()) // No event type specified
	assert.JSONEq(t, `{"type":"ping"}`, string(scanner.Data()))

	assert.False(t, scanner.Scan())
}

func TestSSEScanner_Comments(t *testing.T) {
	input := `: This is a comment
event: test
: Another comment
data: {"value":123}

`
	scanner := NewSSEScanner(strings.NewReader(input))

	require.True(t, scanner.Scan())
	assert.Equal(t, "test", scanner.Event())
	assert.JSONEq(t, `{"value":123}`, string(scanner.Data()))

	assert.False(t, scanner.Scan())
}

func TestSSEScanner_EmptyLines(t *testing.T) {
	input := `

event: first
data: {"a":1}


event: second
data: {"b":2}

`
	scanner := NewSSEScanner(strings.NewReader(input))

	// First event
	require.True(t, scanner.Scan())
	assert.Equal(t, "first", scanner.Event())
	assert.JSONEq(t, `{"a":1}`, string(scanner.Data()))

	// Second event
	require.True(t, scanner.Scan())
	assert.Equal(t, "second", scanner.Event())
	assert.JSONEq(t, `{"b":2}`, string(scanner.Data()))

	assert.False(t, scanner.Scan())
}

func TestSSEScanner_SpaceAfterColon(t *testing.T) {
	input := `event:message_start
data:{"type":"message_start"}

event: content_block_start
data: {"type":"content_block_start"}

`
	scanner := NewSSEScanner(strings.NewReader(input))

	// First event (no space after colon)
	require.True(t, scanner.Scan())
	assert.Equal(t, "message_start", scanner.Event())
	assert.JSONEq(t, `{"type":"message_start"}`, string(scanner.Data()))

	// Second event (space after colon)
	require.True(t, scanner.Scan())
	assert.Equal(t, "content_block_start", scanner.Event())
	assert.JSONEq(t, `{"type":"content_block_start"}`, string(scanner.Data()))

	assert.False(t, scanner.Scan())
}

func TestSSEScanner_EmptyData(t *testing.T) {
	input := `event: empty

`
	scanner := NewSSEScanner(strings.NewReader(input))

	require.True(t, scanner.Scan())
	assert.Equal(t, "empty", scanner.Event())
	assert.Nil(t, scanner.Data())

	assert.False(t, scanner.Scan())
}

func TestFormatSSE_SimpleEvent(t *testing.T) {
	result := FormatSSE("message_start", []byte(`{"type":"message_start"}`))
	expected := "event: message_start\ndata: {\"type\":\"message_start\"}\n\n"
	assert.Equal(t, expected, string(result))
}

func TestFormatSSE_NoEventType(t *testing.T) {
	result := FormatSSE("", []byte(`{"type":"ping"}`))
	expected := "data: {\"type\":\"ping\"}\n\n"
	assert.Equal(t, expected, string(result))
}

func TestFormatSSE_MultilineData(t *testing.T) {
	result := FormatSSE("test", []byte("line1\nline2\nline3"))
	expected := "event: test\ndata: line1\ndata: line2\ndata: line3\n\n"
	assert.Equal(t, expected, string(result))
}

func TestFormatSSE_RoundTrip(t *testing.T) {
	// Format an event and then parse it back
	originalEvent := "content_block_delta"
	originalData := []byte(`{"delta":{"text":"Hello, world!"}}`)

	formatted := FormatSSE(originalEvent, originalData)

	scanner := NewSSEScanner(bytes.NewReader(formatted))
	require.True(t, scanner.Scan())

	assert.Equal(t, originalEvent, scanner.Event())
	assert.JSONEq(t, string(originalData), string(scanner.Data()))

	assert.False(t, scanner.Scan())
	assert.NoError(t, scanner.Err())
}

func TestSSEEvent_String(t *testing.T) {
	tests := []struct {
		name     string
		event    SSEEvent
		expected string
	}{
		{
			name:     "simple event",
			event:    SSEEvent{Event: "test", Data: []byte(`{"key":"value"}`)},
			expected: `SSEEvent{event="test", data="{\"key\":\"value\"}"}`,
		},
		{
			name:     "unnamed event",
			event:    SSEEvent{Event: "", Data: []byte(`{"type":"ping"}`)},
			expected: `SSEEvent{event="(unnamed)", data="{\"type\":\"ping\"}"}`,
		},
		{
			name:     "long data truncated",
			event:    SSEEvent{Event: "long", Data: []byte(strings.Repeat("a", 100))},
			expected: `SSEEvent{event="long", data="` + strings.Repeat("a", 77) + `..."}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.event.String()
			assert.Equal(t, tt.expected, result)
		})
	}
}

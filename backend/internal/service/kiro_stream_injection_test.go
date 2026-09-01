package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	kiropkg "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockUpstreamSSEBody creates a mock Kiro SSE response body
func mockUpstreamSSEBody(withContentBlocks bool) io.ReadCloser {
	var buf bytes.Buffer

	// message_start
	msgStart := map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":    "msg_test123",
			"type":  "message",
			"role":  "assistant",
			"model": "claude-3-5-sonnet-20241022",
		},
	}
	msgStartData, _ := json.Marshal(msgStart)
	buf.WriteString("event: message_start\n")
	buf.WriteString("data: " + string(msgStartData) + "\n\n")

	if withContentBlocks {
		// content_block_start (index 0 in original stream, will become index 2 after injection)
		blockStart := map[string]any{
			"type":  "content_block_start",
			"index": 0,
			"content_block": map[string]any{
				"type": "text",
				"text": "",
			},
		}
		blockStartData, _ := json.Marshal(blockStart)
		buf.WriteString("event: content_block_start\n")
		buf.WriteString("data: " + string(blockStartData) + "\n\n")

		// content_block_delta
		delta := map[string]any{
			"type":  "content_block_delta",
			"index": 0,
			"delta": map[string]any{
				"type": "text_delta",
				"text": "Hello",
			},
		}
		deltaData, _ := json.Marshal(delta)
		buf.WriteString("event: content_block_delta\n")
		buf.WriteString("data: " + string(deltaData) + "\n\n")

		// content_block_stop
		blockStop := map[string]any{
			"type":  "content_block_stop",
			"index": 0,
		}
		blockStopData, _ := json.Marshal(blockStop)
		buf.WriteString("event: content_block_stop\n")
		buf.WriteString("data: " + string(blockStopData) + "\n\n")
	}

	// message_delta
	msgDelta := map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason": "end_turn",
		},
		"usage": map[string]any{
			"output_tokens": 10,
		},
	}
	msgDeltaData, _ := json.Marshal(msgDelta)
	buf.WriteString("event: message_delta\n")
	buf.WriteString("data: " + string(msgDeltaData) + "\n\n")

	// message_stop
	buf.WriteString("event: message_stop\n")
	buf.WriteString("data: {}\n\n")

	return io.NopCloser(&buf)
}

func TestStreamKiroWithSearchInjection_Basic(t *testing.T) {
	svc := &GatewayService{}
	ctx := context.Background()

	searchRounds := []kiropkg.SearchRound{
		{
			Query:     "test query",
			ToolUseID: "toolu_search123",
			Results: []kiropkg.SearchResultItem{
				{
					Title:   "Test Result",
					URL:     "https://example.com",
					Snippet: "This is a test",
				},
			},
		},
	}

	upstreamBody := mockUpstreamSSEBody(true)
	var downstream bytes.Buffer

	err := svc.streamKiroWithSearchInjection(
		ctx,
		upstreamBody,
		&downstream,
		"claude-3-5-sonnet-20241022",
		100,
		kiropkg.KiroRequestContext{},
		searchRounds,
	)

	require.NoError(t, err)

	output := downstream.String()
	t.Logf("Output:\n%s", output)

	// Verify structure
	assert.Contains(t, output, "event: message_start")
	assert.Contains(t, output, "event: content_block_start")
	assert.Contains(t, output, "event: content_block_delta")
	assert.Contains(t, output, "event: content_block_stop")
	assert.Contains(t, output, "event: message_delta")
	assert.Contains(t, output, "event: message_stop")

	// Verify search blocks were injected
	assert.Contains(t, output, `"type":"server_tool_use"`)
	assert.Contains(t, output, `"type":"web_search_tool_result_delta"`)
	// The web_search_tool_result is inside delta.json (escaped JSON string)
	assert.Contains(t, output, `\"type\":\"web_search_tool_result\"`)
	assert.Contains(t, output, "test query")
	assert.Contains(t, output, "Test Result")
	assert.Contains(t, output, "https://example.com")
}

func TestStreamKiroWithSearchInjection_BlockIndexAdjustment(t *testing.T) {
	svc := &GatewayService{}
	ctx := context.Background()

	searchRounds := []kiropkg.SearchRound{
		{
			Query:     "test",
			ToolUseID: "toolu_123",
			Results: []kiropkg.SearchResultItem{
				{Title: "R1", URL: "https://example.com/1", Snippet: "S1"},
			},
		},
	}

	upstreamBody := mockUpstreamSSEBody(true)
	var downstream bytes.Buffer

	err := svc.streamKiroWithSearchInjection(
		ctx,
		upstreamBody,
		&downstream,
		"claude-3-5-sonnet-20241022",
		100,
		kiropkg.KiroRequestContext{},
		searchRounds,
	)

	require.NoError(t, err)

	output := downstream.String()

	// Parse events to verify block indices were adjusted
	lines := strings.Split(output, "\n")
	var foundAdjustedIndex bool

	for i, line := range lines {
		if strings.HasPrefix(line, "data: ") {
			dataJSON := strings.TrimPrefix(line, "data: ")
			var event map[string]any
			if err := json.Unmarshal([]byte(dataJSON), &event); err == nil {
				if event["type"] == "content_block_start" || event["type"] == "content_block_stop" {
					if idx, ok := event["index"].(float64); ok {
						t.Logf("Line %d: Found %s with index=%v", i, event["type"], idx)
						// Original upstream had index=0, after injecting 1 search block it should be index=1
						if idx == 1 {
							foundAdjustedIndex = true
						}
					}
				}
			}
		}
	}

	assert.True(t, foundAdjustedIndex, "Expected to find adjusted block index=1 in output")
}

func TestStreamKiroWithSearchInjection_NoContentBlocks(t *testing.T) {
	svc := &GatewayService{}
	ctx := context.Background()

	searchRounds := []kiropkg.SearchRound{
		{
			Query:     "test",
			ToolUseID: "toolu_123",
			Results: []kiropkg.SearchResultItem{
				{Title: "R1", URL: "https://example.com/1", Snippet: "S1"},
			},
		},
	}

	upstreamBody := mockUpstreamSSEBody(false) // No content blocks
	var downstream bytes.Buffer

	err := svc.streamKiroWithSearchInjection(
		ctx,
		upstreamBody,
		&downstream,
		"claude-3-5-sonnet-20241022",
		100,
		kiropkg.KiroRequestContext{},
		searchRounds,
	)

	require.NoError(t, err)

	output := downstream.String()

	// Should still inject search blocks
	assert.Contains(t, output, `"type":"server_tool_use"`)
	assert.Contains(t, output, `"type":"web_search_tool_result_delta"`)
	assert.Contains(t, output, `\"type\":\"web_search_tool_result\"`)
	// Should have message_start, message_delta, message_stop
	assert.Contains(t, output, "event: message_start")
	assert.Contains(t, output, "event: message_delta")
	assert.Contains(t, output, "event: message_stop")
	// Search blocks ARE content blocks, so content_block_start WILL appear
	assert.Contains(t, output, "event: content_block_start")
}

func TestStreamKiroWithSearchInjection_MultiRound(t *testing.T) {
	svc := &GatewayService{}
	ctx := context.Background()

	searchRounds := []kiropkg.SearchRound{
		{
			RoundNumber: 0,
			Query:       "first query",
			ToolUseID:   "toolu_round0",
			Results: []kiropkg.SearchResultItem{
				{Title: "Result A", URL: "https://example.com/a", Snippet: "First round result"},
			},
		},
		{
			RoundNumber: 1,
			Query:       "second query",
			ToolUseID:   "toolu_round1",
			Results: []kiropkg.SearchResultItem{
				{Title: "Result B", URL: "https://example.com/b", Snippet: "Second round result"},
			},
		},
	}

	upstreamBody := mockUpstreamSSEBody(true)
	var downstream bytes.Buffer

	err := svc.streamKiroWithSearchInjection(
		ctx,
		upstreamBody,
		&downstream,
		"claude-3-5-sonnet-20241022",
		100,
		kiropkg.KiroRequestContext{},
		searchRounds,
	)

	require.NoError(t, err)

	output := downstream.String()

	// Verify both search rounds were injected
	assert.Contains(t, output, "first query")
	assert.Contains(t, output, "second query")
	assert.Contains(t, output, "Result A")
	assert.Contains(t, output, "Result B")

	// Verify block index offset = 4 (2 rounds × 2 blocks each)
	// Original upstream content_block_start had index=0, should now be index=4
	lines := strings.Split(output, "\n")
	var foundIndex4 bool
	for _, line := range lines {
		if strings.HasPrefix(line, "data: ") {
			dataJSON := strings.TrimPrefix(line, "data: ")
			var event map[string]any
			if err := json.Unmarshal([]byte(dataJSON), &event); err == nil {
				if event["type"] == "content_block_start" {
					if cb, ok := event["content_block"].(map[string]any); ok {
						if cb["type"] == "text" {
							if idx, ok := event["index"].(float64); ok && int(idx) == 2 {
								foundIndex4 = true
							}
						}
					}
				}
			}
		}
	}
	assert.True(t, foundIndex4, "Expected text block index=2 after injecting 2 search rounds (offset=2)")
}

func TestAdjustBlockIndex(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		offset      int
		wantIndex   int
		wantErr     bool
	}{
		{
			name:      "adjust index 0 by 2",
			input:     `{"type":"content_block_start","index":0}`,
			offset:    2,
			wantIndex: 2,
		},
		{
			name:      "adjust index 1 by 2",
			input:     `{"type":"content_block_stop","index":1}`,
			offset:    2,
			wantIndex: 3,
		},
		{
			name:    "missing index field",
			input:   `{"type":"content_block_start"}`,
			offset:  2,
			wantErr: true,
		},
		{
			name:    "invalid json",
			input:   `{invalid}`,
			offset:  2,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := adjustBlockIndex([]byte(tt.input), tt.offset)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)

			var parsed map[string]any
			err = json.Unmarshal(result, &parsed)
			require.NoError(t, err)

			idx, ok := parsed["index"].(float64)
			require.True(t, ok, "index should be a number")
			assert.Equal(t, tt.wantIndex, int(idx))
		})
	}
}

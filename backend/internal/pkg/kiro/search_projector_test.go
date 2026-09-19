package kiro

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSearchProjector_ProjectSearchRound(t *testing.T) {
	tests := []struct {
		name      string
		round     SearchRound
		wantQuery string
		wantURL   string
		wantErr   bool
	}{
		{
			name: "single result with all fields",
			round: SearchRound{
				RoundNumber: 1,
				Query:       "golang testing",
				ToolUseID:   "srvtoolu_test123",
				Results: []SearchResultItem{
					{
						Title:   "Go Testing Guide",
						URL:     "https://example.com/guide",
						Snippet: "Complete guide to testing in Go",
						PageAge: "2 days ago",
					},
				},
				Source:   SearchSourceMCP,
				Outcome:  SearchOutcomeDone,
				Duration: 100 * time.Millisecond,
			},
			wantQuery: "golang testing",
			wantURL:   "https://example.com/guide",
			wantErr:   false,
		},
		{
			name: "empty results",
			round: SearchRound{
				RoundNumber: 1,
				Query:       "nonexistent query",
				ToolUseID:   "srvtoolu_empty",
				Results:     []SearchResultItem{},
				Source:      SearchSourceMCP,
				Outcome:     SearchOutcomeEmpty,
				Duration:    50 * time.Millisecond,
			},
			wantQuery: "nonexistent query",
			wantErr:   false,
		},
		{
			name: "multiple results without page_age",
			round: SearchRound{
				RoundNumber: 2,
				Query:       "test query",
				ToolUseID:   "srvtoolu_multi",
				Results: []SearchResultItem{
					{
						Title:   "Result 1",
						URL:     "https://example.com/1",
						Snippet: "First result",
					},
					{
						Title:   "Result 2",
						URL:     "https://example.com/2",
						Snippet: "Second result",
					},
				},
				Source:   SearchSourceGateway,
				Outcome:  SearchOutcomeContinue,
				Duration: 200 * time.Millisecond,
			},
			wantQuery: "test query",
			wantURL:   "https://example.com/1",
			wantErr:   false,
		},
		{
			name: "query sanitization",
			round: SearchRound{
				RoundNumber: 1,
				Query:       "test\nwith\x00control\rchars",
				ToolUseID:   "srvtoolu_sanitize",
				Results:     []SearchResultItem{},
				Source:      SearchSourceMCP,
				Outcome:     SearchOutcomeDone,
			},
			wantQuery: "testwithcontrolchars",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			ctx := context.Background()
			writer := NewAnthropicSSEWriter(ctx, &buf, "claude-opus-4", "req_test")
			allocator := NewSearchToolIDAllocator()
			projector := NewSearchProjector(writer, allocator)

			// Start message first (SSE state machine requirement)
			if err := writer.WriteMessageStart(100, 0, 0); err != nil {
				t.Fatalf("WriteMessageStart failed: %v", err)
			}

			// Project the search round
			toolID, err := projector.ProjectSearchRound(tt.round)
			if (err != nil) != tt.wantErr {
				t.Errorf("ProjectSearchRound() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if err != nil {
				return
			}

			// Verify tool ID matches round's tool ID
			if toolID != tt.round.ToolUseID {
				t.Errorf("tool ID = %q, want %q", toolID, tt.round.ToolUseID)
			}

			// Parse SSE events
			output := buf.String()
			events := parseSSEEvents(output)

			// Verify we have content_block_start, content_block_delta, content_block_stop
			var foundStart, foundDelta, foundStop bool
			var deltaPayload map[string]any

			for _, evt := range events {
				switch evt["type"] {
				case "content_block_start":
					foundStart = true
					cb := evt["content_block"].(map[string]any)
					if cb["type"] != "server_tool_use" {
						t.Errorf("content_block type = %v, want server_tool_use", cb["type"])
					}
					if cb["name"] != "web_search" {
						t.Errorf("tool name = %v, want web_search", cb["name"])
					}
				case "content_block_delta":
					foundDelta = true
					delta := evt["delta"].(map[string]any)
					if delta["type"] != "web_search_tool_result_delta" {
						t.Errorf("delta type = %v, want web_search_tool_result_delta", delta["type"])
					}
					// Parse the JSON payload
					jsonStr := delta["json"].(string)
					if err := json.Unmarshal([]byte(jsonStr), &deltaPayload); err != nil {
						t.Fatalf("failed to parse delta json: %v", err)
					}
				case "content_block_stop":
					foundStop = true
				}
			}

			if !foundStart {
				t.Error("missing content_block_start event")
			}
			if !foundDelta {
				t.Error("missing content_block_delta event")
			}
			if !foundStop {
				t.Error("missing content_block_stop event")
			}

			// Verify payload structure
			if deltaPayload["type"] != "web_search_tool_result" {
				t.Errorf("payload type = %v, want web_search_tool_result", deltaPayload["type"])
			}
			if deltaPayload["tool_use_id"] != tt.round.ToolUseID {
				t.Errorf("payload tool_use_id = %v, want %v", deltaPayload["tool_use_id"], tt.round.ToolUseID)
			}
			if deltaPayload["search_query"] != tt.wantQuery {
				t.Errorf("payload search_query = %v, want %v", deltaPayload["search_query"], tt.wantQuery)
			}

			// Verify results array
			if len(tt.round.Results) > 0 {
				results, ok := deltaPayload["results"].([]any)
				if !ok {
					t.Fatal("payload missing results array")
				}
				if len(results) != len(tt.round.Results) {
					t.Errorf("results count = %d, want %d", len(results), len(tt.round.Results))
				}
				if len(results) > 0 {
					firstResult := results[0].(map[string]any)
					if firstResult["url"] != tt.wantURL {
						t.Errorf("first result URL = %v, want %v", firstResult["url"], tt.wantURL)
					}
				}
			}
		})
	}
}

func TestSearchProjector_FallbackToolIDAllocation(t *testing.T) {
	var buf bytes.Buffer
	ctx := context.Background()
	writer := NewAnthropicSSEWriter(ctx, &buf, "claude-opus-4", "req_fallback")
	allocator := NewSearchToolIDAllocator()
	projector := NewSearchProjector(writer, allocator)

	// Start message first
	if err := writer.WriteMessageStart(100, 0, 0); err != nil {
		t.Fatalf("WriteMessageStart failed: %v", err)
	}

	// Round with empty ToolUseID — projector should allocate
	round := SearchRound{
		RoundNumber: 1,
		Query:       "fallback test",
		ToolUseID:   "", // empty
		Results:     []SearchResultItem{},
		Source:      SearchSourceMCP,
		Outcome:     SearchOutcomeDone,
	}

	toolID, err := projector.ProjectSearchRound(round)
	if err != nil {
		t.Fatalf("ProjectSearchRound failed: %v", err)
	}

	if !strings.HasPrefix(toolID, "srvtoolu_") {
		t.Errorf("allocated tool ID = %q, want prefix srvtoolu_", toolID)
	}
}

// parseSSEEvents parses SSE output into structured events.
func parseSSEEvents(output string) []map[string]any {
	var events []map[string]any
	lines := strings.Split(output, "\n")
	var currentData string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data: ") {
			currentData = strings.TrimPrefix(line, "data: ")
		} else if line == "" && currentData != "" {
			var evt map[string]any
			if err := json.Unmarshal([]byte(currentData), &evt); err == nil {
				events = append(events, evt)
			}
			currentData = ""
		}
	}
	return events
}

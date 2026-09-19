package kiro

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SearchProjector projects SearchRound into Anthropic streaming blocks.
type SearchProjector struct {
	writer    *AnthropicSSEWriter
	allocator *SearchToolIDAllocator
}

// NewSearchProjector creates a new search result projector.
func NewSearchProjector(writer *AnthropicSSEWriter, allocator *SearchToolIDAllocator) *SearchProjector {
	return &SearchProjector{
		writer:    writer,
		allocator: allocator,
	}
}

// ProjectSearchRound emits server_tool_use and web_search_tool_result blocks
// for the given SearchRound. Returns the tool_use_id and any error.
func (p *SearchProjector) ProjectSearchRound(round SearchRound) (string, error) {
	// Use round's existing tool ID (already allocated by orchestrator)
	toolID := round.ToolUseID
	if toolID == "" {
		// Fallback: allocate if missing
		toolID = p.allocator.Next()
	}

	// Start server_tool_use block
	if err := p.writer.StartServerToolUseBlock(toolID, "web_search"); err != nil {
		return "", fmt.Errorf("start server_tool_use: %w", err)
	}

	// Build web_search_tool_result payload
	resultPayload := map[string]any{
		"type":         "web_search_tool_result",
		"tool_use_id":  toolID,
		"search_query": sanitizeQuery(round.Query),
	}

	// Add results array if non-empty
	if len(round.Results) > 0 {
		results := make([]map[string]any, len(round.Results))
		for i, r := range round.Results {
			results[i] = map[string]any{
				"url":     r.URL,
				"title":   r.Title,
				"snippet": r.Snippet,
			}
			// Optional page_age
			if r.PageAge != "" {
				results[i]["page_age"] = r.PageAge
			}
		}
		resultPayload["results"] = results
	}

	// Serialize to JSON
	resultJSON, err := json.Marshal(resultPayload)
	if err != nil {
		return "", fmt.Errorf("marshal web_search_tool_result: %w", err)
	}

	// Emit delta (even though it's complete — protocol symmetry)
	if err := p.writer.WriteWebSearchToolResultDelta(string(resultJSON)); err != nil {
		return "", fmt.Errorf("write web_search_tool_result_delta: %w", err)
	}

	// Stop the server_tool_use block
	if err := p.writer.StopContentBlock(); err != nil {
		return "", fmt.Errorf("stop server_tool_use: %w", err)
	}

	return toolID, nil
}

// sanitizeQuery removes control characters and truncates to 500 chars.
func sanitizeQuery(q string) string {
	q = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7F {
			return -1
		}
		return r
	}, q)
	if len(q) > 500 {
		return q[:500]
	}
	return q
}

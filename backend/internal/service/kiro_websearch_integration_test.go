package service

import (
	"encoding/json"
	"net/http"
	"testing"

	kiropkg "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInjectSearchContextIntoBody(t *testing.T) {
	tests := []struct {
		name     string
		body     []byte
		round    kiropkg.SearchRound
		wantJSON string // expected system field value
	}{
		{
			name: "inject into string system",
			body: []byte(`{"model":"claude-3-5-sonnet-20241022","system":"You are helpful.","messages":[{"role":"user","content":"Hello"}]}`),
			round: kiropkg.SearchRound{
				Query: "test query",
				Results: []kiropkg.SearchResultItem{
					{Title: "Result 1", URL: "https://example.com/1", Snippet: "snippet 1"},
					{Title: "Result 2", URL: "https://example.com/2"},
				},
			},
			wantJSON: `"You are helpful.\n\n<search_results>\nQuery: test query\n\n1. Result 1\n   URL: https://example.com/1\n   snippet 1\n\n2. Result 2\n   URL: https://example.com/2\n\n</search_results>"`,
		},
		{
			name: "inject into nil system",
			body: []byte(`{"model":"claude-3-5-sonnet-20241022","messages":[{"role":"user","content":"Hello"}]}`),
			round: kiropkg.SearchRound{
				Query: "test",
				Results: []kiropkg.SearchResultItem{
					{Title: "Result", URL: "https://example.com"},
				},
			},
			wantJSON: `"\n\n<search_results>\nQuery: test\n\n1. Result\n   URL: https://example.com\n\n</search_results>"`,
		},
		{
			name: "empty results - no injection",
			body: []byte(`{"model":"claude-3-5-sonnet-20241022","system":"Original.","messages":[{"role":"user","content":"Hello"}]}`),
			round: kiropkg.SearchRound{
				Query:   "test",
				Results: []kiropkg.SearchResultItem{},
			},
			wantJSON: `"Original."`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := injectSearchContextIntoBody(tt.body, tt.round)
			var parsed map[string]any
			require.NoError(t, json.Unmarshal(result, &parsed))

			systemBytes, err := json.Marshal(parsed["system"])
			require.NoError(t, err)
			assert.JSONEq(t, tt.wantJSON, string(systemBytes))
		})
	}
}
func TestIsKiroServerToolsWebSearchEnabled(t *testing.T) {
	tests := []struct {
		name    string
		headers http.Header
		want    bool
	}{
		{
			name: "enabled",
			headers: http.Header{
				"X-Kiro-Enable-Websearch": []string{"server-tools"},
			},
			want: true,
		},
		{
			name: "case insensitive header value",
			headers: http.Header{
				"X-Kiro-Enable-Websearch": []string{"Server-Tools"},
			},
			want: true,
		},
		{
			name: "wrong value",
			headers: http.Header{
				"X-Kiro-Enable-Websearch": []string{"client-tools"},
			},
			want: false,
		},
		{
			name:    "missing header",
			headers: http.Header{},
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isKiroServerToolsWebSearchEnabled(tt.headers)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestWebSearchResultConversion tests the data conversion from WebSearchResult to SearchResultItem
func TestWebSearchResultConversion(t *testing.T) {
	tests := []struct {
		name        string
		webResults  []kiropkg.WebSearchResult
		wantCount   int
		validateFn  func(t *testing.T, items []kiropkg.SearchResultItem)
	}{
		{
			name: "with snippets",
			webResults: []kiropkg.WebSearchResult{
				{Title: "Result 1", URL: "https://example.com/1", Snippet: strPtr("snippet 1")},
				{Title: "Result 2", URL: "https://example.com/2", Snippet: strPtr("snippet 2")},
			},
			wantCount: 2,
			validateFn: func(t *testing.T, items []kiropkg.SearchResultItem) {
				assert.Equal(t, "Result 1", items[0].Title)
				assert.Equal(t, "https://example.com/1", items[0].URL)
				assert.Equal(t, "snippet 1", items[0].Snippet)
				assert.Equal(t, "Result 2", items[1].Title)
				assert.Equal(t, "snippet 2", items[1].Snippet)
			},
		},
		{
			name: "without snippets",
			webResults: []kiropkg.WebSearchResult{
				{Title: "Result", URL: "https://example.com", Snippet: nil},
			},
			wantCount: 1,
			validateFn: func(t *testing.T, items []kiropkg.SearchResultItem) {
				assert.Equal(t, "Result", items[0].Title)
				assert.Equal(t, "", items[0].Snippet)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate conversion logic from executeKiroMCPWebSearch
			items := make([]kiropkg.SearchResultItem, 0, len(tt.webResults))
			for _, r := range tt.webResults {
				item := kiropkg.SearchResultItem{
					Title: r.Title,
					URL:   r.URL,
				}
				if r.Snippet != nil {
					item.Snippet = *r.Snippet
				}
				items = append(items, item)
			}

			require.Len(t, items, tt.wantCount)
			if tt.validateFn != nil {
				tt.validateFn(t, items)
			}
		})
	}
}

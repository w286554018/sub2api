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

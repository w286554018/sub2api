package service

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestTodoGuardAuxiliaryNeutralInsertionAndIdempotence(t *testing.T) {
	req := &apicompat.ResponsesRequest{Input: json.RawMessage(`[{"type":"message","role":"user","content":"hello"}]`)}
	require.True(t, appendOpenAICompatClaudeCodeTodoGuard(req))
	text := gjson.GetBytes(req.Input, "0.content.0.text").String()
	require.Contains(t, text, "<todo-guard>")
	require.Contains(t, text, "</todo-guard>")
	require.NotContains(t, text, "sub2api")
	first := string(req.Input)
	require.False(t, appendOpenAICompatClaudeCodeTodoGuard(req))
	require.Equal(t, first, string(req.Input))
	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{"input":`+first+`}`), &body))
	require.False(t, appendOpenAICompatClaudeCodeTodoGuardToRequestBody(body))
	require.True(t, isOpenAICompatMessagesBridgeRequestBody(body))
	require.True(t, isOpenAICompatMessagesBridgeBody([]byte(`{"input":`+first+`}`)))
}

func TestTodoGuardAuxiliaryLegacyAndNeutralMarkers(t *testing.T) {
	for _, marker := range []string{"<todo-guard>", "<sub2api-claude-code-todo-guard>"} {
		for _, escaped := range []bool{false, true} {
			name := marker + "/literal"
			if escaped {
				name = marker + "/escaped"
			}
			t.Run(name, func(t *testing.T) {
				rawText := `"` + marker + `"`
				if escaped {
					encoded, err := json.Marshal(marker)
					require.NoError(t, err)
					rawText = string(encoded)
				}
				input := `[{"type":"message","role":"developer","content":[{"type":"input_text","text":` + rawText + `}]},{"type":"message","role":"user","content":"hello"}]`
				req := &apicompat.ResponsesRequest{Input: json.RawMessage(input)}
				require.False(t, appendOpenAICompatClaudeCodeTodoGuard(req))
				require.Equal(t, input, string(req.Input))
				body := []byte(`{"input":` + input + `}`)
				require.True(t, isOpenAICompatMessagesBridgeBody(body))
				var obj map[string]any
				require.NoError(t, json.Unmarshal(body, &obj))
				require.True(t, isOpenAICompatMessagesBridgeRequestBody(obj))
				require.False(t, appendOpenAICompatClaudeCodeTodoGuardToRequestBody(obj))
				require.Len(t, obj["input"], 2)
			})
		}
	}
}

func TestTodoGuardAuxiliaryNonBridgeAndEmptyInputs(t *testing.T) {
	require.False(t, appendOpenAICompatClaudeCodeTodoGuard(nil))
	require.False(t, appendOpenAICompatClaudeCodeTodoGuard(&apicompat.ResponsesRequest{Input: json.RawMessage(`null`)}))
	require.False(t, appendOpenAICompatClaudeCodeTodoGuardToRequestBody(nil))
	require.False(t, isOpenAICompatMessagesBridgeBody([]byte(`{"input":[{"role":"user","content":"ordinary text"}]}`)))
	require.False(t, isOpenAICompatMessagesBridgeRequestBody(map[string]any{"input": []any{map[string]any{"content": "ordinary text"}}}))
	require.True(t, isOpenAICompatMessagesBridgeBody([]byte(`{"prompt_cache_key":"anthropic-cache-existing"}`)))
	body := map[string]any{"input": []any{map[string]any{"type": "message", "role": "user", "content": "hello"}}}
	require.True(t, appendOpenAICompatClaudeCodeTodoGuardToRequestBody(body))
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	require.Contains(t, gjson.GetBytes(encoded, "input.0.content.0.text").String(), "<todo-guard>")
	require.False(t, appendOpenAICompatClaudeCodeTodoGuardToRequestBody(body))
}

package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
)

type openAISemanticDriftKind string

const (
	openAISemanticDriftMissing openAISemanticDriftKind = "missing"
	openAISemanticDriftAdded   openAISemanticDriftKind = "added"
	openAISemanticDriftChanged openAISemanticDriftKind = "changed"
	openAISemanticDriftInvalid openAISemanticDriftKind = "invalid"
)

type OpenAISemanticDrift struct {
	Field string                  `json:"field"`
	Kind  openAISemanticDriftKind `json:"kind"`
	Hash  string                  `json:"hash,omitempty"`
}

type openAISemanticIntegrityOptions struct {
	AllowedFinalModel        string
	AllowedInstructionSuffix string
}

var (
	openAISemanticIntegrityObserved uint64
	openAISemanticIntegrityDrifted  uint64
	openAISemanticIntegrityErrors   uint64
)

var openAISemanticIntegrityFields = []string{
	"model",
	"input",
	"instructions",
	"reasoning",
	"tools",
	"functions",
	"tool_choice",
	"parallel_tool_calls",
	"text",
	"previous_response_id",
	"max_tokens",
	"max_output_tokens",
	"max_completion_tokens",
	"prompt_cache_key",
	"conversation_id",
	"session_id",
}

func compareOpenAISemanticIntegrity(originalBody, finalBody []byte, opts openAISemanticIntegrityOptions) ([]OpenAISemanticDrift, error) {
	original, err := decodeOpenAISemanticBody(originalBody)
	if err != nil {
		return []OpenAISemanticDrift{{Field: "body", Kind: openAISemanticDriftInvalid}}, err
	}
	final, err := decodeOpenAISemanticBody(finalBody)
	if err != nil {
		return []OpenAISemanticDrift{{Field: "body", Kind: openAISemanticDriftInvalid}}, err
	}
	canonicalizeOpenAISemanticBody(original)
	canonicalizeOpenAISemanticBody(final)
	canonicalizeAllowedInstructionSuffix(original, final, opts.AllowedInstructionSuffix)

	drifts := make([]OpenAISemanticDrift, 0)
	for _, field := range openAISemanticIntegrityFields {
		originalValue, originalExists := original[field]
		finalValue, finalExists := final[field]
		if field == "model" && originalExists && finalExists && strings.TrimSpace(opts.AllowedFinalModel) != "" {
			if finalModel, ok := finalValue.(string); ok && strings.TrimSpace(finalModel) == strings.TrimSpace(opts.AllowedFinalModel) {
				continue
			}
		}
		switch {
		case originalExists && !finalExists:
			drifts = append(drifts, OpenAISemanticDrift{Field: field, Kind: openAISemanticDriftMissing, Hash: semanticValueHash(originalValue)})
		case !originalExists && finalExists:
			drifts = append(drifts, OpenAISemanticDrift{Field: field, Kind: openAISemanticDriftAdded})
		case originalExists && finalExists && !semanticValuesEqual(originalValue, finalValue):
			drifts = append(drifts, OpenAISemanticDrift{Field: field, Kind: openAISemanticDriftChanged, Hash: semanticValueHash(finalValue)})
		}
	}
	return drifts, nil
}

func canonicalizeAllowedInstructionSuffix(original, final map[string]any, suffix string) {
	suffix = strings.TrimSpace(suffix)
	if suffix == "" || original == nil || final == nil {
		return
	}
	originalInstructions, originalExists := original["instructions"].(string)
	finalInstructions, finalExists := final["instructions"].(string)
	if !finalExists {
		return
	}
	if !originalExists && finalInstructions == suffix {
		delete(final, "instructions")
		return
	}
	if originalExists && finalInstructions == originalInstructions+"\n\n"+suffix {
		final["instructions"] = originalInstructions
	}
}

func observeOpenAISemanticIntegrity(c *gin.Context, account *Account, transport string, originalBody, finalBody []byte, opts openAISemanticIntegrityOptions) {
	if account == nil || !account.UsesOpenAICodexProtocol() || len(originalBody) == 0 || len(finalBody) == 0 {
		return
	}
	atomic.AddUint64(&openAISemanticIntegrityObserved, 1)
	drifts, err := compareOpenAISemanticIntegrity(originalBody, finalBody, opts)
	if err != nil {
		atomic.AddUint64(&openAISemanticIntegrityErrors, 1)
		logger.LegacyPrintf("service.openai_semantic_integrity", "[OpenAI semantic integrity] compare_error account_id=%d transport=%s fields=%s", account.ID, normalizeOpenAISemanticLogToken(transport), openAISemanticDriftFields(drifts))
		return
	}
	if len(drifts) == 0 {
		return
	}
	atomic.AddUint64(&openAISemanticIntegrityDrifted, 1)
	logger.LegacyPrintf("service.openai_semantic_integrity", "[OpenAI semantic integrity] drift account_id=%d transport=%s fields=%s", account.ID, normalizeOpenAISemanticLogToken(transport), openAISemanticDriftFields(drifts))
}

func observeOpenAISemanticIntegrityMap(c *gin.Context, account *Account, transport string, original, final map[string]any, opts openAISemanticIntegrityOptions) {
	if account == nil || !account.UsesOpenAICodexProtocol() || original == nil || final == nil {
		return
	}
	originalBody, err := json.Marshal(original)
	if err != nil {
		atomic.AddUint64(&openAISemanticIntegrityErrors, 1)
		return
	}
	finalBody, err := json.Marshal(final)
	if err != nil {
		atomic.AddUint64(&openAISemanticIntegrityErrors, 1)
		return
	}
	observeOpenAISemanticIntegrity(c, account, transport, originalBody, finalBody, opts)
}

func decodeOpenAISemanticBody(body []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var decoded map[string]any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("semantic body contains trailing JSON token")
		}
		return nil, err
	}
	return decoded, nil
}

func canonicalizeOpenAISemanticBody(body map[string]any) {
	if body == nil {
		return
	}
	if model, ok := body["model"].(string); ok {
		body["model"] = strings.TrimSpace(model)
	}
	if reasoning, ok := body["reasoning"].(map[string]any); ok {
		if reasoning["effort"] == "minimal" {
			reasoning["effort"] = "none"
		}
		model, _ := body["model"].(string)
		mode, _ := reasoning["mode"].(string)
		effort, _ := reasoning["effort"].(string)
		if !isOpenAIGPT6AstraModel(model) && strings.EqualFold(strings.TrimSpace(mode), "pro") && strings.TrimSpace(effort) == "" {
			reasoning["effort"] = "max"
			delete(reasoning, "mode")
		}
	}
	if text, ok := body["input"].(string); ok {
		body["input"] = []any{}
		if strings.TrimSpace(text) != "" {
			body["input"] = []any{map[string]any{"type": "message", "role": "user", "content": text}}
		}
	}
	omitSystem := true
	if text, ok := body["text"].(map[string]any); ok {
		if format, ok := text["format"].(map[string]any); ok && format["type"] == "json_object" {
			omitSystem = false
		}
	}
	extractSystemMessagesFromInput(body, omitSystem)
	if instructions, ok := body["instructions"].(string); body["instructions"] == nil || (ok && strings.TrimSpace(instructions) == "") {
		delete(body, "instructions")
	}
	_, hasTools := body["tools"]
	if functions, ok := body["functions"].([]any); ok && !hasTools {
		tools := make([]any, 0, len(functions))
		for _, function := range functions {
			tools = append(tools, map[string]any{"type": "function", "function": function})
		}
		body["tools"] = tools
		delete(body, "functions")
	}
	_, hasChoice := body["tool_choice"]
	if choice, exists := body["function_call"]; exists && !hasChoice {
		switch c := choice.(type) {
		case string:
			body["tool_choice"] = c
			delete(body, "function_call")
		case map[string]any:
			if name, ok := c["name"].(string); ok && name != "" {
				body["tool_choice"] = map[string]any{"type": "function", "name": name}
				delete(body, "function_call")
			}
		}
	}
	if tools, ok := body["tools"].([]any); ok {
		for _, raw := range tools {
			tool, ok := raw.(map[string]any)
			if !ok || tool["type"] != "function" {
				continue
			}
			if function, ok := tool["function"].(map[string]any); ok {
				for key, value := range function {
					if _, exists := tool[key]; !exists {
						tool[key] = value
					}
				}
				delete(tool, "function")
			}
		}
	}
	if choice, ok := body["tool_choice"].(map[string]any); ok && choice["type"] == "function" {
		if function, ok := choice["function"].(map[string]any); ok {
			if _, exists := choice["name"]; !exists {
				choice["name"] = function["name"]
			}
			delete(choice, "function")
		}
	}
	_, _, _ = aliasOpenAIOAuthReservedToolNames(body)
	input, ok := body["input"].([]any)
	if !ok {
		return
	}
	referenceIDs := codexItemReferenceIDMappings(input, false)
	itemIDs := codexInputItemIDs(input)
	callIDs := codexInputCallIDs(input)
	for i, raw := range input {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if item["role"] == "tool" && strings.TrimSpace(firstNonEmptyString(item["call_id"], item["tool_call_id"], item["id"])) != "" {
			if _, lossless := extractLosslessTextFromContent(item["content"]); lossless {
				if result, changed := normalizeCodexToolRoleMessages([]any{item}); changed {
					item = result[0].(map[string]any)
					input[i] = item
				}
			}
		}
		typ, _ := item["type"].(string)
		if typ == "" && (item["role"] == "user" || item["role"] == "assistant" || item["role"] == "developer") {
			typ = "message"
			item["type"] = typ
		}
		switch typ {
		case "message":
			delete(item, "id")
			if text, ok := item["content"].(string); ok {
				item["content"] = []any{map[string]any{"type": "input_text", "text": text}}
			}
			if parts, ok := item["content"].([]any); ok {
				for _, rawPart := range parts {
					if part, ok := rawPart.(map[string]any); ok && (part["type"] == "text" || part["type"] == "output_text") {
						part["type"] = "input_text"
					}
				}
			}
		case "reasoning":
			delete(item, "id")
			delete(item, "call_id")
			if item["summary"] == nil {
				item["summary"] = []any{}
			}
		case "compaction_summary":
			delete(item, "id")
		case "item_reference":
			id, _ := item["id"].(string)
			id = strings.TrimSpace(id)
			if _, existing := itemIDs[id]; !existing && strings.HasPrefix(id, "call_") {
				if mapped, exists := referenceIDs[id]; exists {
					item["id"] = mapped
				} else if _, sameTurnCall := callIDs[id]; !sameTurnCall {
					item["id"] = normalizeCodexCallID(id)
				}
			}
		}
		if isCodexToolCallItemType(typ) {
			id := firstNonEmptyString(item["call_id"], item["id"])
			if id != "" {
				item["call_id"] = normalizeCodexCallIDForItemType(typ, id)
			}
			delete(item, "id")
		}
	}
}

func semanticValuesEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func semanticValueHash(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:8])
}

func openAISemanticDriftFields(drifts []OpenAISemanticDrift) string {
	if len(drifts) == 0 {
		return "-"
	}
	fields := make([]string, 0, len(drifts))
	for _, drift := range drifts {
		fields = append(fields, fmt.Sprintf("%s:%s", normalizeOpenAISemanticLogToken(drift.Field), normalizeOpenAISemanticLogToken(string(drift.Kind))))
	}
	sort.Strings(fields)
	return strings.Join(fields, ",")
}

func normalizeOpenAISemanticLogToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == ':' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

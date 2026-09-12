package service

import (
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

var codexResponsesFieldOrder = []string{
	"model", "instructions", "input", "tools", "tool_choice", "parallel_tool_calls",
	"reasoning", "store", "stream", "stream_options", "include", "service_tier",
	"prompt_cache_key", "text", "client_metadata", "access_programs",
}

var codexCompactFieldOrder = []string{
	"model", "input", "instructions", "tools", "parallel_tool_calls", "reasoning",
	"service_tier", "prompt_cache_key", "text", "access_programs",
}

var codexWSCreateFieldOrder = []string{
	"type", "model", "instructions", "previous_response_id", "input", "tools",
	"tool_choice", "parallel_tool_calls", "reasoning", "store", "stream",
	"stream_options", "include", "service_tier", "prompt_cache_key", "text",
	"generate", "client_metadata", "access_programs",
}

func applyCodexBodyFieldOrder(c *gin.Context, account *Account, targetURL string, body []byte) []byte {
	if !codexDeviceWireProfileEnabled(c, account) {
		return body
	}
	parsed, err := url.Parse(targetURL)
	if err != nil {
		return body
	}
	path := strings.TrimRight(parsed.Path, "/")
	switch {
	case strings.HasSuffix(path, "/responses/compact"):
		return reorderCodexTopLevelFields(body, codexCompactFieldOrder)
	case strings.HasSuffix(path, "/responses"):
		return reorderCodexTopLevelFields(body, codexResponsesFieldOrder)
	default:
		return body
	}
}

func reorderCodexTopLevelFields(body []byte, order []string) []byte {
	if !gjson.ValidBytes(body) {
		return body
	}
	parsed := gjson.ParseBytes(body)
	if !parsed.IsObject() {
		return body
	}
	type field struct {
		name string
		key  string
		raw  string
	}
	fields := make([]field, 0, len(order))
	seen := make(map[string]bool)
	duplicate := false
	parsed.ForEach(func(key, value gjson.Result) bool {
		name := key.String()
		if seen[name] {
			duplicate = true
			return false
		}
		seen[name] = true
		fields = append(fields, field{name: name, key: key.Raw, raw: value.Raw})
		return true
	})
	if duplicate || len(fields) == 0 {
		return body
	}
	index := make(map[string]int, len(fields))
	for i, field := range fields {
		index[field.name] = i
	}
	emitted := make([]bool, len(fields))
	out := []byte{'{'}
	emit := func(i int) {
		if len(out) > 1 {
			out = append(out, ',')
		}
		out = append(out, fields[i].key...)
		out = append(out, ':')
		out = append(out, fields[i].raw...)
		emitted[i] = true
	}
	for _, name := range order {
		if i, ok := index[name]; ok {
			emit(i)
		}
	}
	for i := range fields {
		if !emitted[i] {
			emit(i)
		}
	}
	return append(out, '}')
}

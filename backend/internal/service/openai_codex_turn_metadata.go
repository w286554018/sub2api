package service

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf16"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// rewriteCodexTurnMetadataJSON changes selected top-level values without
// re-marshalling the whole object. This preserves key order, whitespace,
// escape spelling, duplicate keys, and unknown metadata fields.
func rewriteCodexTurnMetadataJSON(raw string, rebuildInvalid bool, updates func(map[string]any) map[string]any) string {
	original := raw
	var metadata map[string]any
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if !json.Valid([]byte(raw)) || decoder.Decode(&metadata) != nil || metadata == nil {
		if !rebuildInvalid {
			return original
		}
		raw, metadata = "{}", map[string]any{}
	}
	fields := updates(metadata)
	encoded := make(map[string]string, len(fields))
	for name, value := range fields {
		next, err := marshalCodexTurnMetadataValue(value)
		if err != nil {
			return original
		}
		encoded[name] = next
	}
	seen := make(map[string]bool, len(fields))
	next := rewriteCodexJSONMembers(raw, func(name string, value gjson.Result) (string, bool) {
		replacement, ok := encoded[name]
		if !ok {
			return "", false
		}
		seen[name] = true
		if text, ok := fields[name].(string); ok && value.Type == gjson.String && value.Str == text {
			return "", false
		}
		return replacement, true
	})
	for _, name := range slices.Sorted(maps.Keys(encoded)) {
		if seen[name] {
			continue
		}
		var err error
		next, err = sjson.SetRaw(next, name, encoded[name])
		if err != nil {
			return original
		}
	}
	return next
}

// Visit every member, including duplicate keys, without decoding into a map.
func rewriteCodexJSONMembers(raw string, rewrite func(string, gjson.Result) (string, bool)) string {
	var out []byte
	offset := 0
	gjson.Parse(raw).ForEach(func(key, value gjson.Result) bool {
		next, changed := rewrite(key.String(), value)
		if !changed {
			return true
		}
		if out == nil {
			out = make([]byte, 0, len(raw))
		}
		out = append(out, raw[offset:value.Index]...)
		out = append(out, next...)
		offset = value.Index + len(value.Raw)
		return true
	})
	if out == nil {
		return raw
	}
	out = append(out, raw[offset:]...)
	return string(out)
}

func deleteCodexJSONMembers(raw string, remove func(string, gjson.Result) bool) string {
	parsed := gjson.Parse(raw)
	if !parsed.IsObject() || !gjson.Valid(raw) {
		return raw
	}
	start := strings.IndexByte(raw, '{') + 1
	previousEnd := start
	out := make([]byte, 0, len(raw))
	out = append(out, raw[:start]...)
	kept, removed := 0, false
	parsed.ForEach(func(key, value gjson.Result) bool {
		leading := raw[previousEnd:key.Index]
		previousEnd = value.Index + len(value.Raw)
		if remove(key.String(), value) {
			removed = true
			return true
		}
		if kept > 0 {
			out = append(out, ',')
		}
		if comma := strings.IndexByte(leading, ','); comma >= 0 {
			out = append(out, leading[:comma]...)
			out = append(out, leading[comma+1:]...)
		} else {
			out = append(out, leading...)
		}
		out = append(out, raw[key.Index:previousEnd]...)
		kept++
		return true
	})
	if !removed {
		return raw
	}
	out = append(out, raw[previousEnd:]...)
	return string(out)
}

func marshalCodexTurnMetadataValue(value any) (string, error) {
	raw, err := marshalOpenAIUpstreamJSON(value)
	if err != nil {
		return "", err
	}
	out := make([]byte, 0, len(raw))
	for _, r := range string(raw) {
		switch {
		case r < 0x80:
			out = append(out, byte(r))
		case r <= 0xffff:
			out = append(out, fmt.Sprintf(`\u%04x`, r)...)
		default:
			high, low := utf16.EncodeRune(r)
			out = append(out, fmt.Sprintf(`\u%04x\u%04x`, high, low)...)
		}
	}
	return string(out), nil
}

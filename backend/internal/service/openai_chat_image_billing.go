package service

import (
	"encoding/base64"
	"strings"

	"github.com/tidwall/gjson"
)

// openAIChatImageBillingContext carries the image-specific request metadata
// needed by the raw Chat Completions path.  Banana-style Gemini upstreams use
// this path and put the requested tier under extra_body.google.image_config;
// the normal OpenAI image endpoint parser never sees that body.
type openAIChatImageBillingContext struct {
	Enabled   bool
	InputSize string
}

// resolveOpenAIChatImageBillingContext identifies Chat Completions requests
// that may return generated images and extracts the Google-compatible image
// tier without treating the ignored top-level `size` field as authoritative.
func resolveOpenAIChatImageBillingContext(body []byte, models ...string) openAIChatImageBillingContext {
	inputSize := extractOpenAIChatImageInputSize(body)
	enabled := strings.TrimSpace(inputSize) != ""

	for _, model := range models {
		if isOpenAIImageBillingModelAlias(model) {
			enabled = true
			break
		}
	}

	if !enabled && len(body) > 0 && gjson.ValidBytes(body) {
		modalities := gjson.GetBytes(body, "modalities")
		if modalities.IsArray() {
			modalities.ForEach(func(_, value gjson.Result) bool {
				if strings.EqualFold(strings.TrimSpace(value.String()), "image") {
					enabled = true
				}
				return !enabled
			})
		}
		if !enabled {
			for _, path := range []string{
				"extra_body.google.image_config",
				"extra_body.google.imageConfig",
				"generationConfig.imageConfig",
				"generation_config.image_config",
			} {
				if value := gjson.GetBytes(body, path); value.Exists() && value.IsObject() {
					enabled = true
					break
				}
			}
		}
	}

	return openAIChatImageBillingContext{Enabled: enabled, InputSize: inputSize}
}

// extractOpenAIChatImageInputSize reads the field used by Gemini's
// OpenAI-compatible Chat Completions wrapper.  The top-level `size` parameter
// is intentionally excluded: Banana ignores it, so billing it would promise a
// tier that the upstream did not actually receive.
func extractOpenAIChatImageInputSize(body []byte) string {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return ""
	}
	for _, path := range []string{
		"extra_body.google.image_config.image_size",
		"extra_body.google.imageConfig.imageSize",
		"extra_body.google.image_config.size",
		"extra_body.google.imageConfig.size",
		"generationConfig.imageConfig.imageSize",
		"generation_config.image_config.image_size",
	} {
		value := gjson.GetBytes(body, path)
		if value.Type == gjson.String {
			if trimmed := strings.TrimSpace(value.String()); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

// openAIChatImageOutputTracker combines the existing OpenAI response counter
// with a small accumulator for Chat Completions message/delta content.  Some
// Gemini-compatible upstreams render generated images as Markdown data URLs,
// and streaming responses may split that URL across multiple SSE chunks.
type openAIChatImageOutputTracker struct {
	counter   *openAIImageOutputCounter
	fragments map[string]string
}

const (
	// A 4K JPEG data URL is well below this limit.  The cap prevents a text-only
	// response from retaining unbounded content while still allowing the image
	// marker and payload to arrive over many SSE chunks.
	maxOpenAIChatImageFragmentBytes = 8 << 20
	openAIChatImageMarkerTailBytes  = 96
)

func newOpenAIChatImageOutputTracker() *openAIChatImageOutputTracker {
	return &openAIChatImageOutputTracker{
		counter:   newOpenAIImageOutputCounter(),
		fragments: make(map[string]string),
	}
}

func (t *openAIChatImageOutputTracker) ObserveJSON(body []byte) {
	if t == nil || len(body) == 0 || !gjson.ValidBytes(body) {
		return
	}
	t.counter.AddJSONResponse(body)
	t.observeChoices(gjson.GetBytes(body, "choices"))
}

func (t *openAIChatImageOutputTracker) ObserveSSEData(data []byte) {
	if t == nil || len(data) == 0 || strings.TrimSpace(string(data)) == "[DONE]" || !gjson.ValidBytes(data) {
		return
	}
	t.counter.AddSSEData(data)
	t.observeChoices(gjson.GetBytes(data, "choices"))
}

func (t *openAIChatImageOutputTracker) observeChoices(choices gjson.Result) {
	if t == nil || !choices.IsArray() {
		return
	}
	choices.ForEach(func(index, choice gjson.Result) bool {
		choiceKey := strings.TrimSpace(choice.Get("index").String())
		if choiceKey == "" {
			choiceKey = index.String()
		}
		for _, path := range []string{"message.content", "delta.content"} {
			t.appendContent(choiceKey, choice.Get(path))
		}
		return true
	})
}

func (t *openAIChatImageOutputTracker) appendContent(key string, content gjson.Result) {
	if t == nil || !content.Exists() {
		return
	}
	if content.Type == gjson.String {
		fragment := content.String()
		if fragment == "" {
			return
		}
		combined := t.fragments[key] + fragment
		lower := strings.ToLower(combined)
		switch {
		case strings.Contains(lower, "data:image/") && len(combined) > maxOpenAIChatImageFragmentBytes:
			combined = combined[len(combined)-maxOpenAIChatImageFragmentBytes:]
		case !strings.Contains(lower, "data:image/") && isLikelyOpenAIChatImageBase64(combined) && len(combined) > maxOpenAIChatImageFragmentBytes:
			combined = combined[len(combined)-maxOpenAIChatImageFragmentBytes:]
		case !strings.Contains(lower, "data:image/") && !isLikelyOpenAIChatImageBase64(combined) && len(combined) > openAIChatImageMarkerTailBytes:
			combined = combined[len(combined)-openAIChatImageMarkerTailBytes:]
		}
		t.fragments[key] = combined
		return
	}
	if content.IsArray() || content.IsObject() {
		content.ForEach(func(_, child gjson.Result) bool {
			t.appendContent(key, child)
			return true
		})
	}
}

// Result flushes accumulated Chat Completions content and returns the same
// count/size representation used by the existing OpenAI image billing path.
func (t *openAIChatImageOutputTracker) Result() (int, []string) {
	if t == nil || t.counter == nil {
		return 0, nil
	}
	for _, fragment := range t.fragments {
		dataURIs := extractOpenAIChatImageDataURIs(fragment)
		for _, dataURI := range dataURIs {
			t.counter.addInlineImageDataURI(dataURI)
		}
		if len(dataURIs) == 0 {
			t.counter.addInlineImageBase64(fragment)
		}
	}
	return t.counter.Count(), t.counter.Sizes()
}

func (c *openAIImageOutputCounter) addInlineImageDataURI(dataURI string) {
	if c == nil {
		return
	}
	dataURI = strings.TrimSpace(dataURI)
	comma := strings.IndexByte(dataURI, ',')
	if comma <= 0 || comma+1 >= len(dataURI) {
		return
	}
	mimeAndEncoding := strings.TrimSpace(dataURI[:comma])
	normalized := normalizeOpenAIChatImageBase64(dataURI[comma+1:])
	// A generated image data URL is substantially larger than a short piece of
	// prose that happens to follow `data:image/...;base64,`.  Keep the existing
	// dimension probe as the preferred validation and reject tiny payloads that
	// cannot represent a normal PNG/JPEG/WebP image.
	if normalized == "" || len(normalized) < 32 {
		return
	}
	key := "chat-inline:" + hashOpenAIImageOutputResult(strings.ToLower(mimeAndEncoding)+","+normalized)
	if key == "" {
		return
	}
	if _, exists := c.seen[key]; exists {
		return
	}
	c.seen[key] = struct{}{}
	c.seenOrder = append(c.seenOrder, key)
	c.count++
	if size := detectOpenAIImageResultSize(mimeAndEncoding + "," + normalized); size != "" {
		c.seenSizes[key] = size
	}
}

// addInlineImageBase64 accounts for providers that put the raw image payload
// directly in message.content instead of wrapping it in a data URL.  A
// dimension probe is required before counting so ordinary assistant prose (or
// an arbitrary long base64-looking string) cannot create a billable image.
func (c *openAIImageOutputCounter) addInlineImageBase64(content string) {
	if c == nil {
		return
	}
	normalized := normalizeOpenAIChatImageBase64(content)
	if normalized == "" || len(normalized) < 32 {
		return
	}
	size := detectOpenAIImageResultSize(normalized)
	if size == "" {
		return
	}
	key := "chat-inline-base64:" + hashOpenAIImageOutputResult(normalized)
	if key == "" {
		return
	}
	if _, exists := c.seen[key]; exists {
		return
	}
	c.seen[key] = struct{}{}
	c.seenOrder = append(c.seenOrder, key)
	c.seenSizes[key] = size
	c.count++
}

func extractOpenAIChatImageDataURIs(content string) []string {
	if content == "" {
		return nil
	}
	lower := strings.ToLower(content)
	const marker = "data:image/"
	const base64Marker = ";base64,"
	var out []string
	searchFrom := 0
	for searchFrom < len(content) {
		relative := strings.Index(lower[searchFrom:], marker)
		if relative < 0 {
			break
		}
		start := searchFrom + relative
		encodingRelative := strings.Index(lower[start:], base64Marker)
		if encodingRelative < 0 {
			break
		}
		payloadStart := start + encodingRelative + len(base64Marker)
		end := payloadStart
		for end < len(content) && isOpenAIBase64Byte(content[end]) {
			end++
		}
		if end > payloadStart {
			candidate := content[start:end]
			if comma := strings.IndexByte(candidate, ','); comma >= 0 && normalizeOpenAIChatImageBase64(candidate[comma+1:]) != "" {
				out = append(out, candidate)
			}
		}
		if end <= start {
			searchFrom = start + len(marker)
		} else {
			searchFrom = end
		}
	}
	return out
}

// normalizeOpenAIChatImageBase64 validates a Chat Completions inline payload
// while preserving a correctly padded representation for hashing and the
// dimension probe.  The older generic helper intentionally accepts several
// pointer formats but removes padding before calculating the replacement
// length; this local variant keeps padded Gemini data URLs decodable.
func normalizeOpenAIChatImageBase64(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !isLikelyOpenAIChatImageBase64(raw) {
		return ""
	}
	raw = strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\r', '\n':
			return -1
		default:
			return r
		}
	}, raw)
	raw = strings.TrimRight(raw, "=")
	raw += strings.Repeat("=", (4-len(raw)%4)%4)
	if _, err := base64.StdEncoding.DecodeString(raw); err != nil {
		return ""
	}
	return raw
}

func isLikelyOpenAIChatImageBase64(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		if isOpenAIBase64Byte(value[i]) || value[i] == ' ' || value[i] == '\t' || value[i] == '\r' || value[i] == '\n' {
			continue
		}
		return false
	}
	return true
}

func isOpenAIBase64Byte(value byte) bool {
	return (value >= 'A' && value <= 'Z') ||
		(value >= 'a' && value <= 'z') ||
		(value >= '0' && value <= '9') ||
		value == '+' || value == '/' || value == '='
}

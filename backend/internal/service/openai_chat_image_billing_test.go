package service

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractOpenAIChatImageInputSize(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		models     []string
		wantEnable bool
		wantSize   string
	}{
		{
			name:       "google snake case wins over ignored top level size",
			body:       `{"model":"gemini-3.1-flash-image","size":"1024x1024","extra_body":{"google":{"image_config":{"image_size":"4K"}}}}`,
			models:     []string{"gemini-3.1-flash-image"},
			wantEnable: true,
			wantSize:   "4K",
		},
		{
			name:       "google camel case compatibility",
			body:       `{"model":"gemini-3-pro-image-preview","extra_body":{"google":{"imageConfig":{"imageSize":"1K"}}}}`,
			models:     []string{"gemini-3-pro-image-preview"},
			wantEnable: true,
			wantSize:   "1K",
		},
		{
			name:       "image modality enables generic model",
			body:       `{"model":"custom-model","modalities":["text","image"]}`,
			wantEnable: true,
		},
		{
			name:       "image model enables without explicit tier",
			body:       `{"model":"gemini-3-pro-image"}`,
			models:     []string{"gemini-3-pro-image"},
			wantEnable: true,
		},
		{
			name:       "top level size alone is not trusted",
			body:       `{"model":"custom-model","size":"4096x4096"}`,
			wantEnable: false,
		},
		{
			name:       "invalid json",
			body:       `{not-json`,
			wantEnable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveOpenAIChatImageBillingContext([]byte(tt.body), tt.models...)
			require.Equal(t, tt.wantEnable, got.Enabled)
			require.Equal(t, tt.wantSize, got.InputSize)
		})
	}
}

func TestOpenAIChatImageOutputTrackerDetectsMarkdownDataURL(t *testing.T) {
	encoded := encodeOpenAIImageTestJPEG(t, 4, 3)
	content := "生成完成：\n![image](data:image/jpeg;base64," + encoded + ")"
	body, err := json.Marshal(map[string]any{
		"choices": []any{map[string]any{
			"index":   0,
			"message": map[string]any{"role": "assistant", "content": content},
		}},
	})
	require.NoError(t, err)

	tracker := newOpenAIChatImageOutputTracker()
	tracker.ObserveJSON(body)
	t.Logf("body=%s fragments=%#v uris=%#v", body, tracker.fragments, extractOpenAIChatImageDataURIs(tracker.fragments["0"]))
	count, sizes := tracker.Result()
	require.Equal(t, 1, count)
	require.Equal(t, []string{"4x3"}, sizes)
}

func TestOpenAIChatImageOutputTrackerJoinsSplitStreamingDataURL(t *testing.T) {
	encoded := encodeOpenAIImageTestJPEG(t, 5, 2)
	content := "![image](data:image/jpeg;base64," + encoded + ")"
	cut := len(content) / 2

	makeEvent := func(fragment string) []byte {
		return []byte(fmt.Sprintf(`{"choices":[{"index":0,"delta":{"content":%q}}]}`, fragment))
	}
	tracker := newOpenAIChatImageOutputTracker()
	tracker.ObserveSSEData(makeEvent(content[:cut]))
	tracker.ObserveSSEData(makeEvent(content[cut:]))

	count, sizes := tracker.Result()
	require.Equal(t, 1, count)
	require.Equal(t, []string{"5x2"}, sizes)
}

func TestOpenAIChatImageOutputTrackerIgnoresTinyProseDataURL(t *testing.T) {
	body := []byte(`{"choices":[{"index":0,"message":{"content":"not an image: data:image/jpeg;base64,not-valid!!!"}}]}`)
	tracker := newOpenAIChatImageOutputTracker()
	tracker.ObserveJSON(body)
	count, sizes := tracker.Result()
	require.Zero(t, count)
	require.Empty(t, sizes)
}

func TestOpenAIChatImageOutputTrackerDetectsPlainBase64(t *testing.T) {
	encoded := encodeOpenAIImageTestJPEG(t, 7, 6)
	body, err := json.Marshal(map[string]any{
		"choices": []any{map[string]any{
			"index":   0,
			"message": map[string]any{"role": "assistant", "content": encoded},
		}},
	})
	require.NoError(t, err)

	tracker := newOpenAIChatImageOutputTracker()
	tracker.ObserveJSON(body)
	count, sizes := tracker.Result()
	require.Equal(t, 1, count)
	require.Equal(t, []string{"7x6"}, sizes)
}

func TestOpenAIChatImageOutputTrackerJoinsSplitPlainBase64(t *testing.T) {
	encoded := encodeOpenAIImageTestJPEG(t, 9, 4)
	cut := len(encoded) / 2

	makeEvent := func(fragment string) []byte {
		return []byte(fmt.Sprintf(`{"choices":[{"index":0,"delta":{"content":%q}}]}`, fragment))
	}
	tracker := newOpenAIChatImageOutputTracker()
	tracker.ObserveSSEData(makeEvent(encoded[:cut]))
	tracker.ObserveSSEData(makeEvent(encoded[cut:]))

	count, sizes := tracker.Result()
	require.Equal(t, 1, count)
	require.Equal(t, []string{"9x4"}, sizes)
}

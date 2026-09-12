package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Codex manifest 标准化后只保留 slug，测试弹窗用 display_name 当选项标签，
// 留空会让模型选择器渲染成一排空白项。
func TestFetchOpenAIAccountModelsFillsPickerLabels(t *testing.T) {
	newCodexModelsOAuthCacheServer(t, `{"models":[{"slug":"gpt-5.6-terra"},{"slug":"codex-auto-review"}]}`)
	svc := &AccountTestService{}
	svc.SetOpenAIGatewayService(&OpenAIGatewayService{})

	models, err := svc.FetchOpenAIAccountModels(context.Background(), newCodexModelsTestAccount())
	require.NoError(t, err)
	wantLabels := map[string]string{
		"gpt-5.6-terra":          "gpt-5.6-terra",
		"codex-auto-review":      "codex-auto-review",
		"gpt-image-1":            "GPT Image 1",
		"gpt-image-1.5":          "GPT Image 1.5",
		"gpt-image-2":            "GPT Image 2",
		"gpt-image-2.5-flare":    "GPT Image 2.5 Flare",
		"gpt-image-2.5-sunburst": "GPT Image 2.5 Sunburst",
	}
	require.Len(t, models, len(wantLabels))
	for _, model := range models {
		require.NotEmpty(t, model.DisplayName, "picker label must not be empty for %q", model.ID)
		require.Equal(t, wantLabels[model.ID], model.DisplayName)
		require.Equal(t, "model", model.Type)
	}
	require.Equal(t, "gpt-5.6-terra", models[0].ID)
	require.Equal(t, "codex-auto-review", models[1].ID)
	// Image choices belong to the test picker, not the shared discovery cache.
	catalog, err := svc.openaiGatewayService.FetchOpenAIModelsList(context.Background(), newCodexModelsTestAccount())
	require.NoError(t, err)
	require.Equal(t, int64(2), gjson.GetBytes(catalog.Body, "data.#").Int())
}

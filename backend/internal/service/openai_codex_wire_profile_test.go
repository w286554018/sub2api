package service

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestReorderCodexTopLevelFieldsPreservesValues(t *testing.T) {
	body := []byte(`{"client_metadata":{"x":"y"},"input":[{"type":"message"}],"model":"gpt-5","unknown":7}`)
	got := reorderCodexTopLevelFields(body, []string{"model", "input", "client_metadata"})
	require.Equal(t, `{"model":"gpt-5","input":[{"type":"message"}],"client_metadata":{"x":"y"},"unknown":7}`, string(got))
	require.Equal(t, gjson.GetBytes(body, "input").Raw, gjson.GetBytes(got, "input").Raw)
}

func TestRewriteCodexTurnMetadataPreservesFormattingAndUnknownFields(t *testing.T) {
	raw := `{ "session_id" : "old", "unknown":"\u8bbe\u5907", "session_id":"old-duplicate" }`
	got := rewriteCodexTurnMetadataJSON(raw, false, func(metadata map[string]any) map[string]any {
		return map[string]any{"session_id": "new"}
	})
	require.Contains(t, got, `"unknown":"\u8bbe\u5907"`)
	require.Equal(t, 2, strings.Count(got, "session_id"))
	require.Contains(t, got, `"session_id":"new"`)
}

func TestCompressCodexRequestBodyUsesZstdOnlyForResponses(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	account := newTestOAuthAccount(9913, map[string]any{
		codexFingerprintModeExtraKey:        "device",
		codexFingerprintConvergenceExtraKey: true,
		codexFingerprintSeedExtraKey:        "11111111-1111-4111-8111-111111111111",
	})
	body := []byte(`{"model":"gpt-5","input":[]}`)
	wire, encoding, err := compressCodexRequestBody(c, account, "https://api.openai.com/v1/responses", body)
	require.NoError(t, err)
	require.Equal(t, "zstd", encoding)
	require.NotEqual(t, body, wire)
	require.True(t, bytes.HasPrefix(wire, []byte{0x28, 0xb5, 0x2f, 0xfd}))
	require.Equal(t, []byte{0x28, 0xb5, 0x2f, 0xfd, 0x00, 0x58}, wire[:6])

	plain, encoding, err := compressCodexRequestBody(c, account, "https://api.openai.com/v1/responses/compact", body)
	require.NoError(t, err)
	require.Empty(t, encoding)
	require.Equal(t, body, plain)
}

func TestOpenAICompatBridgeIdentityIsStableAndSelfConsistent(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	account := newTestOAuthAccount(9914, map[string]any{
		codexFingerprintModeExtraKey:        "device",
		codexFingerprintConvergenceExtraKey: true,
		codexFingerprintSeedExtraKey:        testCodexFingerprintSeed,
	})
	svc := &OpenAIGatewayService{}
	first := map[string]any{"model": "gpt-5", "input": "one"}
	restore, injected := svc.injectOpenAICompatBridgeIdentity(c, account, first, "cache-a")
	defer restore()
	require.True(t, injected)
	second := map[string]any{"model": "gpt-5", "input": "two"}
	secondRestore, secondInjected := svc.injectOpenAICompatBridgeIdentity(c, account, second, "cache-a")
	defer secondRestore()
	require.True(t, secondInjected)
	require.Equal(t, first["prompt_cache_key"], second["prompt_cache_key"])
	require.Equal(t, "auto", first["tool_choice"])
	meta := first["client_metadata"].(map[string]any)
	turnMeta := gjson.Parse(meta[openAIWSTurnMetadataHeader].(string))
	require.Equal(t, meta["session_id"], first["prompt_cache_key"])
	require.Equal(t, meta["session_id"], meta["thread_id"])
	require.Equal(t, meta["x-codex-window-id"], turnMeta.Get("window_id").String())
	require.Equal(t, meta["session_id"], turnMeta.Get("session_id").String())
	require.Equal(t, float64(0), turnMeta.Get("window_number").Float())
}

func TestOpenAICodexTurnStateWSEventOwnership(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	account := newTestOAuthAccount(9915, map[string]any{
		codexFingerprintModeExtraKey:        "device",
		codexFingerprintConvergenceExtraKey: true,
		codexFingerprintSeedExtraKey:        testCodexFingerprintSeed,
	})
	svc := &OpenAIGatewayService{}
	frame := []byte(`{"type":"response.metadata","headers":{"X-Codex-Turn-State":"blob-a"}}`)
	svc.noteOpenAICodexTurnStateFromWSEvent(c, account, frame)
	_, recorded := svc.openaiCodexTurnStateOrigins.Load(openAICodexTurnStateKey("blob-a"))
	require.True(t, recorded)
	require.False(t, svc.openAICodexTurnStateMintedByOther(c, account, "blob-a"))
	other := newTestOAuthAccount(9916, map[string]any{
		codexFingerprintModeExtraKey:        "device",
		codexFingerprintConvergenceExtraKey: true,
		codexFingerprintSeedExtraKey:        "22222222-2222-4222-8222-222222222222",
	})
	require.True(t, svc.openAICodexTurnStateMintedByOther(c, other, "blob-a"))
	payload := []byte(`{"type":"response.create","client_metadata":{"x-codex-turn-state":"blob-a","keep":"yes"}}`)
	clean := svc.guardOpenAICodexWSFrameTurnState(c, other, payload)
	require.False(t, gjson.GetBytes(clean, "client_metadata.x-codex-turn-state").Exists())
	require.Equal(t, "yes", gjson.GetBytes(clean, "client_metadata.keep").String())
}

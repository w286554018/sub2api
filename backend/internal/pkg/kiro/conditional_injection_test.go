package kiro

import (
	"strings"
	"testing"
)

func TestBuildConditionalSystemPrompt_PlainChat_NoInjection(t *testing.T) {
	// Plain chat: no thinking, no tools, no file tools
	result := buildConditionalSystemPrompt("You are a helpful assistant.", nil, "", false)

	// Should NOT contain identity prompt
	if strings.Contains(result, "CRITICAL_OVERRIDE") {
		t.Error("plain chat should not contain CRITICAL_OVERRIDE")
	}
	if strings.Contains(result, "You are Claude") {
		t.Error("plain chat should not contain identity injection")
	}

	// Should NOT contain chunked write policy
	if strings.Contains(result, systemChunkedWritePolicy) {
		t.Error("plain chat should not contain chunked write policy")
	}

	// Should contain the user's system prompt
	if !strings.Contains(result, "You are a helpful assistant.") {
		t.Error("should preserve user system prompt")
	}

	// Should NOT contain thinking prefix
	if strings.Contains(result, "<thinking_mode>") {
		t.Error("plain chat should not contain thinking prefix")
	}
}

func TestBuildConditionalSystemPrompt_WithThinking(t *testing.T) {
	thinking := &thinkingDirective{Mode: "adaptive", Effort: "high"}
	result := buildConditionalSystemPrompt("Solve this.", thinking, "", false)

	// Should contain thinking prefix
	if !strings.Contains(result, "<thinking_mode>adaptive</thinking_mode>") {
		t.Error("should contain thinking mode")
	}
	if !strings.Contains(result, "<thinking_effort>high</thinking_effort>") {
		t.Error("should contain thinking effort")
	}

	// Should NOT contain identity
	if strings.Contains(result, "CRITICAL_OVERRIDE") {
		t.Error("should not contain identity injection")
	}
}

func TestBuildConditionalSystemPrompt_WithToolChoiceHint(t *testing.T) {
	result := buildConditionalSystemPrompt("", nil, "[Use the search tool]", false)

	if !strings.Contains(result, "[Use the search tool]") {
		t.Error("should contain tool choice hint")
	}
}

func TestBuildConditionalSystemPrompt_WithFileTools(t *testing.T) {
	result := buildConditionalSystemPrompt("", nil, "", true)

	if !strings.Contains(result, systemChunkedWritePolicy) {
		t.Error("should contain chunked write policy when file tools present")
	}
}

func TestBuildConditionalSystemPrompt_WithoutFileTools(t *testing.T) {
	result := buildConditionalSystemPrompt("", nil, "", false)

	if strings.Contains(result, systemChunkedWritePolicy) {
		t.Error("should NOT contain chunked write policy without file tools")
	}
}

func TestBuildConditionalSystemPrompt_EmptySystem(t *testing.T) {
	result := buildConditionalSystemPrompt("", nil, "", false)

	// Should be minimal — no identity, no chunked write, no thinking
	if strings.Contains(result, "CRITICAL_OVERRIDE") {
		t.Error("should not have identity")
	}
	if strings.Contains(result, systemChunkedWritePolicy) {
		t.Error("should not have chunked write policy")
	}
}

func TestBuildInjectedSystemPrompt_Legacy_AlwaysInjects(t *testing.T) {
	// Legacy path should always inject identity and chunked write
	result := buildInjectedSystemPrompt("Hello", nil, "")

	if !strings.Contains(result, "CRITICAL_OVERRIDE") {
		t.Error("legacy should contain CRITICAL_OVERRIDE")
	}
	if !strings.Contains(result, systemChunkedWritePolicy) {
		t.Error("legacy should contain chunked write policy")
	}
	if !strings.Contains(result, "Hello") {
		t.Error("legacy should preserve user system prompt")
	}
}

func TestBuildConditionalSystemPrompt_TokenReduction(t *testing.T) {
	userPrompt := "You are a helpful coding assistant."

	legacy := buildInjectedSystemPrompt(userPrompt, nil, "")
	conditional := buildConditionalSystemPrompt(userPrompt, nil, "", false)

	// Conditional should be significantly shorter
	if len(conditional) >= len(legacy) {
		t.Errorf("conditional (%d chars) should be shorter than legacy (%d chars)",
			len(conditional), len(legacy))
	}

	reduction := float64(len(legacy)-len(conditional)) / float64(len(legacy)) * 100
	t.Logf("Token reduction: %d → %d chars (%.1f%% reduction)", len(legacy), len(conditional), reduction)
}

func TestHasFileWriteTools_WithWriteTool(t *testing.T) {
	body := []byte(`{"tools":[{"name":"write","input_schema":{}}]}`)
	if !hasFileWriteTools(body) {
		t.Error("should detect 'write' tool")
	}
}

func TestHasFileWriteTools_WithEditFile(t *testing.T) {
	body := []byte(`{"tools":[{"name":"edit_file","input_schema":{}}]}`)
	if !hasFileWriteTools(body) {
		t.Error("should detect 'edit_file' tool")
	}
}

func TestHasFileWriteTools_WithStrReplaceEditor(t *testing.T) {
	body := []byte(`{"tools":[{"name":"str_replace_editor","input_schema":{}}]}`)
	if !hasFileWriteTools(body) {
		t.Error("should detect 'str_replace_editor' tool")
	}
}

func TestHasFileWriteTools_NoFileTools(t *testing.T) {
	body := []byte(`{"tools":[{"name":"get_weather","input_schema":{}}]}`)
	if hasFileWriteTools(body) {
		t.Error("should not detect non-file tools")
	}
}

func TestHasFileWriteTools_NoTools(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"hello"}]}`)
	if hasFileWriteTools(body) {
		t.Error("should not detect tools when none present")
	}
}

func TestConditionalInjectionEnabled_Default(t *testing.T) {
	if ConditionalInjectionEnabled() {
		t.Fatal("should be off by default")
	}
}

package kiro

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateStructuredOutput_ValidJSON(t *testing.T) {
	schema := []byte(`{
		"type": "object",
		"properties": {
			"title": {"type": "string"},
			"summary": {"type": "string"},
			"tags": {"type": "array", "items": {"type": "string"}}
		},
		"required": ["title", "summary"],
		"additionalProperties": false
	}`)

	validJSON := `{"title":"AI Safety","summary":"A field focused on safety.","tags":["ai","safety"]}`
	if err := ValidateStructuredOutput(validJSON, schema); err != nil {
		t.Fatalf("expected valid, got error: %v", err)
	}
}

func TestValidateStructuredOutput_MissingRequired(t *testing.T) {
	schema := []byte(`{
		"type": "object",
		"properties": {
			"title": {"type": "string"},
			"summary": {"type": "string"}
		},
		"required": ["title", "summary"]
	}`)

	invalidJSON := `{"summary":"Missing title field"}`
	err := ValidateStructuredOutput(invalidJSON, schema)
	if err == nil {
		t.Fatal("expected validation error for missing required field")
	}
	if !strings.Contains(err.Error(), "title") {
		t.Errorf("error should mention missing field 'title', got: %v", err)
	}
}

func TestValidateStructuredOutput_WrongType(t *testing.T) {
	schema := []byte(`{
		"type": "object",
		"properties": {
			"count": {"type": "integer"}
		},
		"required": ["count"]
	}`)

	invalidJSON := `{"count":"not a number"}`
	err := ValidateStructuredOutput(invalidJSON, schema)
	if err == nil {
		t.Fatal("expected validation error for wrong type")
	}
}

func TestValidateStructuredOutput_InvalidJSON(t *testing.T) {
	schema := []byte(`{"type": "object"}`)
	err := ValidateStructuredOutput(`{broken json`, schema)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "invalid JSON") {
		t.Errorf("error should mention invalid JSON, got: %v", err)
	}
}

func TestValidateStructuredOutput_BadSchema_Degrades(t *testing.T) {
	badSchema := []byte(`{not valid json}`)
	err := ValidateStructuredOutput(`{"foo":"bar"}`, badSchema)
	if err != nil {
		t.Fatalf("bad schema should degrade gracefully, got: %v", err)
	}
}

func TestValidateStructuredOutput_EmptyInputs(t *testing.T) {
	if err := ValidateStructuredOutput("", []byte(`{"type":"object"}`)); err != nil {
		t.Fatalf("empty JSON should pass: %v", err)
	}
	if err := ValidateStructuredOutput(`{"a":1}`, nil); err != nil {
		t.Fatalf("empty schema should pass: %v", err)
	}
	if err := ValidateStructuredOutput("", nil); err != nil {
		t.Fatalf("both empty should pass: %v", err)
	}
}

func TestValidateStructuredOutput_AdditionalProperties(t *testing.T) {
	schema := []byte(`{
		"type": "object",
		"properties": {
			"name": {"type": "string"}
		},
		"additionalProperties": false
	}`)

	invalidJSON := `{"name":"test","extra":"field"}`
	err := ValidateStructuredOutput(invalidJSON, schema)
	if err == nil {
		t.Fatal("expected error for additional properties")
	}
}

func TestValidateStructuredOutput_NestedObject(t *testing.T) {
	schema := []byte(`{
		"type": "object",
		"properties": {
			"address": {
				"type": "object",
				"properties": {
					"city": {"type": "string"},
					"zip": {"type": "string"}
				},
				"required": ["city"]
			}
		},
		"required": ["address"]
	}`)

	validJSON := `{"address":{"city":"Paris","zip":"75001"}}`
	if err := ValidateStructuredOutput(validJSON, schema); err != nil {
		t.Fatalf("expected valid: %v", err)
	}

	invalidJSON := `{"address":{"zip":"75001"}}`
	if err := ValidateStructuredOutput(invalidJSON, schema); err == nil {
		t.Fatal("expected error for missing nested required field")
	}
}

func TestBuildStructuredOutputErrorResponse(t *testing.T) {
	err := &StructuredOutputValidationError{Message: "missing properties: 'title'"}
	resp := BuildStructuredOutputErrorResponse(err)

	var parsed map[string]any
	if e := json.Unmarshal(resp, &parsed); e != nil {
		t.Fatalf("failed to parse error response: %v", e)
	}
	if parsed["type"] != "error" {
		t.Errorf("type = %v, want error", parsed["type"])
	}
	errObj := parsed["error"].(map[string]any)
	if errObj["type"] != "invalid_request_error" {
		t.Errorf("error.type = %v", errObj["type"])
	}
	msg := errObj["message"].(string)
	if !strings.Contains(msg, "title") {
		t.Errorf("error.message should contain 'title': %v", msg)
	}
}

func TestStructuredOutputValidationEnabled_Default(t *testing.T) {
	if StructuredOutputValidationEnabled() {
		t.Fatal("should be off by default")
	}
}

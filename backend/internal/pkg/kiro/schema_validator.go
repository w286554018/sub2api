package kiro

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// StructuredOutputValidationEnabled returns true if structured output
// schema validation is enabled via environment variable.
func StructuredOutputValidationEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("SUB2API_KIRO_STRUCTURED_OUTPUT_VALIDATION")))
	return v == "true" || v == "1" || v == "yes"
}

// ValidateStructuredOutput validates a JSON string against a JSON Schema.
// Returns nil if validation passes, or a descriptive error if it fails.
//
// Schema compilation failures are treated as degradation: a warning is logged
// and nil is returned (the output passes through unvalidated).
func ValidateStructuredOutput(jsonText string, schemaBytes []byte) error {
	if len(schemaBytes) == 0 || len(jsonText) == 0 {
		return nil
	}

	// Parse the schema
	var schemaObj any
	if err := json.Unmarshal(schemaBytes, &schemaObj); err != nil {
		log.Printf("[kiro] structured output: schema parse error (degrading): %v", err)
		return nil // degrade: don't block on bad schema
	}

	// Compile the schema
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", schemaObj); err != nil {
		log.Printf("[kiro] structured output: schema compile error (degrading): %v", err)
		return nil
	}
	schema, err := compiler.Compile("schema.json")
	if err != nil {
		log.Printf("[kiro] structured output: schema compile error (degrading): %v", err)
		return nil
	}

	// Parse the output JSON
	var output any
	if err := json.Unmarshal([]byte(jsonText), &output); err != nil {
		return fmt.Errorf("invalid JSON output: %w", err)
	}

	// Validate
	if err := schema.Validate(output); err != nil {
		return fmt.Errorf("schema validation failed: %w", err)
	}

	return nil
}

// StructuredOutputValidationError represents a validation failure that
// should be returned to the client as an Anthropic-compatible error.
type StructuredOutputValidationError struct {
	Message string
}

func (e *StructuredOutputValidationError) Error() string {
	return e.Message
}

// BuildStructuredOutputErrorResponse builds an Anthropic-compatible error
// response body for structured output validation failure.
func BuildStructuredOutputErrorResponse(validationErr error) []byte {
	resp := map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    "invalid_request_error",
			"message": fmt.Sprintf("Structured output validation failed: %s", validationErr.Error()),
		},
	}
	b, _ := json.Marshal(resp)
	return b
}

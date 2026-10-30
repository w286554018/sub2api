package service

import (
	"encoding/json"
	"strings"

	"github.com/tidwall/gjson"
)

// excelBPSLineageSessionPrefix keeps BPS's invalid-ciphertext lineage apart
// from the native one under the same conversation key.
const excelBPSLineageSessionPrefix = "bps:"

// Only an explicit invalid-ciphertext rejection permits this recovery. Ordinary
// 400s, authentication, quota and transport errors must not replay a BPS request.
func isExcelBPSInvalidEncryptedContent(raw []byte) bool {
	if !gjson.ValidBytes(raw) {
		return false
	}
	if code := extractUpstreamErrorCode(raw); code != "" {
		return code == "invalid_encrypted_content"
	}
	// Accept the same diagnostic when a provider omits its error code. Do not classify
	// arbitrary mentions of encryption (or echoed request text) as this error.
	message := strings.ToLower(strings.TrimSpace(extractUpstreamErrorMessage(raw)))
	return strings.HasPrefix(message, "the encrypted content ") &&
		strings.Contains(message, "could not be verified") &&
		strings.Contains(message, "could not be decrypted or parsed")
}

// Prepare a single same-route retry from the already translated wire request.
// Like the native path (sanitizeEncryptedReasoningInputItem), drop opaque
// reasoning and whole compaction items carrying ciphertext BPS cannot read (for
// example produced by a native Codex account), preserving messages,
// tools/results, attachments and routing metadata without decoding numbers or
// string contents. The conversation continues without the compacted details.
// Encrypted message bodies and function arguments may be the only copy of user
// context and are never discarded to make a request pass.
func prepareExcelBPSInvalidEncryptedRetry(body, rejection []byte) ([]byte, bool) {
	if !isExcelBPSInvalidEncryptedContent(rejection) {
		return body, false
	}
	retry, removed := stripExcelBPSInvalidEncryptedContent(body, nil)
	return retry, len(removed) > 0
}

// A nil invalid set permits the initial, explicitly rejected BPS retry. Later
// turns remove only remembered ciphertexts under the same safe-item policy.
func stripExcelBPSInvalidEncryptedContent(body []byte, invalid map[string]struct{}) ([]byte, []string) {
	var request map[string]json.RawMessage
	if json.Unmarshal(body, &request) != nil {
		return body, nil
	}
	var input []json.RawMessage
	if json.Unmarshal(request["input"], &input) != nil {
		return body, nil
	}
	kept := make([]json.RawMessage, 0, len(input))
	var removed []string
	hasHistory := false
	for _, item := range input {
		typeName := gjson.GetBytes(item, "type").String()
		encrypted := gjson.GetBytes(item, "encrypted_content")
		if (typeName == "reasoning" && encrypted.Type == gjson.String && encrypted.String() != "") ||
			((typeName == "compaction" || typeName == "compaction_summary") && encrypted.Exists()) {
			digest := openAIEncryptedContentDigest(encrypted.String())
			_, remembered := invalid[digest]
			if invalid == nil || remembered {
				removed = append(removed, digest)
			} else {
				kept = append(kept, item)
			}
			continue
		}
		if encrypted.Exists() || len(gjson.GetBytes(item, "encrypted_function_args").Array()) > 0 {
			return body, nil
		}
		for _, field := range []string{"content", "output"} {
			for _, part := range gjson.GetBytes(item, field).Array() {
				if part.Get("type").String() == "encrypted_content" || part.Get("encrypted_content").Exists() {
					return body, nil
				}
			}
		}
		// Injected developer instructions and a compaction trigger alone are
		// not enough context to regenerate a user's task.
		role := gjson.GetBytes(item, "role").String()
		if role == "user" || role == "assistant" || typeName == "agent_message" ||
			typeName == "function_call" || typeName == "function_call_output" {
			hasHistory = true
		}
		kept = append(kept, item)
	}
	if len(removed) == 0 || !hasHistory {
		return body, nil
	}
	var err error
	request["input"], err = json.Marshal(kept)
	if err != nil {
		return body, nil
	}
	retry, err := json.Marshal(request)
	if err != nil {
		return body, nil
	}
	return retry, removed
}

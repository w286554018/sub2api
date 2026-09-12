package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const (
	codexMatrixSession      = "0198f0de-7b2e-7abc-8def-123456789abc"
	codexMatrixTurn         = "0198f0df-0123-7abc-8def-123456789abc"
	codexMatrixParent       = "0198f0dc-1234-7abc-8def-123456789abc"
	codexMatrixAncestorTurn = "0198f0dd-2345-7abc-8def-123456789abc"
	codexMatrixInstallation = "b3219132-7351-4dbd-92ee-f88b4c71c616"
	codexMatrixDevice       = "c23ce1aa-de02-456b-9189-c0ce42fe9bb9"
)

func codexModeMatrixAccount(mode string, convergence bool) *Account {
	account := newTestOAuthAccount(9401, map[string]any{
		codexFingerprintModeExtraKey:        mode,
		codexFingerprintConvergenceExtraKey: convergence,
		codexFingerprintSeedExtraKey:        testCodexFingerprintSeed,
		"openai_device_id":                  codexMatrixDevice,
	})
	account.Credentials = map[string]any{
		"access_token":       "offline-matrix-token",
		"chatgpt_account_id": "offline-matrix-account",
	}
	account.Concurrency = 1
	return account
}

func codexModeMatrixMetadata(window int, root bool) string {
	rootTurn := codexMatrixAncestorTurn
	if root {
		rootTurn = codexMatrixTurn
	}
	return fmt.Sprintf(`{ "session_id":"%s", "thread_id":"%s", "turn_id":"%s", "root_turn_id":"%s", "parent_turn_id":"%s", "parent_thread_id":"%s", "forked_from_thread_id":"%s", "context_window_id":"%s", "installation_id":"%s", "window_id":"%s:%d", "window_number":%d, "x-openai-subagent":"reviewer", "tool_namespaces_info":["offline-tool"], "unknown":"\u0061", "ratio":1e+06 }`,
		codexMatrixSession, codexMatrixSession, codexMatrixTurn, rootTurn,
		codexMatrixAncestorTurn, codexMatrixParent, codexMatrixParent,
		codexMatrixAncestorTurn, codexMatrixInstallation, codexMatrixSession, window, window)
}

func codexModeMatrixBody(t *testing.T, metadataKind, cacheKey string, window int, root bool) []byte {
	t.Helper()
	body := map[string]any{
		"model": "gpt-5.4", "instructions": "Offline matrix.", "input": "hello",
		"stream": true, "prompt_cache_key": cacheKey,
	}
	switch metadataKind {
	case "absent":
	case "empty":
		body["client_metadata"] = map[string]any{}
	case "installation":
		body["client_metadata"] = map[string]any{"x-codex-installation-id": codexMatrixInstallation}
	case "embedded":
		body["client_metadata"] = map[string]any{openAIWSTurnMetadataHeader: codexModeMatrixMetadata(window, root)}
	case "full":
		var cm map[string]any
		require.NoError(t, json.Unmarshal([]byte(codexModeMatrixMetadata(window, root)), &cm))
		delete(cm, "installation_id")
		delete(cm, "window_id")
		cm["x-codex-installation-id"] = codexMatrixInstallation
		cm["x-codex-window-id"] = fmt.Sprintf("%s:%d", codexMatrixSession, window)
		cm[openAIWSTurnMetadataHeader] = codexModeMatrixMetadata(window, root)
		body["client_metadata"] = cm
	default:
		t.Fatalf("unknown metadata kind %q", metadataKind)
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	return raw
}

func codexModeMatrixHeaders(direct bool, window int, root bool) http.Header {
	headers := make(http.Header)
	headers.Set(openAIWSTurnMetadataHeader, codexModeMatrixMetadata(window, root))
	if direct {
		headers.Set("session-id", codexMatrixSession)
		headers.Set("thread-id", codexMatrixSession)
		headers.Set("x-client-request-id", codexMatrixSession)
		headers.Set("x-codex-parent-thread-id", codexMatrixParent)
		headers.Set("x-codex-installation-id", codexMatrixInstallation)
		headers.Set("x-codex-window-id", fmt.Sprintf("%s:%d", codexMatrixSession, window))
		headers.Set("x-openai-subagent", "reviewer")
	}
	return headers
}

func requireCodexMatrixUUID(t *testing.T, value string, version uuid.Version) uuid.UUID {
	t.Helper()
	parsed, err := uuid.Parse(value)
	require.NoError(t, err)
	require.Equal(t, parsed.String(), value)
	require.Equal(t, version, parsed.Version())
	require.Equal(t, uuid.RFC4122, parsed.Variant())
	return parsed
}

func TestCodexFingerprintConsistencyModeMatrix(t *testing.T) {
	for _, mode := range []string{"off", "device", "session", "full"} {
		for _, convergence := range []bool{false, true} {
			for _, metadataKind := range []string{"full", "absent", "empty", "installation"} {
				for _, direct := range []bool{false, true} {
					for _, cacheKey := range []string{codexMatrixSession, "explicit-cache-key", "guardian:" + codexMatrixParent} {
						for _, passthrough := range []bool{false, true} {
							name := fmt.Sprintf("%s/convergence=%v/%s/direct=%v/%s/raw=%v", mode, convergence, metadataKind, direct, cacheKey, passthrough)
							t.Run(name, func(t *testing.T) {
								account := codexModeMatrixAccount(mode, convergence)
								input := codexModeMatrixBody(t, metadataKind, cacheKey, 9, true)
								headers, wire := codexIdentityForward(t, account, "/v1/responses",
									codexModeMatrixHeaders(direct, 9, true), input, passthrough)
								require.True(t, json.Valid(wire))
								cm := gjson.GetBytes(wire, "client_metadata")
								headerMeta := gjson.Parse(headers.Get(openAIWSTurnMetadataHeader))
								embedded := gjson.Parse(cm.Get(openAIWSTurnMetadataHeader).String())
								sessionMode := mode == "session" || mode == "full"
								wantCache := scopeCodexAccountIdentityValue(account, 81, "prompt-cache", cacheKey)
								if cacheKey == codexMatrixSession && sessionMode && (convergence || metadataKind == "full") {
									wantCache = headers.Get("session-id")
									require.NotEmpty(t, wantCache)
								}
								require.Equal(t, wantCache, gjson.GetBytes(wire, "prompt_cache_key").String())
								require.NotEqual(t, cacheKey, wantCache, "all cache keys remain credential/API-key scoped")
								if strings.HasPrefix(cacheKey, "guardian:") {
									require.True(t, strings.HasPrefix(wantCache, "guardian:"))
									if convergence {
										require.Equal(t, "guardian:"+headerMeta.Get("parent_thread_id").String(), wantCache)
									}
								}
								for _, pair := range [][2]string{{"session-id", "session_id"}, {"thread-id", "thread_id"}} {
									want := headerMeta.Get(pair[1]).String()
									require.NotEmpty(t, want)
									if convergence || sessionMode {
										require.Equal(t, want, headers.Get(pair[0]))
									} else {
										require.Empty(t, headers.Get(pair[0]), "opt-out does not synthesize native headers from metadata")
									}
									if value := cm.Get(pair[1]); value.Exists() {
										require.Equal(t, want, value.String())
									}
									if value := embedded.Get(pair[1]); value.Exists() {
										require.Equal(t, want, value.String())
									}
								}
								if convergence {
									require.Equal(t, headers.Get("thread-id"), headers.Get("x-client-request-id"))
									require.Empty(t, headers.Get("session_id"))
									require.Empty(t, headers.Get("conversation_id"))
									if cacheKey == codexMatrixSession {
										require.Equal(t, headers.Get("session-id"), wantCache)
									}
								} else if sessionMode {
									require.Equal(t, headers.Get("session-id"), headers.Get("session_id"), "legacy alias is retained")
								}
								if mode != "off" {
									require.Equal(t, codexMatrixDevice, cm.Get("x-codex-installation-id").String())
									require.Equal(t, codexMatrixDevice, headerMeta.Get("installation_id").String())
									if convergence && mode == "device" {
										require.Empty(t, headers.Get("x-codex-installation-id"))
									} else {
										require.Equal(t, codexMatrixDevice, headers.Get("x-codex-installation-id"))
									}
								}
								require.Equal(t, !(convergence && mode == "device"), headerMeta.Get("tool_namespaces_info").Exists())
								require.Contains(t, headerMeta.Raw, `"unknown":"\u0061"`)
								require.Contains(t, headerMeta.Raw, `"ratio":1e+06`)
								if metadataKind == "full" {
									require.True(t, embedded.Get("tool_namespaces_info").Exists(), "header-only projection must not strip body metadata")
									require.Equal(t, headerMeta.Get("turn_id").String(), cm.Get("turn_id").String())
									require.Equal(t, headerMeta.Get("turn_id").String(), embedded.Get("turn_id").String())
								}
							})
						}
					}
				}
			}
		}
	}
}

func TestCodexFingerprintConsistencyEvidenceCarriers(t *testing.T) {
	for _, mode := range []string{"off", "device", "session", "full"} {
		for _, passthrough := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/raw=%v", mode, passthrough), func(t *testing.T) {
				var first http.Header
				for _, carrier := range []string{"headers", "header_metadata", "body", "embedded"} {
					t.Run(carrier, func(t *testing.T) {
						headers := make(http.Header)
						metadataKind := "absent"
						switch carrier {
						case "headers":
							headers = codexModeMatrixHeaders(true, 9, true)
							headers.Del(openAIWSTurnMetadataHeader)
						case "header_metadata":
							headers = codexModeMatrixHeaders(false, 9, true)
						case "body":
							metadataKind = "full"
						case "embedded":
							metadataKind = "embedded"
						}
						account := codexModeMatrixAccount(mode, true)
						body := codexModeMatrixBody(t, metadataKind, codexMatrixSession, 9, true)
						got, wire := codexIdentityForward(t, account, "/v1/responses", headers, body, passthrough)
						for _, field := range []string{"session-id", "thread-id", "x-client-request-id"} {
							require.NotEmpty(t, got.Get(field))
							if first != nil {
								require.Equal(t, first.Get(field), got.Get(field), "carrier must not change identity derivation")
							}
						}
						if first == nil {
							first = got.Clone()
						}
						require.Equal(t, got.Get("session-id"), gjson.GetBytes(wire, "prompt_cache_key").String())
						if mode == "off" || mode == "device" {
							scoped := requireCodexMatrixUUID(t, got.Get("session-id"), uuid.Version(7))
							original := uuid.MustParse(codexMatrixSession)
							require.Equal(t, original[:6], scoped[:6])
							require.NotEqual(t, original, scoped)
						}
					})
				}
			})
		}
	}
}

func TestCodexFingerprintConsistencyUnprovenCacheKey(t *testing.T) {
	for _, mode := range []string{"off", "device", "session", "full"} {
		for _, convergence := range []bool{false, true} {
			for _, passthrough := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/convergence=%v/raw=%v", mode, convergence, passthrough), func(t *testing.T) {
					account := codexModeMatrixAccount(mode, convergence)
					var firstCache string
					for _, key := range []string{"unproven-cache-a", "unproven-cache-b", codexMatrixSession} {
						body := codexModeMatrixBody(t, "absent", key, 9, true)
						headers, wire := codexIdentityForward(t, account, "/v1/responses", nil, body, passthrough)
						got := gjson.GetBytes(wire, "prompt_cache_key").String()
						require.Equal(t, scopeCodexAccountIdentityValue(account, 81, "prompt-cache", key), got)
						require.NotEqual(t, key, got)
						require.NotEqual(t, headers.Get("session-id"), got, "a cache key alone is not independent session evidence")
						if mode == "off" || mode == "device" {
							require.Empty(t, headers.Get("session-id"))
							require.Empty(t, headers.Get("thread-id"))
							require.NotEmpty(t, headers.Get("session_id"), "legacy cache affinity must survive")
						}
						if firstCache != "" {
							require.NotEqual(t, firstCache, got)
						}
						firstCache = got
					}
				})
			}
		}
	}
}

func TestCodexFingerprintConsistencyRootParentWindow(t *testing.T) {
	for _, mode := range []string{"off", "device", "session", "full"} {
		for _, convergence := range []bool{false, true} {
			for _, root := range []bool{false, true} {
				for _, passthrough := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/convergence=%v/root=%v/raw=%v", mode, convergence, root, passthrough), func(t *testing.T) {
						account := codexModeMatrixAccount(mode, convergence)
						var previousTurn, previousSession, previousThread string
						for _, window := range []int{9, 10} {
							body := codexModeMatrixBody(t, "full", codexMatrixSession, window, root)
							h, wire := codexIdentityForward(t, account, "/v1/responses", codexModeMatrixHeaders(true, window, root), body, passthrough)
							cm := gjson.GetBytes(wire, "client_metadata")
							metadata := []gjson.Result{cm, gjson.Parse(cm.Get(openAIWSTurnMetadataHeader).String()), gjson.Parse(h.Get(openAIWSTurnMetadataHeader))}
							for _, meta := range metadata {
								turn := meta.Get("turn_id").String()
								require.NotEmpty(t, turn)
								if convergence && root {
									require.Equal(t, turn, meta.Get("root_turn_id").String())
								} else {
									wantRoot := codexMatrixAncestorTurn
									if root {
										wantRoot = codexMatrixTurn
									}
									if convergence {
										wantRoot = scopeCodexAccountIdentityValue(account, 81, "turn", wantRoot)
									}
									require.Equal(t, wantRoot, meta.Get("root_turn_id").String(), "do not invent an unproven root or change legacy opt-out")
								}
								parent := meta.Get("parent_thread_id").String()
								require.Equal(t, parent, meta.Get("forked_from_thread_id").String())
								if convergence {
									require.NotEqual(t, codexMatrixParent, parent)
									require.Equal(t, parent, h.Get("x-codex-parent-thread-id"))
									require.NotEqual(t, codexMatrixAncestorTurn, meta.Get("context_window_id").String())
									require.NotEqual(t, meta.Get("parent_turn_id").String(), meta.Get("context_window_id").String(), "context-window has a distinct namespace")
									if !root {
										require.Equal(t, meta.Get("parent_turn_id").String(), meta.Get("root_turn_id").String())
										require.NotEqual(t, turn, meta.Get("root_turn_id").String())
									}
								} else {
									require.Equal(t, codexMatrixParent, parent)
									require.Equal(t, codexMatrixAncestorTurn, meta.Get("parent_turn_id").String())
									require.Equal(t, codexMatrixAncestorTurn, meta.Get("context_window_id").String())
								}
							}
							wantNumber := window
							if !convergence && (mode == "session" || mode == "full") {
								wantNumber = 0
							}
							wantWindow := fmt.Sprintf("%s:%d", cm.Get("thread_id").String(), wantNumber)
							require.Equal(t, wantWindow, h.Get("x-codex-window-id"))
							require.Equal(t, wantWindow, cm.Get("x-codex-window-id").String())
							for _, meta := range metadata[1:] {
								require.Equal(t, wantWindow, meta.Get("window_id").String())
								require.Equal(t, int64(wantNumber), meta.Get("window_number").Int())
							}
							if previousSession != "" {
								require.Equal(t, previousSession, h.Get("session-id"))
								require.Equal(t, previousThread, h.Get("thread-id"))
							}
							if mode == "session" || mode == "full" {
								requireCodexMatrixUUID(t, cm.Get("turn_id").String(), uuid.Version(7))
								require.NotEqual(t, previousTurn, cm.Get("turn_id").String())
							} else {
								version := uuid.Version(4)
								if convergence {
									version = 7
								}
								got := requireCodexMatrixUUID(t, cm.Get("turn_id").String(), version)
								if convergence {
									original := uuid.MustParse(codexMatrixTurn)
									require.Equal(t, original[:6], got[:6])
								}
								if previousTurn != "" {
									require.Equal(t, previousTurn, got.String())
								}
							}
							previousTurn, previousSession, previousThread = cm.Get("turn_id").String(), h.Get("session-id"), h.Get("thread-id")
						}
					})
				}
			}
		}
	}
}

func TestCodexFingerprintConsistencyStagingClearsEvidence(t *testing.T) {
	for _, raw := range []bool{false, true} {
		t.Run(fmt.Sprintf("raw=%v", raw), func(t *testing.T) {
			account := codexModeMatrixAccount("off", true)
			c := codexIdentityHTTPContext("/v1/responses", nil)
			for _, body := range []string{
				`{"client_metadata":{"session_id":"scoped-session","thread_id":"scoped-thread"}}`,
				`{}`, `{"client_metadata":{"session_id":7,"thread_id":true}}`,
			} {
				if raw {
					stageCodexConvergenceBodyIdentityRaw(c, account, []byte(body))
				} else {
					var decoded map[string]any
					require.NoError(t, json.Unmarshal([]byte(body), &decoded))
					stageCodexConvergenceBodyIdentityMap(c, account, decoded)
				}
				headers := make(http.Header)
				headers.Set("session_id", "legacy-cache-affinity")
				applyCodexFingerprintConvergenceHeaders(c, account, headers)
				if strings.Contains(body, "scoped-session") {
					require.Equal(t, "scoped-session", headers.Get("session-id"))
					require.Equal(t, "scoped-thread", headers.Get("x-client-request-id"))
					require.Empty(t, headers.Get("session_id"))
					other := codexModeMatrixAccount("off", true)
					other.ID++
					next := make(http.Header)
					applyCodexFingerprintConvergenceHeaders(c, other, next)
					require.Empty(t, next, "account failover must reject another attempt's staged body")
				} else {
					require.Empty(t, headers.Get("session-id"))
					require.Empty(t, headers.Get("thread-id"))
					require.Equal(t, "legacy-cache-affinity", headers.Get("session_id"))
				}
			}
		})
	}
}

func TestCodexFingerprintConsistencyCompactCacheMatrix(t *testing.T) {
	for _, mode := range []string{"off", "device", "session", "full"} {
		for _, convergence := range []bool{false, true} {
			for _, passthrough := range []bool{false, true} {
				for _, carrier := range []string{"headers", "metadata", "none"} {
					for _, cacheKey := range []string{codexMatrixSession, "custom-cache", "guardian:" + codexMatrixParent} {
						t.Run(fmt.Sprintf("%s/convergence=%v/raw=%v/%s/%s", mode, convergence, passthrough, carrier, cacheKey), func(t *testing.T) {
							account := codexModeMatrixAccount(mode, convergence)
							input := codexModeMatrixBody(t, "full", cacheKey, 9, true)
							body, _, err := normalizeOpenAICompactRequestBody(input)
							require.NoError(t, err)
							require.Equal(t, cacheKey, gjson.GetBytes(body, "prompt_cache_key").String(), "normalization precedes selection")
							var headers http.Header
							if carrier != "none" {
								headers = codexModeMatrixHeaders(carrier == "headers", 9, true)
							}
							h, wire := codexIdentityForward(t, account, "/v1/responses/compact", headers, body, passthrough)
							require.False(t, gjson.GetBytes(wire, "client_metadata").Exists())
							got := gjson.GetBytes(wire, "prompt_cache_key")
							if mode != "device" || !convergence {
								require.False(t, got.Exists(), "legacy compact schema excludes every cache-key kind")
								return
							}
							require.Equal(t, scopeCodexAccountIdentityValue(account, 81, "prompt-cache", cacheKey), got.String())
							require.NotEqual(t, cacheKey, got.String())
							require.Empty(t, h.Get("x-client-request-id"))
							require.Equal(t, codexMatrixDevice, h.Get("x-codex-installation-id"))
							if carrier != "none" && cacheKey == codexMatrixSession {
								require.Equal(t, h.Get("session-id"), got.String())
							} else if carrier == "none" {
								require.Empty(t, h.Get("session-id"), "UUID-shaped cache key does not prove a native session")
							}
						})
					}
				}
			}
		}
	}
}

func TestCodexFingerprintConsistencyWSCurrentWindow(t *testing.T) {
	for _, mode := range []string{"off", "device", "session", "full"} {
		for _, convergence := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/convergence=%v", mode, convergence), func(t *testing.T) {
				account := codexModeMatrixAccount(mode, convergence)
				// The connection retains its first handshake while each frame advances.
				c := codexIdentityHTTPContext("/v1/responses", codexModeMatrixHeaders(true, 9, true))
				var previousWindow, previousTurn string
				for _, window := range []int{9, 10} {
					body := codexModeMatrixBody(t, "full", codexMatrixSession, window, true)
					payload, err := applyCodexIdentityToWSPayload(c, account, body)
					require.NoError(t, err)
					h, _, err := (&OpenAIGatewayService{}).buildOpenAIWSHeaders(context.Background(), c, account,
						"offline-token", OpenAIWSProtocolDecision{Transport: OpenAIUpstreamTransportResponsesWebsocketV2},
						true, "", c.GetHeader(openAIWSTurnMetadataHeader), codexMatrixSession, "gpt-5.4", "")
					require.NoError(t, err)
					cm := gjson.GetBytes(payload, "client_metadata")
					embedded := gjson.Parse(cm.Get(openAIWSTurnMetadataHeader).String())
					wantWindow := fmt.Sprintf("%s:%d", cm.Get("thread_id").String(), window)
					require.Equal(t, wantWindow, cm.Get("x-codex-window-id").String())
					require.Equal(t, wantWindow, embedded.Get("window_id").String())
					require.Equal(t, int64(window), embedded.Get("window_number").Int())
					require.NotEqual(t, previousWindow, wantWindow, "stale handshake evidence must not fix the current frame's window")
					if convergence {
						require.Equal(t, h.Get("session-id"), cm.Get("session_id").String())
						require.Equal(t, h.Get("thread-id"), cm.Get("thread_id").String())
						require.Equal(t, h.Get("session-id"), gjson.GetBytes(payload, "prompt_cache_key").String())
						require.Equal(t, cm.Get("turn_id").String(), cm.Get("root_turn_id").String())
						require.Equal(t, cm.Get("turn_id").String(), embedded.Get("root_turn_id").String())
						if mode == "session" || mode == "full" {
							require.NotEqual(t, previousTurn, cm.Get("turn_id").String())
							require.Equal(t, wantWindow, h.Get("x-codex-window-id"))
						}
					} else {
						// Local WS opt-out scopes raw identities but does not stage
						// session/full fingerprints through this helper.
						requireCodexMatrixUUID(t, cm.Get("turn_id").String(), uuid.Version(4))
						require.Equal(t, codexMatrixTurn, cm.Get("root_turn_id").String())
						require.NotEqual(t, codexMatrixDevice, cm.Get("x-codex-installation-id").String())
						if previousTurn != "" {
							require.Equal(t, previousTurn, cm.Get("turn_id").String())
						}
					}
					previousWindow, previousTurn = wantWindow, cm.Get("turn_id").String()
				}
			})
		}
	}
}

package service

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestCodexWSIntegrationHTTPBridgeDeviceWire(t *testing.T) {
	const state = "bridge-device-delivered-state"
	const turn = "0198f0df-0123-7abc-8def-123456789abc"
	metadata := `{"session_id":"` + codexIdentitySessionFixture +
		`","thread_id":"` + codexIdentitySessionFixture +
		`","turn_id":"` + turn + `","root_turn_id":"` + turn +
		`","window_id":"` + codexIdentitySessionFixture +
		`:3","window_number":3,"installation_id":"client-installation",` +
		`"tool_namespaces_info":["shell"],"opaque":"\u4fdd\u7559","ratio":1e+06}`
	for _, mode := range []string{OpenAIWSIngressModeCtxPool, OpenAIWSIngressModePassthrough} {
		for _, carrier := range []string{"header", "frame"} {
			for _, sameCredential := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/same_credential=%v", mode, carrier, sameCredential), func(t *testing.T) {
					upstream := newCodexWSIntegrationUpstream(t, nil)
					svc := newCodexWSIntegrationService(t, upstream, false)
					svc.cfg.Gateway.OpenAIWS.HTTPBridgeEnabled = true
					svc.cfg.Gateway.OpenAIWS.HTTPBridgeThresholdBytes = 1
					httpUpstream := svc.httpUpstream.(*httpUpstreamRecorder)
					httpUpstream.responses = []*http.Response{
						codexWSIntegrationHTTPResponse(codexWSIntegrationMetadata(state), codexWSIntegrationCompleted("resp_device_1")),
						codexWSIntegrationHTTPResponse(codexWSIntegrationCompleted("resp_device_2")),
					}
					headers := codexWSIntegrationHeaders()
					headers.Set("session-id", codexIdentitySessionFixture)
					headers.Set("thread-id", codexIdentitySessionFixture)
					headers.Set(openAIWSTurnMetadataHeader, metadata)
					headers.Set("x-codex-inference-call-id", "inbound-http-trace")
					frame := []byte(codexWSIntegrationFrame)
					for key, value := range map[string]any{
						"prompt_cache_key":                        codexIdentitySessionFixture,
						"client_metadata.session_id":              codexIdentitySessionFixture,
						"client_metadata.thread_id":               codexIdentitySessionFixture,
						"client_metadata.x-codex-window-id":       codexIdentitySessionFixture + ":3",
						"client_metadata.x-codex-installation-id": "client-installation",
						"client_metadata.x-codex-turn-metadata":   metadata,
					} {
						var err error
						frame, err = sjson.SetBytes(frame, key, value)
						require.NoError(t, err)
					}

					first := codexWSIntegrationAccount(21901, "credential-a", true, mode)
					events := runCodexWSIntegrationIngress(t, svc, first, headers, frame)
					require.Equal(t, codexWSIntegrationMetadata(state), events[0])
					namespace := "credential-b"
					if sameCredential {
						namespace = "credential-a"
					}
					second := codexWSIntegrationAccount(21902, namespace, true, mode)
					if !sameCredential {
						second.Extra[codexFingerprintSeedExtraKey] = "28113a25-6d63-4da2-bd12-81df3ac5b8a6"
					}
					if carrier == "header" {
						headers.Set(openAICodexTurnStateHeader, state)
					} else {
						var err error
						frame, err = sjson.SetBytes(frame, "client_metadata."+openAICodexTurnStateHeader, state)
						require.NoError(t, err)
					}
					runCodexWSIntegrationIngress(t, svc, second, headers, frame)
					handshakes, frames := upstream.snapshot()
					require.Empty(t, handshakes, "both turns must route to HTTP, not upstream WS")
					require.Empty(t, frames)
					require.Len(t, httpUpstream.requests, 2)

					installations := make([]string, 0, 2)
					sessions := make([]string, 0, 2)
					for i, request := range httpUpstream.requests {
						raw := httpUpstream.bodies[i]
						require.True(t, strings.HasSuffix(request.URL.Path, "/responses"))
						require.Equal(t, "zstd", request.Header.Get("Content-Encoding"))
						require.Equal(t, int64(len(raw)), request.ContentLength)
						require.Greater(t, len(raw), 6)
						require.Equal(t, []byte{0x28, 0xb5, 0x2f, 0xfd, 0, 0x58}, raw[:6])
						decoder, err := zstd.NewReader(nil)
						require.NoError(t, err)
						body, err := decoder.DecodeAll(raw, nil)
						decoder.Close()
						require.NoError(t, err)
						require.True(t, bytes.HasPrefix(body, []byte(`{"model":`)), string(body))
						require.False(t, gjson.GetBytes(body, "type").Exists())
						require.Empty(t, request.Header.Get("OpenAI-Beta"))
						require.Empty(t, request.Header.Get("x-codex-installation-id"))
						require.Empty(t, request.Header.Get("x-codex-inference-call-id"), "WS ingress must not manufacture an HTTP client trace")
						require.Empty(t, request.Header.Get("session_id"))
						session := request.Header.Get("session-id")
						require.NotEmpty(t, session)
						require.NotEqual(t, codexIdentitySessionFixture, session)
						require.Equal(t, session, request.Header.Get("thread-id"))
						require.Equal(t, session, gjson.GetBytes(body, "client_metadata.session_id").String())
						require.Equal(t, session, gjson.GetBytes(body, "prompt_cache_key").String())
						embedded := gjson.GetBytes(body, "client_metadata.x-codex-turn-metadata").String()
						require.Equal(t, session, gjson.Get(embedded, "session_id").String())
						require.Equal(t, session+":3", gjson.Get(embedded, "window_id").String())
						require.Equal(t, gjson.Get(embedded, "turn_id").String(), gjson.Get(embedded, "root_turn_id").String())
						require.Contains(t, embedded, `"opaque":"\u4fdd\u7559"`)
						require.Contains(t, embedded, `"ratio":1e+06`)
						require.True(t, gjson.Get(embedded, "tool_namespaces_info").Exists())
						headerMetadata := request.Header.Get(openAIWSTurnMetadataHeader)
						require.False(t, gjson.Get(headerMetadata, "tool_namespaces_info").Exists())
						require.Equal(t, session, gjson.Get(headerMetadata, "session_id").String())
						installation := gjson.GetBytes(body, "client_metadata.x-codex-installation-id").String()
						require.NotEmpty(t, installation)
						require.Equal(t, installation, gjson.Get(embedded, "installation_id").String())
						require.Equal(t, "a <b> & c", gjson.GetBytes(body, "client_metadata.keep").String())
						installations = append(installations, installation)
						sessions = append(sessions, session)
						if i == 1 {
							want := ""
							if sameCredential {
								want = state
							}
							got := request.Header.Get(openAICodexTurnStateHeader)
							if carrier == "frame" {
								got = gjson.GetBytes(body, "client_metadata."+openAICodexTurnStateHeader).String()
							}
							require.Equal(t, want, got)
						}
					}
					if sameCredential {
						require.Equal(t, installations[0], installations[1])
						require.Equal(t, sessions[0], sessions[1])
					} else {
						require.NotEqual(t, installations[0], installations[1])
						require.NotEqual(t, sessions[0], sessions[1])
					}
				})
			}
		}
	}
}

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const codexMatrixInferenceHeader = "x-codex-inference-call-id"

func TestCodexInferenceCallMatrixHTTPAndCompact(t *testing.T) {
	for _, mode := range []string{"off", "device", "session", "full"} {
		for _, convergence := range []bool{false, true} {
			for _, passthrough := range []bool{false, true} {
				for _, path := range []string{"/v1/responses", "/v1/responses/compact"} {
					for _, inbound := range []string{"", " \t ", "bbd9bf7b-cb3d-48e7-bdcb-1c4bba7ee0a1", "opaque-client-trace"} {
						t.Run(fmt.Sprintf("%s/convergence=%v/raw=%v%s/trace=%q", mode, convergence, passthrough, path, inbound), func(t *testing.T) {
							account := codexModeMatrixAccount(mode, convergence)
							body := codexModeMatrixBody(t, "full", codexMatrixSession, 9, true)
							if strings.HasSuffix(path, "/compact") {
								var err error
								body, _, err = normalizeOpenAICompactRequestBody(body)
								require.NoError(t, err)
							}
							headers := codexModeMatrixHeaders(true, 9, true)
							headers.Set(codexMatrixInferenceHeader, inbound)
							got, wire := codexIdentityForward(t, account, path, headers, body, passthrough)
							require.True(t, gjson.ValidBytes(wire), "inspect the actual serialized request")
							value := got.Get(codexMatrixInferenceHeader)
							wantTrace := mode == "device" && convergence && path == "/v1/responses" && strings.TrimSpace(inbound) != ""
							if !wantTrace {
								require.Empty(t, value)
								return
							}
							requireCodexMatrixUUID(t, value, uuid.Version(4))
							require.NotEqual(t, inbound, value)
							require.Equal(t, inbound, headers.Get(codexMatrixInferenceHeader), "the caller's trace is not rewritten in place")
							retry, _ := codexIdentityForward(t, account, path, headers, body, passthrough)
							requireCodexMatrixUUID(t, retry.Get(codexMatrixInferenceHeader), uuid.Version(4))
							require.NotEqual(t, value, retry.Get(codexMatrixInferenceHeader))
						})
					}
				}
			}
		}
	}
}

func TestCodexInferenceCallMatrixBuilderConditions(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, tc := range []struct {
			name      string
			method    string
			path      string
			transport OpenAIClientTransport
			want      bool
		}{
			{"post", http.MethodPost, "/v1/responses", "", true},
			{"unversioned", http.MethodPost, "/responses", "", true},
			{"query", http.MethodPost, "/v1/responses?trace=1", "", true},
			{"get", http.MethodGet, "/v1/responses", "", false},
			{"put", http.MethodPut, "/v1/responses", "", false},
			{"compact", http.MethodPost, "/v1/responses/compact", "", false},
			{"nested_response", http.MethodPost, "/v1/responses/resp_offline", "", false},
			{"alpha", http.MethodPost, "/v1/alpha/search", "", false},
			{"images", http.MethodPost, "/v1/images/generations", "", false},
			{"ws_to_http", http.MethodPost, "/v1/responses", OpenAIClientTransportWS, false},
		} {
			t.Run(fmt.Sprintf("%s/raw=%v", tc.name, passthrough), func(t *testing.T) {
				c := codexIdentityHTTPContext(tc.path, nil)
				c.Request.Method = tc.method
				c.Request.Header.Set(codexMatrixInferenceHeader, "downstream-trace")
				if tc.transport != "" {
					SetOpenAIClientTransport(c, tc.transport)
				}
				account := codexModeMatrixAccount("device", true)
				svc := &OpenAIGatewayService{}
				body := []byte(`{"model":"gpt-5.4","input":"offline"}`)
				var previous string
				for attempt := 0; attempt < 3; attempt++ {
					if attempt == 2 {
						account = codexModeMatrixAccount("device", true)
						account.ID++
						account.Credentials["chatgpt_account_id"] = "offline-failover"
					}
					var req *http.Request
					var err error
					if passthrough {
						req, err = svc.buildUpstreamRequestOpenAIPassthrough(context.Background(), c, account, body, "offline-token")
					} else {
						req, err = svc.buildUpstreamRequest(context.Background(), c, account, body, "offline-token", true, "", true)
					}
					require.NoError(t, err)
					sent, err := io.ReadAll(req.Body)
					require.NoError(t, err)
					require.NoError(t, req.Body.Close())
					require.NotEmpty(t, sent)
					got := req.Header.Get(codexMatrixInferenceHeader)
					if tc.want {
						requireCodexMatrixUUID(t, got, uuid.Version(4))
						require.NotEqual(t, "downstream-trace", got)
						require.NotEqual(t, previous, got, "same-context retries and failover each mint a fresh trace")
						previous = got
					} else {
						require.Empty(t, got)
					}
				}
			})
		}
	}
}

func TestCodexInferenceCallMatrixWebSocketHeaders(t *testing.T) {
	for _, mode := range []string{"off", "device", "session", "full"} {
		for _, convergence := range []bool{false, true} {
			for _, transport := range []OpenAIUpstreamTransport{
				OpenAIUpstreamTransportResponsesWebsocket, OpenAIUpstreamTransportResponsesWebsocketV2,
			} {
				t.Run(fmt.Sprintf("%s/convergence=%v/%s", mode, convergence, transport), func(t *testing.T) {
					c := codexIdentityHTTPContext("/v1/responses", codexModeMatrixHeaders(true, 9, true))
					c.Request.Header.Set(codexMatrixInferenceHeader, "downstream-trace")
					account := codexModeMatrixAccount(mode, convergence)
					svc := &OpenAIGatewayService{}
					body := codexModeMatrixBody(t, "full", codexMatrixSession, 9, true)
					payload, err := applyCodexIdentityToWSPayload(c, account, body)
					require.NoError(t, err)
					headers, _, err := svc.buildOpenAIWSHeaders(context.Background(), c, account, "offline-token",
						OpenAIWSProtocolDecision{Transport: transport}, true, "", c.GetHeader(openAIWSTurnMetadataHeader), codexMatrixSession, "gpt-5.4", "")
					require.NoError(t, err)
					require.NotEmpty(t, headers.Get("Authorization"), "exercise a fully built handshake")
					require.NotEmpty(t, headers.Get("OpenAI-Beta"))
					require.Empty(t, headers.Get(codexMatrixInferenceHeader))
					require.NotContains(t, string(payload), codexMatrixInferenceHeader)
					if convergence {
						require.Equal(t, headers.Get("session-id"), gjson.GetBytes(payload, "client_metadata.session_id").String())
					}
				})
			}
		}
	}
}

func TestCodexInferenceCallMatrixAuxiliaryRequests(t *testing.T) {
	for _, mode := range []string{"off", "device", "session", "full"} {
		for _, convergence := range []bool{false, true} {
			for _, endpoint := range []string{"alpha", "pat_responses", "images"} {
				t.Run(fmt.Sprintf("%s/convergence=%v/%s", mode, convergence, endpoint), func(t *testing.T) {
					account := codexModeMatrixAccount(mode, convergence)
					headers := codexModeMatrixHeaders(true, 9, true)
					headers.Set(codexMatrixInferenceHeader, "downstream-trace")
					svc := &OpenAIGatewayService{cfg: &config.Config{}}
					body := []byte(`{"model":"gpt-5.4","id":"offline-session","query":"offline"}`)
					var req *http.Request
					if endpoint == "images" {
						c := codexIdentityHTTPContext("/v1/images/generations", headers)
						upstream := &httpUpstreamRecorder{resp: auxiliaryDirectImageResponse()}
						svc.httpUpstream = upstream
						input := []byte(`{"model":"gpt-image-2","prompt":"draw a square"}`)
						parsed, err := svc.ParseOpenAIImagesRequest(c, input)
						require.NoError(t, err)
						result, err := svc.ForwardImages(context.Background(), c, account, input, parsed, "")
						require.NoError(t, err)
						require.NotNil(t, result)
						req = upstream.lastReq
						require.NotNil(t, req)
						require.NotEmpty(t, upstream.lastBody)
					} else {
						c := codexIdentityHTTPContext("/v1/alpha/search", headers)
						var err error
						if endpoint == "alpha" {
							req, err = svc.buildOpenAIAlphaSearchRequest(context.Background(), c, account, body, "offline-token")
						} else {
							account.Type = AccountTypeSetupToken
							req, err = svc.buildOpenAIAlphaSearchResponsesWebSearchRequest(context.Background(), c, account, body,
								[]byte(`{"model":"gpt-5.4","input":"offline","tools":[{"type":"web_search"}]}`), "offline-token")
						}
						require.NoError(t, err)
						sent, err := io.ReadAll(req.Body)
						require.NoError(t, err)
						require.NoError(t, req.Body.Close())
						require.NotEmpty(t, sent)
					}
					require.Equal(t, http.MethodPost, req.Method)
					require.NotEmpty(t, req.Header.Get("Authorization"))
					require.Empty(t, req.Header.Get(codexMatrixInferenceHeader), "auxiliary ingress must not mint a direct Responses trace")
					if endpoint == "alpha" {
						require.True(t, strings.HasSuffix(req.URL.Path, "/alpha/search"))
					} else if endpoint == "images" {
						require.True(t, strings.HasSuffix(req.URL.Path, "/images/generations"))
					} else {
						require.True(t, strings.HasSuffix(req.URL.Path, "/responses"), "exclusion follows ingress, not the auxiliary upstream URL")
					}
				})
			}
		}
	}
}

func TestCodexInferenceCallMatrixCredentialGate(t *testing.T) {
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken, AccountTypeAPIKey} {
		for _, parentEnabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/source=%v", accountType, parentEnabled), func(t *testing.T) {
				child := codexModeMatrixAccount("device", !parentEnabled)
				child.Type = accountType
				parent := codexModeMatrixAccount("off", parentEnabled)
				c := codexIdentityHTTPContext("/v1/responses", nil)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				c.Request.Header.Set(codexMatrixInferenceHeader, "downstream-trace")
				c.Set(codexAccountIdentitySourceContextKey, parent)
				req, err := (&OpenAIGatewayService{}).buildUpstreamRequest(context.Background(), c, child,
					[]byte(`{"model":"gpt-5.4","input":"offline"}`), "offline-token", false, "", true)
				require.NoError(t, err)
				require.NoError(t, req.Body.Close())
				if parentEnabled && accountType != AccountTypeAPIKey {
					requireCodexMatrixUUID(t, req.Header.Get(codexMatrixInferenceHeader), uuid.Version(4))
				} else {
					require.Empty(t, req.Header.Get(codexMatrixInferenceHeader))
				}
			})
		}
	}
}

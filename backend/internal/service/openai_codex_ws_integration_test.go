package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const codexWSIntegrationFrame = `{"client_metadata":{"session_id":"ws-session","keep":"a <b> & c"},"input":[{"type":"message","role":"user","content":"hello"}],"instructions":"wire integration","stream":true,"model":"gpt-5.5","type":"response.create"}`

type codexWSIntegrationFrameCapture struct {
	messageType coderws.MessageType
	payload     []byte
}

// Capture bytes after the production dialer/connection encoder, not a JSON map.
type codexWSIntegrationUpstream struct {
	mu         sync.Mutex
	headers    []http.Header
	frames     []codexWSIntegrationFrameCapture
	handshakes []http.Header
	turns      [][][]byte
	server     *httptest.Server
}

func newCodexWSIntegrationUpstream(t *testing.T, handshakes []http.Header, turns ...[][]byte) *codexWSIntegrationUpstream {
	t.Helper()
	upstream := &codexWSIntegrationUpstream{handshakes: handshakes, turns: turns}
	upstream.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream.mu.Lock()
		connection := len(upstream.headers)
		upstream.headers = append(upstream.headers, r.Header.Clone())
		if connection < len(upstream.handshakes) {
			for name, values := range upstream.handshakes[connection] {
				w.Header()[name] = append([]string(nil), values...)
			}
		}
		upstream.mu.Unlock()
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
		if err != nil {
			t.Errorf("accept upstream websocket: %v", err)
			return
		}
		defer func() { _ = conn.CloseNow() }()
		for {
			messageType, payload, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			upstream.mu.Lock()
			turn := len(upstream.frames)
			upstream.frames = append(upstream.frames, codexWSIntegrationFrameCapture{
				messageType: messageType,
				payload:     append([]byte(nil), payload...),
			})
			events := [][]byte{codexWSIntegrationCompleted(fmt.Sprintf("resp_ws_%d", turn+1))}
			if turn < len(upstream.turns) && upstream.turns[turn] != nil {
				events = upstream.turns[turn]
			}
			upstream.mu.Unlock()
			for _, event := range events {
				if err := conn.Write(r.Context(), coderws.MessageText, event); err != nil {
					return
				}
			}
		}
	}))
	t.Cleanup(upstream.server.Close)
	return upstream
}

func (u *codexWSIntegrationUpstream) snapshot() ([]http.Header, []codexWSIntegrationFrameCapture) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]http.Header(nil), u.headers...), append([]codexWSIntegrationFrameCapture(nil), u.frames...)
}

type codexWSIntegrationDialer struct {
	url   string
	calls atomic.Int64
}

func (d *codexWSIntegrationDialer) Dial(ctx context.Context, _ string, headers http.Header, proxyURL string) (openAIWSClientConn, int, http.Header, error) {
	d.calls.Add(1)
	return (&coderOpenAIWSClientDialer{}).Dial(ctx, d.url, headers, proxyURL)
}

func codexWSIntegrationCompleted(id string) []byte {
	return []byte(`{"type":"response.completed","response":{"id":"` + id + `","model":"gpt-5.5","output":[],"usage":{"input_tokens":2,"output_tokens":1}}}`)
}

func codexWSIntegrationMetadata(blob string) []byte {
	return []byte(`{"type":"response.metadata","headers":{"X-Codex-Turn-State":"` + blob + `"}}`)
}

func codexWSIntegrationAccount(id int64, namespace string, wireProfile bool, mode string) *Account {
	return &Account{
		ID: id, Name: "codex-ws-integration", Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"access_token": "offline-token", "chatgpt_account_id": namespace},
		Extra: map[string]any{
			codexFingerprintModeExtraKey:                   "device",
			codexFingerprintSeedExtraKey:                   testCodexFingerprintSeed,
			codexFingerprintConvergenceExtraKey:            wireProfile,
			"openai_oauth_responses_websockets_v2_mode":    mode,
			"openai_oauth_responses_websockets_v2_enabled": true,
			"openai_passthrough":                           false,
		},
	}
}

func newCodexWSIntegrationService(t *testing.T, upstream *codexWSIntegrationUpstream, prewarm bool) *OpenAIGatewayService {
	t.Helper()
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.IngressModeDefault = OpenAIWSIngressModeCtxPool
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.PrewarmGenerateEnabled = prewarm
	dialer := &codexWSIntegrationDialer{url: "ws" + strings.TrimPrefix(upstream.server.URL, "http")}
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(dialer)
	t.Cleanup(pool.Close)
	return &OpenAIGatewayService{
		cfg: cfg, httpUpstream: &httpUpstreamRecorder{}, cache: &stubGatewayCache{},
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector(),
		openaiWSPool: pool, openaiWSPassthroughDialer: &codexWSIntegrationDialer{url: dialer.url},
	}
}

func codexWSIntegrationHeaders() http.Header {
	headers := http.Header{}
	headers.Set("User-Agent", "codex_cli_rs/0.98.0")
	headers.Set("session-id", "ws-session")
	headers.Set("thread-id", "ws-session")
	headers.Set(openAIWSTurnMetadataHeader, `{ "marker" : "header", "opaque":"\u8bbe\u5907" }`)
	return headers
}

func codexWSIntegrationContext(headers http.Header, body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header = headers.Clone()
	return c, recorder
}

func forwardCodexWSIntegration(t *testing.T, svc *OpenAIGatewayService, account *Account, headers http.Header, frame []byte) (*gin.Context, *httptest.ResponseRecorder, *OpenAIForwardResult) {
	t.Helper()
	body, err := sjson.DeleteBytes(frame, "type")
	require.NoError(t, err)
	c, recorder := codexWSIntegrationContext(headers, body)
	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.OpenAIWSMode, "must exercise WS, not an HTTP fallback")
	require.NotEmpty(t, c.GetString(OpsOpenAIWSConnIDKey), "must reach the native v2 pool entrypoint")
	return c, recorder, result
}

func startCodexWSIntegrationIngress(t *testing.T, svc *OpenAIGatewayService, account *Account, headers http.Header, hooks *OpenAIWSIngressHooks, beforeRelay func(*coderws.Conn)) (*coderws.Conn, <-chan error) {
	t.Helper()
	serverErrors := make(chan error, 1)
	passthroughDialer := svc.openaiWSPassthroughDialer.(*codexWSIntegrationDialer)
	initialPassthroughDials := passthroughDialer.calls.Load()
	httpUpstream := svc.httpUpstream.(*httpUpstreamRecorder)
	initialHTTPRequests := len(httpUpstream.requests)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			serverErrors <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()
		c, _ := codexWSIntegrationContext(headers, nil)
		c.Request = c.Request.WithContext(r.Context())
		// Match the real ResponsesWebSocket handler's ingress transport marker.
		SetOpenAIClientTransport(c, OpenAIClientTransportWS)
		messageType, first, err := conn.Read(r.Context())
		if err != nil {
			serverErrors <- err
			return
		}
		if messageType != coderws.MessageText {
			serverErrors <- fmt.Errorf("unexpected first message type %v", messageType)
			return
		}
		if beforeRelay != nil {
			beforeRelay(conn)
		}
		err = svc.ProxyResponsesWebSocketFromClient(r.Context(), c, conn, account, "offline-token", first, hooks)
		wantBridge := svc.cfg.Gateway.OpenAIWS.HTTPBridgeEnabled && svc.cfg.Gateway.OpenAIWS.HTTPBridgeThresholdBytes == 1
		wantPassthrough := !wantBridge && account.Extra["openai_oauth_responses_websockets_v2_mode"] == OpenAIWSIngressModePassthrough
		usedPassthrough := passthroughDialer.calls.Load() > initialPassthroughDials
		if usedPassthrough != wantPassthrough {
			err = fmt.Errorf("wrong ingress route: passthrough=%v, want %v", usedPassthrough, wantPassthrough)
		}
		usedHTTP := len(httpUpstream.requests) > initialHTTPRequests
		if usedHTTP != wantBridge || c.GetBool("openai_ws_http_bridge") != wantBridge {
			err = fmt.Errorf("wrong ingress route: HTTP bridge=%v, want %v", usedHTTP, wantBridge)
		}
		serverErrors <- err
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.CloseNow() })
	return client, serverErrors
}

func awaitCodexWSIntegrationIngress(t *testing.T, serverErrors <-chan error) {
	t.Helper()
	select {
	case err := <-serverErrors:
		if err != nil {
			require.True(t, isOpenAIWSClientDisconnectError(err), "ingress failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("websocket ingress did not finish")
	}
}

func runCodexWSIntegrationIngress(t *testing.T, svc *OpenAIGatewayService, account *Account, headers http.Header, frames ...[]byte) [][]byte {
	t.Helper()
	client, serverErrors := startCodexWSIntegrationIngress(t, svc, account, headers, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var downstream [][]byte
	for _, frame := range frames {
		require.NoError(t, client.Write(ctx, coderws.MessageText, frame))
		for {
			messageType, event, err := client.Read(ctx)
			require.NoError(t, err)
			require.Equal(t, coderws.MessageText, messageType)
			downstream = append(downstream, event)
			if gjson.GetBytes(event, "type").String() == "response.completed" {
				break
			}
		}
	}
	require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
	awaitCodexWSIntegrationIngress(t, serverErrors)
	return downstream
}

func requireCodexWSIntegrationWireFrame(t *testing.T, frame codexWSIntegrationFrameCapture, started time.Time) {
	t.Helper()
	require.Equal(t, coderws.MessageText, frame.messageType)
	require.True(t, gjson.ValidBytes(frame.payload), "%s", frame.payload)
	require.Contains(t, string(frame.payload), "a <b> & c")
	require.NotContains(t, string(frame.payload), `\u003c`)
	require.False(t, bytes.HasSuffix(frame.payload, []byte("\n")), "raw text must not add an encoder newline")
	order := []string{
		"type", "model", "instructions", "previous_response_id", "input", "tools", "tool_choice",
		"parallel_tool_calls", "reasoning", "store", "stream", "stream_options", "include",
		"service_tier", "prompt_cache_key", "text", "generate", "client_metadata", "access_programs",
	}
	var keys []string
	gjson.ParseBytes(frame.payload).ForEach(func(key, _ gjson.Result) bool {
		keys = append(keys, key.String())
		return true
	})
	require.Equal(t, "type", keys[0])
	last := -1
	for _, key := range keys {
		for rank, known := range order {
			if key == known {
				require.Greater(t, rank, last, "out-of-order key %q in %s", key, frame.payload)
				last = rank
				break
			}
		}
	}
	stamp := gjson.GetBytes(frame.payload, "client_metadata.x-codex-ws-stream-request-start-ms")
	require.Equal(t, gjson.String, stamp.Type)
	ms, err := strconv.ParseInt(stamp.Str, 10, 64)
	require.NoError(t, err)
	require.GreaterOrEqual(t, ms, started.UnixMilli())
	require.LessOrEqual(t, ms, time.Now().UnixMilli())
}

func TestCodexWSIntegrationV2GuardsFrameTurnStateBeforePrewarmAndGenerate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, enabled := range []bool{false, true} {
		for _, prewarm := range []bool{false, true} {
			for _, sameCredential := range []bool{false, true} {
				t.Run(fmt.Sprintf("wire=%v/prewarm=%v/same_credential=%v", enabled, prewarm, sameCredential), func(t *testing.T) {
					const blob = "relayed-metadata-state"
					upstream := newCodexWSIntegrationUpstream(t, nil, [][]byte{
						codexWSIntegrationMetadata(blob), codexWSIntegrationCompleted("resp_minter"),
					})
					svc := newCodexWSIntegrationService(t, upstream, prewarm)
					minter := codexWSIntegrationAccount(21001, "credential-a", enabled, OpenAIWSIngressModeCtxPool)
					delivered := runCodexWSIntegrationIngress(t, svc, minter, codexWSIntegrationHeaders(), []byte(codexWSIntegrationFrame))
					require.Contains(t, string(delivered[0]), blob, "provenance must originate from a delivered event")

					namespace := "credential-b"
					if sameCredential {
						namespace = "credential-a"
					}
					account := codexWSIntegrationAccount(21002, namespace, enabled, OpenAIWSIngressModeCtxPool)
					frame, err := sjson.SetBytes([]byte(codexWSIntegrationFrame), "client_metadata.x-codex-turn-state", blob)
					require.NoError(t, err)
					started := time.Now()
					forwardCodexWSIntegration(t, svc, account, codexWSIntegrationHeaders(), frame)
					headers, sent := upstream.snapshot()
					require.Len(t, headers, 2)
					wantWrites := 2
					if prewarm {
						wantWrites++
					}
					require.Len(t, sent, wantWrites)
					if prewarm {
						require.Equal(t, "false", gjson.GetBytes(sent[1].payload, "generate").Raw)
					}
					require.False(t, gjson.GetBytes(sent[len(sent)-1].payload, "generate").Exists())
					for _, wire := range sent[1:] {
						if enabled {
							requireCodexWSIntegrationWireFrame(t, wire, started)
						}
						state := gjson.GetBytes(wire.payload, "client_metadata.x-codex-turn-state")
						if sameCredential {
							require.Equal(t, blob, state.String(), "a second local row in the same credential namespace may echo")
						} else {
							require.False(t, state.Exists(), "foreign state leaked in frame: %s", wire.payload)
						}
						require.Equal(t, "a <b> & c", gjson.GetBytes(wire.payload, "client_metadata.keep").String())
					}
				})
			}
		}
	}
}

func TestCodexWSIntegrationEntrypointWireProfile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{"v2", "v2-prewarm", OpenAIWSIngressModeCtxPool, OpenAIWSIngressModePassthrough} {
		for _, enabled := range []bool{false, true} {
			for _, ownMetadata := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/wire=%v/own_metadata=%v", mode, enabled, ownMetadata), func(t *testing.T) {
					upstream := newCodexWSIntegrationUpstream(t, nil)
					svc := newCodexWSIntegrationService(t, upstream, mode == "v2-prewarm")
					accountMode := mode
					if strings.HasPrefix(mode, "v2") {
						accountMode = OpenAIWSIngressModeCtxPool
					}
					account := codexWSIntegrationAccount(21101, "wire-credential", enabled, accountMode)
					headers := codexWSIntegrationHeaders()
					headers.Set(openAICodexTurnStateHeader, "client-header-state")
					frame, err := sjson.SetBytes([]byte(codexWSIntegrationFrame), "client_metadata.x-codex-ws-stream-request-start-ms", "123456")
					require.NoError(t, err)
					if ownMetadata {
						frame, err = sjson.SetBytes(frame, "client_metadata.x-codex-turn-metadata", `{ "marker" : "frame-1", "opaque":"\u8bbe\u5907" }`)
						require.NoError(t, err)
						frame, err = sjson.SetBytes(frame, "client_metadata.x-codex-turn-state", "frame-state")
						require.NoError(t, err)
					}
					second, err := sjson.SetBytes(frame, "client_metadata.x-codex-turn-metadata", `{ "marker" : "frame-2", "opaque":"\u8bbe\u5907" }`)
					require.NoError(t, err)
					second, err = sjson.SetBytes(second, "client_metadata.x-codex-turn-state", "second-state")
					require.NoError(t, err)
					third, err := sjson.SetBytes([]byte(codexWSIntegrationFrame), "client_metadata.x-codex-ws-stream-request-start-ms", "123456")
					require.NoError(t, err)
					started := time.Now()
					if strings.HasPrefix(mode, "v2") {
						forwardCodexWSIntegration(t, svc, account, headers, frame)
						forwardCodexWSIntegration(t, svc, account, headers, second)
						forwardCodexWSIntegration(t, svc, account, headers, third)
					} else {
						runCodexWSIntegrationIngress(t, svc, account, headers, frame, second, third)
					}
					handshakes, sent := upstream.snapshot()
					require.Len(t, handshakes, 1, "second turn must reuse the connection")
					wantWrites, secondIndex := 3, 1
					if mode == "v2-prewarm" {
						wantWrites++
						secondIndex++
					}
					require.Len(t, sent, wantWrites)
					if mode == "v2-prewarm" {
						require.Equal(t, "false", gjson.GetBytes(sent[0].payload, "generate").Raw)
					}
					if enabled {
						require.Empty(t, handshakes[0].Get(openAICodexTurnStateHeader))
					} else {
						require.Equal(t, "client-header-state", handshakes[0].Get(openAICodexTurnStateHeader))
					}
					for i, wire := range sent {
						if enabled {
							requireCodexWSIntegrationWireFrame(t, wire, started)
							require.Equal(t, handshakes[0].Get("session-id"), gjson.GetBytes(wire.payload, "client_metadata.session_id").String(),
								"Forward already scoped v2 metadata; sending must not scope it again")
							require.NotEqual(t, "ws-session", handshakes[0].Get("session-id"))
						} else {
							require.Equal(t, "123456", gjson.GetBytes(wire.payload, "client_metadata.x-codex-ws-stream-request-start-ms").String())
							require.Equal(t, mode != OpenAIWSIngressModePassthrough, bytes.HasSuffix(wire.payload, []byte("\n")))
							if mode != OpenAIWSIngressModePassthrough {
								require.Contains(t, string(wire.payload), `\u003c`, "non-profile JSON encoding remains unchanged")
							}
						}
						wantMarker, wantState := "header", ""
						if enabled {
							wantState = "client-header-state"
						}
						if ownMetadata && i < secondIndex {
							wantMarker, wantState = "frame-1", "frame-state"
						}
						if i == secondIndex {
							wantMarker, wantState = "frame-2", "second-state"
						}
						metadata := gjson.GetBytes(wire.payload, "client_metadata.x-codex-turn-metadata").String()
						require.Equal(t, wantMarker, gjson.Get(metadata, "marker").String(), "frame metadata is fill-only: %s", wire.payload)
						require.Contains(t, metadata, `"opaque":"\u8bbe\u5907"`)
						require.Contains(t, metadata, `"marker" : "`+wantMarker+`"`, "unknown metadata formatting survives")
						require.Equal(t, wantState, gjson.GetBytes(wire.payload, "client_metadata.x-codex-turn-state").String())
					}
				})
			}
		}
	}
}

func TestCodexWSIntegrationMetadataProvenanceGuardsSubsequentEntrypoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{"v2", OpenAIWSIngressModeCtxPool, OpenAIWSIngressModePassthrough} {
		for _, enabled := range []bool{false, true} {
			for _, carrier := range []string{"header", "frame"} {
				for _, sameCredential := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/wire=%v/%s/same_credential=%v", mode, enabled, carrier, sameCredential), func(t *testing.T) {
						const blob = "metadata-only-origin"
						upstream := newCodexWSIntegrationUpstream(t, nil, [][]byte{
							codexWSIntegrationMetadata(blob), codexWSIntegrationCompleted("resp_origin"),
						})
						svc := newCodexWSIntegrationService(t, upstream, false)
						accountMode := mode
						if mode == "v2" {
							accountMode = OpenAIWSIngressModeCtxPool
						}
						minter := codexWSIntegrationAccount(21201, "credential-a", enabled, accountMode)
						if mode == "v2" {
							_, recorder, _ := forwardCodexWSIntegration(t, svc, minter, codexWSIntegrationHeaders(), []byte(codexWSIntegrationFrame))
							require.Contains(t, recorder.Body.String(), string(codexWSIntegrationMetadata(blob)))
						} else {
							events := runCodexWSIntegrationIngress(t, svc, minter, codexWSIntegrationHeaders(), []byte(codexWSIntegrationFrame))
							require.Equal(t, codexWSIntegrationMetadata(blob), events[0])
						}
						namespace := "credential-b"
						if sameCredential {
							namespace = "credential-a"
						}
						account := codexWSIntegrationAccount(21202, namespace, enabled, accountMode)
						headers := codexWSIntegrationHeaders()
						headers.Set("session-id", "another-downstream-session")
						frame := []byte(codexWSIntegrationFrame)
						if carrier == "header" {
							headers.Set(openAICodexTurnStateHeader, blob)
						} else {
							var err error
							frame, err = sjson.SetBytes(frame, "client_metadata.x-codex-turn-state", blob)
							require.NoError(t, err)
						}
						if mode == "v2" {
							forwardCodexWSIntegration(t, svc, account, headers, frame)
							forwardCodexWSIntegration(t, svc, account, headers, frame)
						} else {
							runCodexWSIntegrationIngress(t, svc, account, headers, frame, frame)
						}
						handshakes, sent := upstream.snapshot()
						require.Len(t, handshakes, 2)
						require.Len(t, sent, 3)
						wantHeader, wantFrame := "", ""
						if sameCredential {
							if carrier == "frame" || enabled {
								wantFrame = blob
							} else {
								wantHeader = blob
							}
						}
						require.Equal(t, wantHeader, handshakes[1].Get(openAICodexTurnStateHeader))
						for _, frame := range sent[1:] {
							require.Equal(t, wantFrame, gjson.GetBytes(frame.payload, "client_metadata.x-codex-turn-state").String())
							require.Equal(t, "a <b> & c", gjson.GetBytes(frame.payload, "client_metadata.keep").String())
						}
					})
				}
			}
		}
	}
}

func TestCodexWSIntegrationV2DoesNotRecordUndeliveredMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			const blob = "metadata-never-delivered"
			events := [][]byte{codexWSIntegrationMetadata(blob), codexWSIntegrationCompleted("resp_nonstream")}
			if stream {
				// This attempt is discarded while metadata is buffered before first output.
				events[1] = []byte(`{"type":"error","error":{"type":"server_error","code":"server_error","message":"retry before output"}}`)
			}
			upstream := newCodexWSIntegrationUpstream(t, nil, events)
			svc := newCodexWSIntegrationService(t, upstream, false)
			account := codexWSIntegrationAccount(21301, "credential-a", true, OpenAIWSIngressModeCtxPool)
			frame, err := sjson.SetBytes([]byte(codexWSIntegrationFrame), "stream", stream)
			require.NoError(t, err)
			_, recorder, _ := forwardCodexWSIntegration(t, svc, account, codexWSIntegrationHeaders(), frame)
			require.NotContains(t, recorder.Body.String(), blob)
			other := codexWSIntegrationAccount(21302, "credential-b", true, OpenAIWSIngressModeCtxPool)
			require.Equal(t, blob, svc.guardOpenAICodexTurnStateValue(nil, other, blob),
				"a discarded or non-stream metadata event must not create client-owned provenance")
		})
	}
}

func TestCodexWSIntegrationIngressDoesNotRecordFailedWriteOrDrainedMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := newCodexWSIntegrationUpstream(t, nil, [][]byte{
		codexWSIntegrationMetadata("failed-write-state"),
		codexWSIntegrationMetadata("drained-state"),
		codexWSIntegrationCompleted("resp_disconnected"),
	})
	svc := newCodexWSIntegrationService(t, upstream, false)
	account := codexWSIntegrationAccount(21401, "credential-a", true, OpenAIWSIngressModeCtxPool)
	turnDone := make(chan *OpenAIForwardResult, 1)
	client, serverErrors := startCodexWSIntegrationIngress(t, svc, account, codexWSIntegrationHeaders(),
		&OpenAIWSIngressHooks{
			AfterTurn: func(_ int, result *OpenAIForwardResult, _ error) {
				turnDone <- result
			},
		}, func(conn *coderws.Conn) { _ = conn.CloseNow() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(codexWSIntegrationFrame)))
	select {
	case result := <-turnDone:
		require.NotNil(t, result)
		require.Equal(t, "resp_disconnected", result.RequestID, "drain must reach the terminal event")
	case <-ctx.Done():
		t.Fatal("disconnected ingress did not drain the terminal event")
	}
	readCtx, cancelRead := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancelRead()
	_, _, err := client.Read(readCtx)
	require.Error(t, err, "closed downstream socket must not receive metadata")
	require.NoError(t, client.CloseNow())
	awaitCodexWSIntegrationIngress(t, serverErrors)
	_, sent := upstream.snapshot()
	require.Len(t, sent, 1, "the production ingress still forwarded the request")
	other := codexWSIntegrationAccount(21402, "credential-b", true, OpenAIWSIngressModeCtxPool)
	for _, blob := range []string{"failed-write-state", "drained-state"} {
		require.Equal(t, blob, svc.guardOpenAICodexTurnStateValue(nil, other, blob),
			"only successfully committed downstream events create provenance")
	}
}

func TestCodexWSIntegrationPassthroughFailedWriteDoesNotRecordMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const blob = "passthrough-failed-write"
	svc := &OpenAIGatewayService{}
	account := codexWSIntegrationAccount(21411, "credential-a", true, OpenAIWSIngressModePassthrough)
	writeErrors := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			writeErrors <- err
			return
		}
		_ = conn.CloseNow()
		frameConn := &openAIWSClientFrameConn{
			conn: conn,
			noteTurnState: func(payload []byte) {
				svc.noteOpenAICodexTurnStateFromWSEvent(nil, account, payload)
			},
		}
		writeErrors <- frameConn.WriteFrame(r.Context(), coderws.MessageText, codexWSIntegrationMetadata(blob))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	defer func() { _ = client.CloseNow() }()
	select {
	case err := <-writeErrors:
		require.Error(t, err, "closed native connection must reject the write")
	case <-ctx.Done():
		t.Fatal("failed passthrough write did not return")
	}
	other := codexWSIntegrationAccount(21412, "credential-b", true, OpenAIWSIngressModePassthrough)
	require.Equal(t, blob, svc.guardOpenAICodexTurnStateValue(nil, other, blob))
}

func codexWSIntegrationHTTPResponse(events ...[]byte) *http.Response {
	var body strings.Builder
	for _, event := range events {
		body.WriteString("data: ")
		body.Write(event)
		body.WriteString("\n\n")
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body.String())),
	}
}

func TestCodexWSIntegrationHTTPBridgeMetadataProvenance(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{OpenAIWSIngressModeCtxPool, OpenAIWSIngressModePassthrough} {
		for _, carrier := range []string{"header", "frame"} {
			for _, sameCredential := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/same_credential=%v", mode, carrier, sameCredential), func(t *testing.T) {
					const blob = "http-bridge-metadata"
					upstream := newCodexWSIntegrationUpstream(t, nil)
					svc := newCodexWSIntegrationService(t, upstream, false)
					svc.cfg.Gateway.OpenAIWS.HTTPBridgeEnabled = true
					svc.cfg.Gateway.OpenAIWS.HTTPBridgeThresholdBytes = 1
					httpUpstream := svc.httpUpstream.(*httpUpstreamRecorder)
					httpUpstream.responses = []*http.Response{
						codexWSIntegrationHTTPResponse(codexWSIntegrationMetadata(blob), codexWSIntegrationCompleted("resp_bridge_1")),
						codexWSIntegrationHTTPResponse(codexWSIntegrationCompleted("resp_bridge_2")),
					}
					minter := codexWSIntegrationAccount(21501, "credential-a", false, mode)
					events := runCodexWSIntegrationIngress(t, svc, minter, codexWSIntegrationHeaders(), []byte(codexWSIntegrationFrame))
					require.Equal(t, codexWSIntegrationMetadata(blob), events[0], "real WS ingress delivered HTTP SSE metadata")
					namespace := "credential-b"
					if sameCredential {
						namespace = "credential-a"
					}
					account := codexWSIntegrationAccount(21502, namespace, false, mode)
					headers := codexWSIntegrationHeaders()
					frame := []byte(codexWSIntegrationFrame)
					if carrier == "header" {
						headers.Set(openAICodexTurnStateHeader, blob)
					} else {
						var err error
						frame, err = sjson.SetBytes(frame, "client_metadata.x-codex-turn-state", blob)
						require.NoError(t, err)
					}
					runCodexWSIntegrationIngress(t, svc, account, headers, frame)
					handshakes, sent := upstream.snapshot()
					require.Empty(t, handshakes, "threshold=1 must prevent upstream WS dialing")
					require.Empty(t, sent)
					require.Len(t, httpUpstream.requests, 2, "both turns must use the HTTP bridge")
					want := ""
					if sameCredential {
						want = blob
					}
					if carrier == "header" {
						require.Equal(t, want, httpUpstream.requests[1].Header.Get(openAICodexTurnStateHeader))
					} else {
						require.Equal(t, want, gjson.GetBytes(httpUpstream.bodies[1], "client_metadata.x-codex-turn-state").String())
					}
					require.Equal(t, "a <b> & c", gjson.GetBytes(httpUpstream.bodies[1], "client_metadata.keep").String())
				})
			}
		}
	}
}

func TestCodexWSIntegrationHTTPBridgeFailedWriteDoesNotRecordMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := newCodexWSIntegrationUpstream(t, nil)
	svc := newCodexWSIntegrationService(t, upstream, false)
	svc.cfg.Gateway.OpenAIWS.HTTPBridgeEnabled = true
	svc.cfg.Gateway.OpenAIWS.HTTPBridgeThresholdBytes = 1
	httpUpstream := svc.httpUpstream.(*httpUpstreamRecorder)
	httpUpstream.resp = codexWSIntegrationHTTPResponse(
		codexWSIntegrationMetadata("bridge-failed-write"),
		codexWSIntegrationMetadata("bridge-uncommitted-buffer"),
		codexWSIntegrationCompleted("resp_bridge_disconnected"),
	)
	account := codexWSIntegrationAccount(21511, "credential-a", true, OpenAIWSIngressModePassthrough)
	turnDone := make(chan *OpenAIForwardResult, 1)
	client, serverErrors := startCodexWSIntegrationIngress(t, svc, account, codexWSIntegrationHeaders(), &OpenAIWSIngressHooks{
		AfterTurn: func(_ int, result *OpenAIForwardResult, _ error) {
			turnDone <- result
		},
	}, func(conn *coderws.Conn) { _ = conn.CloseNow() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(codexWSIntegrationFrame)))
	select {
	case result := <-turnDone:
		require.NotNil(t, result)
		require.Equal(t, "resp_bridge_disconnected", result.RequestID)
	case <-ctx.Done():
		t.Fatal("HTTP bridge did not drain after the failed downstream write")
	}
	readCtx, cancelRead := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancelRead()
	_, _, err := client.Read(readCtx)
	require.Error(t, err, "uncommitted HTTP SSE metadata must not reach the downstream socket")
	require.NoError(t, client.CloseNow())
	awaitCodexWSIntegrationIngress(t, serverErrors)
	require.Len(t, httpUpstream.requests, 1)
	handshakes, sent := upstream.snapshot()
	require.Empty(t, handshakes)
	require.Empty(t, sent)
	other := codexWSIntegrationAccount(21512, "credential-b", true, OpenAIWSIngressModeCtxPool)
	for _, blob := range []string{"bridge-failed-write", "bridge-uncommitted-buffer"} {
		require.Equal(t, blob, svc.guardOpenAICodexTurnStateValue(nil, other, blob))
	}
}

func TestCodexWSIntegrationHandshakeStateNeverEntersFreshFrame(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{"v2", OpenAIWSIngressModeCtxPool} {
		for _, enabled := range []bool{false, true} {
			for _, reconnect := range []string{"reuse", "same-row", "same-credential", "foreign-credential", "shadow-row"} {
				t.Run(fmt.Sprintf("%s/wire=%v/%s", mode, enabled, reconnect), func(t *testing.T) {
					const blob = "upstream-handshake-only"
					handshake := http.Header{}
					handshake.Set(openAICodexTurnStateHeader, blob)
					upstream := newCodexWSIntegrationUpstream(t, []http.Header{handshake})
					svc := newCodexWSIntegrationService(t, upstream, mode == "v2")
					minter := codexWSIntegrationAccount(21601, "credential-a", enabled, OpenAIWSIngressModeCtxPool)
					if reconnect == "shadow-row" {
						parent := minter
						minter = codexWSIntegrationAccount(21603, "", enabled, OpenAIWSIngressModeCtxPool)
						minter.ParentAccountID = &parent.ID
						minter.Credentials = map[string]any{"model_mapping": map[string]any{}}
						svc.accountRepo = &stubQuotaAccountRepo{accounts: map[int64]*Account{parent.ID: parent}}
					}
					headers := codexWSIntegrationHeaders()
					first := []byte(codexWSIntegrationFrame)
					if mode == "v2" {
						c, _, _ := forwardCodexWSIntegration(t, svc, minter, headers, first)
						require.Equal(t, blob, c.Writer.Header().Get(openAICodexTurnStateHeader))
					} else {
						runCodexWSIntegrationIngress(t, svc, minter, headers, first)
					}
					c, _ := codexWSIntegrationContext(headers, first)
					store := svc.getOpenAIWSStateStore()
					sessionHash := svc.GenerateSessionHash(c, first)
					saved, ok := store.GetSessionTurnState(0, sessionHash)
					require.True(t, ok, "must populate the exact session cache used by the next entrypoint")
					require.Equal(t, blob, saved)
					if reconnect != "reuse" {
						connID, ok := store.GetSessionConn(0, sessionHash)
						require.True(t, ok)
						svc.getOpenAIWSConnPool().evictConn(minter.ID, connID)
					}
					account := minter
					if reconnect == "same-credential" || reconnect == "foreign-credential" {
						namespace := "credential-a"
						if reconnect == "foreign-credential" {
							namespace = "credential-b"
						}
						account = codexWSIntegrationAccount(21602, namespace, enabled, OpenAIWSIngressModeCtxPool)
					}
					if mode == "v2" {
						forwardCodexWSIntegration(t, svc, account, headers, first)
					} else {
						runCodexWSIntegrationIngress(t, svc, account, headers, first)
					}
					handshakes, sent := upstream.snapshot()
					if reconnect == "reuse" {
						require.Len(t, handshakes, 1)
					} else {
						require.Len(t, handshakes, 2)
						want := ""
						if !enabled && reconnect != "foreign-credential" {
							want = blob
						}
						require.Equal(t, want, handshakes[1].Get(openAICodexTurnStateHeader),
							"cache fallback is guarded by credential and only legacy handshakes may replay it")
					}
					require.GreaterOrEqual(t, len(sent), 2)
					for _, frame := range sent {
						require.False(t, gjson.GetBytes(frame.payload, "client_metadata.x-codex-turn-state").Exists(),
							"neither a new handshake nor cached/reused handshake state belongs in a fresh frame")
					}
				})
			}
		}
	}
}

func TestCodexWSIntegrationShadowMetadataUsesParentCredential(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{"v2", OpenAIWSIngressModeCtxPool, OpenAIWSIngressModePassthrough} {
		for _, tc := range []struct {
			enabled bool
			carrier string
		}{{false, "header"}, {false, "frame"}, {true, "header"}, {true, "frame"}} {
			enabled := tc.enabled
			t.Run(fmt.Sprintf("%s/wire=%v/%s", mode, enabled, tc.carrier), func(t *testing.T) {
				const blob = "shadow-delivered-metadata"
				upstream := newCodexWSIntegrationUpstream(t, nil, [][]byte{
					codexWSIntegrationMetadata(blob), codexWSIntegrationCompleted("resp_shadow"),
				})
				svc := newCodexWSIntegrationService(t, upstream, false)
				accountMode := mode
				if mode == "v2" {
					accountMode = OpenAIWSIngressModeCtxPool
				}
				parent := codexWSIntegrationAccount(21701, "parent-credential", enabled, accountMode)
				shadow := codexWSIntegrationAccount(21702, "", enabled, accountMode)
				shadow.ParentAccountID = &parent.ID
				shadow.Credentials = map[string]any{"model_mapping": map[string]any{}}
				svc.accountRepo = &stubQuotaAccountRepo{accounts: map[int64]*Account{parent.ID: parent}}
				if mode == "v2" {
					_, recorder, _ := forwardCodexWSIntegration(t, svc, shadow, codexWSIntegrationHeaders(), []byte(codexWSIntegrationFrame))
					require.Contains(t, recorder.Body.String(), blob)
				} else {
					events := runCodexWSIntegrationIngress(t, svc, shadow, codexWSIntegrationHeaders(), []byte(codexWSIntegrationFrame))
					require.Equal(t, codexWSIntegrationMetadata(blob), events[0])
				}
				require.Equal(t, blob, svc.guardOpenAICodexTurnStateValue(nil, parent, blob), "the minting owner is the parent credential")
				other := codexWSIntegrationAccount(21703, "other-credential", enabled, accountMode)
				require.Empty(t, svc.guardOpenAICodexTurnStateValue(nil, other, blob))
				headers := codexWSIntegrationHeaders()
				if mode != OpenAIWSIngressModePassthrough {
					c, _ := codexWSIntegrationContext(headers, nil)
					connID, ok := svc.getOpenAIWSStateStore().GetSessionConn(0, svc.GenerateSessionHash(c, nil))
					require.True(t, ok)
					svc.getOpenAIWSConnPool().evictConn(shadow.ID, connID)
				}
				frame := []byte(codexWSIntegrationFrame)
				if tc.carrier == "header" {
					headers.Set(openAICodexTurnStateHeader, blob)
				} else {
					var err error
					frame, err = sjson.SetBytes(frame, "client_metadata.x-codex-turn-state", blob)
					require.NoError(t, err)
				}
				if mode == "v2" {
					forwardCodexWSIntegration(t, svc, shadow, headers, frame)
				} else {
					runCodexWSIntegrationIngress(t, svc, shadow, headers, frame, frame)
				}
				handshakes, sent := upstream.snapshot()
				require.Len(t, handshakes, 2, "reconnect makes the shadow header guard observable")
				wantHeader, wantFrame := "", blob
				if !enabled && tc.carrier == "header" {
					wantHeader, wantFrame = blob, ""
				}
				require.Equal(t, wantHeader, handshakes[1].Get(openAICodexTurnStateHeader))
				for _, wire := range sent[1:] {
					require.Equal(t, wantFrame, gjson.GetBytes(wire.payload, "client_metadata.x-codex-turn-state").String(),
						"frame guard must use the staged parent, not the shadow row")
				}
			})
		}
	}
}

func TestCodexWSIntegrationPassthroughNeverEchoesHandshakeState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("wire=%v", enabled), func(t *testing.T) {
			handshake := http.Header{}
			handshake.Set(openAICodexTurnStateHeader, "passthrough-handshake-only")
			upstream := newCodexWSIntegrationUpstream(t, []http.Header{handshake})
			svc := newCodexWSIntegrationService(t, upstream, false)
			account := codexWSIntegrationAccount(21801, "credential-a", enabled, OpenAIWSIngressModePassthrough)
			frame := []byte(codexWSIntegrationFrame)
			runCodexWSIntegrationIngress(t, svc, account, codexWSIntegrationHeaders(), frame, frame)
			runCodexWSIntegrationIngress(t, svc, account, codexWSIntegrationHeaders(), frame)
			handshakes, sent := upstream.snapshot()
			require.Len(t, handshakes, 2)
			require.Len(t, sent, 3)
			for _, header := range handshakes {
				require.Empty(t, header.Get(openAICodexTurnStateHeader))
			}
			for _, frame := range sent {
				require.False(t, gjson.GetBytes(frame.payload, "client_metadata.x-codex-turn-state").Exists())
			}
		})
	}
}

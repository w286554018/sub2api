package service

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func auxiliaryDecodeRequest(t *testing.T, req *http.Request, wire []byte) []byte {
	t.Helper()
	if req.Header.Get("Content-Encoding") == "" {
		return wire
	}
	require.Equal(t, "zstd", req.Header.Get("Content-Encoding"))
	require.True(t, bytes.HasPrefix(wire, []byte{0x28, 0xb5, 0x2f, 0xfd, 0x00, 0x58}))
	decoder, err := zstd.NewReader(nil)
	require.NoError(t, err)
	defer decoder.Close()
	body, err := decoder.DecodeAll(wire, nil)
	require.NoError(t, err)
	return body
}

func auxiliaryDirectImageResponse() *http.Response {
	return openAIImagesJSONResponse()
}

func auxiliaryResponsesImageResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1},\"output\":[{\"type\":\"image_generation_call\",\"result\":\"aW1hZ2U=\",\"output_format\":\"png\"}]}}\n\n")),
	}
}

func TestImagesAuxiliaryDefaultAndExplicitModels(t *testing.T) {
	for _, endpoint := range []string{"/v1/images/generations", "/v1/images/edits"} {
		for _, model := range []string{"", "   ", "gpt-image-2", "gpt-image-2.5-sunburst", "gpt-image-2.5-flare"} {
			t.Run(endpoint+"/"+model, func(t *testing.T) {
				var body []byte
				contentType := "application/json"
				if strings.HasSuffix(endpoint, "/edits") {
					var buf bytes.Buffer
					writer := multipart.NewWriter(&buf)
					require.NoError(t, writer.WriteField("prompt", "draw a square"))
					if model != "" {
						require.NoError(t, writer.WriteField("model", model))
					}
					part, err := writer.CreateFormFile("image", "input.png")
					require.NoError(t, err)
					_, err = part.Write([]byte("fixture"))
					require.NoError(t, err)
					require.NoError(t, writer.Close())
					body, contentType = buf.Bytes(), writer.FormDataContentType()
				} else {
					body = []byte(`{"prompt":"draw a square"}`)
					if model != "" {
						body = []byte(`{"prompt":"draw a square","model":"` + model + `"}`)
					}
				}
				c := codexIdentityHTTPContext(endpoint, nil)
				c.Request = httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
				c.Request.Header.Set("Content-Type", contentType)
				parsed, err := (&OpenAIGatewayService{}).ParseOpenAIImagesRequest(c, body)
				require.NoError(t, err)
				want := strings.TrimSpace(model)
				if want == "" {
					want = "gpt-image-2.5-sunburst"
					require.Equal(t, OpenAIImagesCapabilityBasic, parsed.RequiredCapability)
				}
				require.Equal(t, want, parsed.Model)
			})
		}
	}
}

func TestImagesAuxiliaryDeviceWireAndOptOut(t *testing.T) {
	for _, mode := range []string{"off", "device", "session", "full"} {
		for _, enabled := range []bool{false, true} {
			name := mode + "/disabled"
			if enabled {
				name = mode + "/enabled"
			}
			t.Run(name, func(t *testing.T) {
				body := []byte(`{"model":"gpt-image-2","prompt":"draw a square"}`)
				headers := http.Header{}
				headers.Set("Content-Type", "application/json")
				headers.Set("Session-Id", "image-session")
				headers.Set("OpenAI-Beta", "responses=experimental")
				headers.Set(openAIWSTurnMetadataHeader, `{"session_id":"image-session","installation_id":"client-device","turn_id":"image-turn"}`)
				c := codexIdentityHTTPContext("/v1/images/generations", headers)
				originalHeaders := c.Request.Header.Clone()
				account := codexIdentityAccount()
				account.Extra[codexFingerprintModeExtraKey] = mode
				account.Extra[codexFingerprintConvergenceExtraKey] = enabled
				upstream := &httpUpstreamRecorder{resp: auxiliaryDirectImageResponse()}
				svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
				parsed, err := svc.ParseOpenAIImagesRequest(c, body)
				require.NoError(t, err)
				result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")
				require.NoError(t, err)
				require.NotNil(t, result)
				wire := auxiliaryDecodeRequest(t, upstream.lastReq, upstream.lastBody)
				require.Equal(t, "gpt-image-2", gjson.GetBytes(wire, "model").String())
				require.Equal(t, originalHeaders, c.Request.Header)
				installation := gjson.GetBytes(wire, "client_metadata.x-codex-installation-id")
				if mode == "device" && enabled {
					require.Equal(t, "zstd", upstream.lastReq.Header.Get("Content-Encoding"))
					require.NotEmpty(t, installation.String())
					require.NotEqual(t, "client-device", installation.String())
					require.Equal(t, installation.String(), gjson.Get(upstream.lastReq.Header.Get(openAIWSTurnMetadataHeader), "installation_id").String())
					require.Empty(t, upstream.lastReq.Header.Get("x-codex-installation-id"))
					require.Empty(t, upstream.lastReq.Header.Get("OpenAI-Beta"))
				} else {
					require.Empty(t, upstream.lastReq.Header.Get("Content-Encoding"))
					require.False(t, installation.Exists())
					require.Empty(t, upstream.lastReq.Header.Get("OpenAI-Beta"))
				}
			})
		}
	}
}

func TestImagesAuxiliaryOAuthFallbackKeepsControllerAndMapping(t *testing.T) {
	t.Setenv("SUB2API_IMAGES_MAIN_MODEL", " gpt-6-astra ")
	for _, mapped := range []string{"", "gpt-image-2"} {
		t.Run(mapped, func(t *testing.T) {
			c := codexIdentityHTTPContext("/v1/images/generations", nil)
			upstream := &httpUpstreamRecorder{resp: auxiliaryResponsesImageResponse()}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			parsed := &OpenAIImagesRequest{Endpoint: "/v1/images/generations", Model: " ", Prompt: "draw a square", N: 1}
			_, err := svc.forwardOpenAIImagesOAuth(withOpenAIImagesForceResponses(context.Background()), c, codexIdentityAccount(), parsed, mapped)
			require.NoError(t, err)
			wire := auxiliaryDecodeRequest(t, upstream.lastReq, upstream.lastBody)
			require.Equal(t, "gpt-6-astra", gjson.GetBytes(wire, "model").String())
			want := mapped
			if want == "" {
				want = "gpt-image-2.5-sunburst"
			}
			require.Equal(t, want, gjson.GetBytes(wire, "tools.0.model").String())
		})
	}
}

func TestImagesAuxiliaryCredentialSourceAndFailover(t *testing.T) {
	for _, sourceEnabled := range []bool{false, true} {
		name := "source-disabled"
		if sourceEnabled {
			name = "source-enabled"
		}
		t.Run(name, func(t *testing.T) {
			parent := codexIdentityAccount()
			parent.Extra[codexFingerprintConvergenceExtraKey] = sourceEnabled
			child := codexIdentityAccount()
			child.ID++
			child.ParentAccountID = &parent.ID
			child.Credentials = map[string]any{}
			child.Extra[codexFingerprintConvergenceExtraKey] = !sourceEnabled
			other := codexIdentityAccount()
			other.ID += 2
			other.Credentials["chatgpt_account_id"] = "next-upstream"
			other.Extra[codexFingerprintModeExtraKey] = "off"
			upstream := &httpUpstreamRecorder{responses: []*http.Response{auxiliaryDirectImageResponse(), auxiliaryDirectImageResponse()}}
			svc := &OpenAIGatewayService{
				cfg: &config.Config{}, httpUpstream: upstream,
				accountRepo: &stubQuotaAccountRepo{accounts: map[int64]*Account{parent.ID: parent}},
			}
			body := []byte(`{"model":"gpt-image-2","prompt":"draw a square"}`)
			c := codexIdentityHTTPContext("/v1/images/generations", http.Header{"Content-Type": {"application/json"}})
			parsed, err := svc.ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)
			_, err = svc.ForwardImages(context.Background(), c, child, body, parsed, "")
			require.NoError(t, err)
			wire := auxiliaryDecodeRequest(t, upstream.lastReq, upstream.lastBody)
			require.Equal(t, sourceEnabled, gjson.GetBytes(wire, "client_metadata.x-codex-installation-id").Exists())
			if sourceEnabled {
				require.Equal(t, "zstd", upstream.lastReq.Header.Get("Content-Encoding"))
				require.Empty(t, upstream.lastReq.Header.Get("OpenAI-Beta"))
			} else {
				require.Empty(t, upstream.lastReq.Header.Get("Content-Encoding"))
				require.Empty(t, upstream.lastReq.Header.Get("OpenAI-Beta"))
			}
			_, err = svc.ForwardImages(context.Background(), c, other, body, parsed, "")
			require.NoError(t, err)
			require.Empty(t, upstream.lastReq.Header.Get("Content-Encoding"))
			require.Empty(t, upstream.lastReq.Header.Get("x-codex-installation-id"))
			require.False(t, gjson.GetBytes(upstream.lastBody, "client_metadata").Exists())
			require.Equal(t, "next-upstream", upstream.lastReq.Header.Get("ChatGPT-Account-ID"))
		})
	}
}

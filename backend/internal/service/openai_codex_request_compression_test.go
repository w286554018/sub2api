package service

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
)

func TestCompressCodexRequestBodyRoundTripAndScope(t *testing.T) {
	account := codexIdentityAccount()
	c := codexIdentityHTTPContext("/v1/responses", nil)
	for _, size := range []int{100, 5000, 70000, 300000, 3 << 20} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			body := []byte(`{"model":"gpt-5.4","input":"` + strings.Repeat("abc ", size/4) + `"}`)
			wire, encoding, err := compressCodexRequestBody(c, account, "https://example.test/responses", body)
			require.NoError(t, err)
			require.Equal(t, "zstd", encoding)
			require.Greater(t, len(wire), 6)
			require.Equal(t, []byte{0x28, 0xb5, 0x2f, 0xfd, 0, 0x58}, wire[:6])
			decoder, err := zstd.NewReader(nil)
			require.NoError(t, err)
			plain, err := decoder.DecodeAll(wire, nil)
			decoder.Close()
			require.NoError(t, err)
			require.Equal(t, body, plain)
			again, _, err := compressCodexRequestBody(c, account, "https://example.test/responses", body)
			require.NoError(t, err)
			require.Equal(t, wire, again)
		})
	}
	body := []byte(`{"input":"unchanged"}`)
	for _, mode := range []string{"off", "device", "session", "full"} {
		for _, enabled := range []bool{false, true} {
			account.Extra[codexFingerprintModeExtraKey] = mode
			account.Extra[codexFingerprintConvergenceExtraKey] = enabled
			for _, target := range []string{
				"https://example.test/responses", "https://example.test/responses/?query=1",
				"https://example.test/responses/compact", "https://example.test/models",
				"https://example.test/alpha/search", "https://example.test/images/generations", "://invalid",
			} {
				wire, encoding, err := compressCodexRequestBody(c, account, target, body)
				require.NoError(t, err)
				want := mode == "device" && enabled && (strings.HasSuffix(target, "/responses") || strings.Contains(target, "/responses/?"))
				if want {
					require.Equal(t, "zstd", encoding)
				} else {
					require.Empty(t, encoding)
					require.Equal(t, body, wire)
				}
			}
		}
	}
}

func TestCompressCodexRequestBodyEmptyAndUnsupportedStayPlain(t *testing.T) {
	account := codexIdentityAccount()
	for _, body := range [][]byte{nil, {}} {
		wire, encoding, err := compressCodexRequestBody(nil, account, "https://example.test/responses", body)
		require.NoError(t, err)
		require.Empty(t, encoding)
		require.Equal(t, body, wire)
	}
	body := []byte(`{"input":"hi"}`)
	for _, candidate := range []*Account{nil, {Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: account.Extra}, {Platform: PlatformGrok, Type: AccountTypeOAuth, Extra: account.Extra}} {
		wire, encoding, err := compressCodexRequestBody(nil, candidate, "https://example.test/responses", body)
		require.NoError(t, err)
		require.Empty(t, encoding)
		require.Equal(t, body, wire)
	}
}

func TestNormalizeCodexZstdFrameHeaderRejectsFlagBits(t *testing.T) {
	for _, flag := range []byte{1, 2, 4, 8, 16} {
		frame := []byte{0x28, 0xb5, 0x2f, 0xfd, flag, 0x58, 1, 0, 0}
		_, err := normalizeCodexZstdFrameHeader(frame)
		require.Error(t, err, "descriptor flag %x", flag)
	}
	for _, descriptor := range []byte{0, 0x20, 0x40, 0x60, 0x80, 0xa0, 0xc0, 0xe0} {
		length := 1
		if descriptor&0x20 == 0 {
			length++
		}
		switch descriptor >> 6 {
		case 0:
			if descriptor&0x20 != 0 {
				length++
			}
		case 1:
			length += 2
		case 2:
			length += 4
		case 3:
			length += 8
		}
		header := append([]byte{0x28, 0xb5, 0x2f, 0xfd, descriptor}, bytes.Repeat([]byte{0}, length-1)...)
		for end := 0; end < len(header); end++ {
			_, err := normalizeCodexZstdFrameHeader(header[:end])
			require.Error(t, err, "descriptor=%x truncated at %d", descriptor, end)
		}
	}
	_, err := normalizeCodexZstdFrameHeader([]byte("not a zstd frame"))
	require.Error(t, err)
}

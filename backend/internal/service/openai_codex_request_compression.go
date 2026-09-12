package service

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"
)

const codexRequestZstdContentEncoding = "zstd"

var codexRequestZstdEncoders = sync.Pool{New: func() any {
	encoder, err := zstd.NewWriter(nil,
		zstd.WithEncoderLevel(zstd.SpeedDefault),
		zstd.WithEncoderCRC(false),
		zstd.WithWindowSize(1<<21),
		zstd.WithSingleSegment(false),
		zstd.WithEncoderConcurrency(1),
		zstd.WithLowerEncoderMem(true),
	)
	if err != nil {
		panic(fmt.Sprintf("create codex zstd encoder: %v", err))
	}
	return encoder
}}

func codexRequestBodyCompressionEnabled(c *gin.Context, account *Account, targetURL string) bool {
	if !codexDeviceWireProfileEnabled(c, account) {
		return false
	}
	parsed, err := url.Parse(targetURL)
	if err != nil {
		return false
	}
	path := strings.TrimRight(parsed.Path, "/")
	return strings.HasSuffix(path, "/responses")
}

func compressCodexRequestBody(c *gin.Context, account *Account, targetURL string, body []byte) ([]byte, string, error) {
	if len(body) == 0 || !codexRequestBodyCompressionEnabled(c, account, targetURL) {
		return body, "", nil
	}
	encoder, ok := codexRequestZstdEncoders.Get().(*zstd.Encoder)
	if !ok {
		return nil, "", errors.New("codex zstd encoder pool returned unexpected type")
	}
	defer codexRequestZstdEncoders.Put(encoder)
	wire := encoder.EncodeAll(body, make([]byte, 0, len(body)/3+64))
	wire, err := normalizeCodexZstdFrameHeader(wire)
	if err != nil {
		return nil, "", fmt.Errorf("normalize codex zstd frame: %w", err)
	}
	return wire, codexRequestZstdContentEncoding, nil
}

var codexZstdMagic = []byte{0x28, 0xb5, 0x2f, 0xfd}

// normalizeCodexZstdFrameHeader matches libzstd's streaming shape:
// no pledged content size, no checksum, and a 2 MiB window.
func normalizeCodexZstdFrameHeader(frame []byte) ([]byte, error) {
	if len(frame) < 5 || !bytes.Equal(frame[:4], codexZstdMagic) {
		return nil, errors.New("unexpected zstd frame magic")
	}
	fhd := frame[4]
	if fhd&0x1f != 0 {
		return nil, errors.New("unexpected zstd frame descriptor flags")
	}
	single := fhd&(1<<5) != 0
	headerLen := 1
	if !single {
		headerLen++
	}
	switch fhd >> 6 {
	case 0:
		if single {
			headerLen++
		}
	case 1:
		headerLen += 2
	case 2:
		headerLen += 4
	default:
		headerLen += 8
	}
	if len(frame) < 4+headerLen {
		return nil, errors.New("short zstd frame header")
	}
	out := make([]byte, 0, len(frame)-headerLen+6)
	out = append(out, codexZstdMagic...)
	out = append(out, 0x00, 0x58)
	return append(out, frame[4+headerLen:]...), nil
}

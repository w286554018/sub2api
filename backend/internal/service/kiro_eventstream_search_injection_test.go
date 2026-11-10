package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	kiropkg "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/stretchr/testify/require"
)

func kiroSearchEventStreamFrame(t *testing.T, eventType string, payload any) []byte {
	t.Helper()
	payloadBytes, err := json.Marshal(payload)
	require.NoError(t, err)
	var headers bytes.Buffer
	require.NoError(t, headers.WriteByte(byte(len(":event-type"))))
	_, _ = headers.WriteString(":event-type")
	require.NoError(t, headers.WriteByte(7))
	require.NoError(t, binary.Write(&headers, binary.BigEndian, uint16(len(eventType))))
	_, _ = headers.WriteString(eventType)

	var frame bytes.Buffer
	require.NoError(t, binary.Write(&frame, binary.BigEndian, uint32(16+headers.Len()+len(payloadBytes))))
	require.NoError(t, binary.Write(&frame, binary.BigEndian, uint32(headers.Len())))
	require.NoError(t, binary.Write(&frame, binary.BigEndian, uint32(0)))
	_, _ = frame.Write(headers.Bytes())
	_, _ = frame.Write(payloadBytes)
	require.NoError(t, binary.Write(&frame, binary.BigEndian, uint32(0)))
	return frame.Bytes()
}

func kiroSearchCreditFixture(t *testing.T) []byte {
	t.Helper()
	var stream bytes.Buffer
	_, _ = stream.Write(kiroSearchEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "short visible answer"},
	}))
	_, _ = stream.Write(kiroSearchEventStreamFrame(t, "meteringEvent", map[string]any{
		"meteringEvent": map[string]any{"unit": "credit", "usage": 1.04842},
	}))
	_, _ = stream.Write(kiroSearchEventStreamFrame(t, "contextUsageEvent", map[string]any{
		"contextUsageEvent": map[string]any{"contextUsagePercentage": 10.0},
	}))
	return stream.Bytes()
}

func TestStreamKiroEventStreamWithSearchInjection_PreservesConverterResult(t *testing.T) {
	t.Setenv("SUB2API_KIRO_CREDITS_OUTPUT", "on")
	fixture := kiroSearchCreditFixture(t)
	requestCtx := kiropkg.KiroRequestContext{
		UpstreamModelID: "claude-opus-5.5",
		PromptCache: kiropkg.KiroPromptCacheHint{
			Resolved: true, CachedTokens: 90000,
		},
	}
	expected, err := kiropkg.StreamEventStreamAsAnthropicWithContext(
		context.Background(), bytes.NewReader(fixture), io.Discard,
		"claude-opus-5-5", 50, requestCtx,
	)
	require.NoError(t, err)
	require.NotNil(t, expected)
	require.Equal(t, kiropkg.KiroOutputMethodCredits, expected.OutputEstimate.Method)
	require.InDelta(t, 3000, expected.Usage.OutputTokens, 1)

	rounds := []kiropkg.SearchRound{
		{
			Query: "first query", ToolUseID: "toolu_first",
			Results: []kiropkg.SearchResultItem{
				{Title: "First result", URL: "https://example.com/first", Snippet: "First snippet"},
			},
		},
		{Query: "failed query", Outcome: kiropkg.SearchOutcomeError},
		{
			Query: "second query", ToolUseID: "toolu_second",
			Results: []kiropkg.SearchResultItem{
				{Title: "Second result", URL: "https://example.com/second", Snippet: "Second snippet"},
			},
		},
	}
	var output bytes.Buffer
	result, err := (&GatewayService{}).streamKiroEventStreamWithSearchInjection(
		context.Background(), io.NopCloser(bytes.NewReader(fixture)), &output,
		"claude-opus-5-5", 50, requestCtx, rounds,
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, expected.Usage, result.Usage)
	require.Equal(t, expected.StopReason, result.StopReason)
	require.Equal(t, expected.OutputEstimate, result.OutputEstimate)
	require.NotNil(t, result.FirstDeltaDur)
	require.NotNil(t, expected.FirstDeltaDur)
	require.Contains(t, output.String(), "first query")
	require.Contains(t, output.String(), "second query")
	require.NotContains(t, output.String(), "failed query")
	require.Contains(t, output.String(), "event: message_stop")

	scanner := kiropkg.NewSSEScanner(bytes.NewReader(output.Bytes()))
	textStart, textDelta, textStop := false, false, false
	var credits float64
	for scanner.Scan() {
		var event map[string]any
		require.NoError(t, json.Unmarshal(scanner.Data(), &event))
		switch scanner.Event() {
		case "content_block_start":
			block, ok := event["content_block"].(map[string]any)
			require.True(t, ok)
			if block["type"] == "text" {
				require.Equal(t, float64(2), event["index"])
				textStart = true
			}
		case "content_block_delta":
			delta, ok := event["delta"].(map[string]any)
			require.True(t, ok)
			if delta["type"] == "text_delta" {
				require.Equal(t, float64(2), event["index"])
				textDelta = true
			}
		case "content_block_stop":
			if event["index"] == float64(2) {
				textStop = true
			}
		case "message_delta":
			usage, ok := event["usage"].(map[string]any)
			require.True(t, ok)
			require.Equal(t, float64(result.Usage.OutputTokens), usage["output_tokens"])
			credits, ok = usage["_sub2api_kiro_credits"].(float64)
			require.True(t, ok)
		}
	}
	require.NoError(t, scanner.Err())
	require.True(t, textStart)
	require.True(t, textDelta)
	require.True(t, textStop)
	require.InDelta(t, result.Usage.KiroCredits, credits, 0.000001)
}

type kiroSearchFailWriter struct{ err error }

func (w kiroSearchFailWriter) Write([]byte) (int, error) { return 0, w.err }

func TestStreamKiroEventStreamWithSearchInjection_WriterFailureDoesNotBlockConverter(t *testing.T) {
	writeErr := errors.New("downstream unavailable")
	fixture := kiroSearchCreditFixture(t)
	done := make(chan error, 1)
	go func() {
		_, err := (&GatewayService{}).streamKiroEventStreamWithSearchInjection(
			context.Background(), io.NopCloser(bytes.NewReader(fixture)), kiroSearchFailWriter{err: writeErr},
			"claude-opus-5-5", 50, kiropkg.KiroRequestContext{}, nil,
		)
		done <- err
	}()
	select {
	case err := <-done:
		require.ErrorIs(t, err, writeErr)
	case <-time.After(2 * time.Second):
		t.Fatal("converter remained blocked after downstream write failure")
	}
}

type kiroSearchReadFailure struct{ err error }

func (r kiroSearchReadFailure) Read([]byte) (int, error) { return 0, r.err }
func (r kiroSearchReadFailure) Close() error             { return nil }

func TestStreamKiroEventStreamWithSearchInjection_PropagatesConverterError(t *testing.T) {
	readErr := errors.New("upstream interrupted")
	result, err := (&GatewayService{}).streamKiroEventStreamWithSearchInjection(
		context.Background(), kiroSearchReadFailure{err: readErr}, io.Discard,
		"claude-opus-5-5", 50, kiropkg.KiroRequestContext{}, nil,
	)
	require.ErrorIs(t, err, readErr)
	require.Nil(t, result)
}

type kiroSearchStartedReader struct {
	io.ReadCloser
	started chan struct{}
}

func (r kiroSearchStartedReader) Read(p []byte) (int, error) {
	select {
	case r.started <- struct{}{}:
	default:
	}
	return r.ReadCloser.Read(p)
}

func TestStreamKiroEventStreamWithSearchInjection_CancelUnblocksUpstreamRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pr, pw := io.Pipe()
	defer func() { _ = pr.Close() }()
	defer func() { _ = pw.Close() }()
	started := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		_, err := (&GatewayService{}).streamKiroEventStreamWithSearchInjection(
			ctx, kiroSearchStartedReader{ReadCloser: pr, started: started}, io.Discard, "claude-opus-5-5", 50,
			kiropkg.KiroRequestContext{}, nil,
		)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("converter did not start reading upstream")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("converter remained blocked on canceled upstream read")
	}
}

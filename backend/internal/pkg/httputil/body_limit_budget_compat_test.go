package httputil

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"testing"
)

const limitBudgetPriorOwnerBytes int64 = 100

func newLimitBudgetContext(t *testing.T, limit int64) (context.Context, *BodyMemoryBudget) {
	t.Helper()
	budget := NewBodyMemoryBudget(limit)
	ctx, release := WithRequestBodyMemoryBudget(context.Background(), budget)
	t.Cleanup(func() {
		release()
		if used := requestBodyBudgetUsed(budget); used != 0 {
			t.Errorf("request cleanup retained %d bytes", used)
		}
	})
	if err := ReserveRequestBodyMemory(ctx, limitBudgetPriorOwnerBytes); err != nil {
		t.Fatalf("reserve previous owner's buffer: %v", err)
	}
	return ctx, budget
}

func gzipLimitBudgetPayload(t *testing.T, payload []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(payload); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return compressed.Bytes()
}

func TestReadRequestBodyWithPreallocLimitRetainsOnlyDecodedAllocationAtExactCap(t *testing.T) {
	payload := bytes.Repeat([]byte{'x'}, 4096)
	ctx, budget := newLimitBudgetContext(t, 16<<20)
	req := newRequestWithBody(t, gzipLimitBudgetPayload(t, payload), "gzip").WithContext(ctx)

	got, err := ReadRequestBodyWithPreallocLimit(req, int64(len(payload)))
	if err != nil {
		t.Fatalf("read at exact decoded cap: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("decoded body changed: got %d bytes, want %d", len(got), len(payload))
	}
	if used, want := requestBodyBudgetUsed(budget), limitBudgetPriorOwnerBytes+int64(cap(got)); used != want {
		t.Fatalf("retained %d bytes, want previous owner plus decoded allocation %d", used, want)
	}
}

func TestReadRequestBodyWithPreallocLimitRefundsOwnedBuffersAboveDecodedCap(t *testing.T) {
	payload := bytes.Repeat([]byte{'x'}, 4096)
	ctx, budget := newLimitBudgetContext(t, 16<<20)
	req := newRequestWithBody(t, gzipLimitBudgetPayload(t, payload), "gzip").WithContext(ctx)
	const decodedCap int64 = 4095

	got, err := ReadRequestBodyWithPreallocLimit(req, decodedCap)
	var maxErr *http.MaxBytesError
	if got != nil || !errors.As(err, &maxErr) || maxErr.Limit != decodedCap {
		t.Fatalf("want nil body and MaxBytesError with limit %d, got len=%d err=%v", decodedCap, len(got), err)
	}
	if used := requestBodyBudgetUsed(budget); used != limitBudgetPriorOwnerBytes {
		t.Fatalf("failed decode retained temporary storage or refunded previous owner: %d", used)
	}
}

func TestReadRequestBodyWithPreallocLimitRefundsOwnedBuffersWhenBudgetExhausted(t *testing.T) {
	payload := bytes.Repeat([]byte{'x'}, 4096)
	ctx, budget := newLimitBudgetContext(t, 2048)
	req := newRequestWithBody(t, gzipLimitBudgetPayload(t, payload), "gzip").WithContext(ctx)

	got, err := ReadRequestBodyWithPreallocLimit(req, 8192)
	if got != nil || !errors.Is(err, ErrRequestBodyMemoryBudget) {
		t.Fatalf("want nil body and memory budget error, got len=%d err=%v", len(got), err)
	}
	if used := requestBodyBudgetUsed(budget); used != limitBudgetPriorOwnerBytes {
		t.Fatalf("failed decode retained temporary storage or refunded previous owner: %d", used)
	}
}

func TestReadRequestBodyWithPreallocLimitPreservesBorrowedOwnerOnRejection(t *testing.T) {
	payload := []byte(`{"input":"borrowed body"}`)
	ctx, budget := newLimitBudgetContext(t, 4096)
	req := newRequestWithBody(t, payload, "").WithContext(ctx)
	original, err := ReadRequestBodyWithPrealloc(req)
	if err != nil {
		t.Fatalf("read original body: %v", err)
	}
	wantUsed := limitBudgetPriorOwnerBytes + int64(cap(original))
	if used := requestBodyBudgetUsed(budget); used != wantUsed {
		t.Fatalf("original allocation charged %d bytes, want %d", used, wantUsed)
	}
	req.Body = NewPrereadBody(original)
	decodedCap := int64(len(original) - 1)

	got, err := ReadRequestBodyWithPreallocLimit(req, decodedCap)
	var maxErr *http.MaxBytesError
	if got != nil || !errors.As(err, &maxErr) || maxErr.Limit != decodedCap {
		t.Fatalf("want nil body and MaxBytesError with limit %d, got len=%d err=%v", decodedCap, len(got), err)
	}
	if used := requestBodyBudgetUsed(budget); used != wantUsed {
		t.Fatalf("rejected preread refunded its original owner: used=%d want=%d", used, wantUsed)
	}
}

func TestReadRequestBodyWithPreallocLimitReusesBorrowedBufferAfterRejection(t *testing.T) {
	payload := []byte(`{"input":"borrowed body"}`)
	ctx, budget := newLimitBudgetContext(t, 4096)
	req := newRequestWithBody(t, payload, "").WithContext(ctx)
	original, err := ReadRequestBodyWithPrealloc(req)
	if err != nil {
		t.Fatalf("read original body: %v", err)
	}
	req.Body = NewPrereadBody(original)
	if _, err := ReadRequestBodyWithPreallocLimit(req, int64(len(original)-1)); err == nil {
		t.Fatal("setup: preread body above decoded cap was accepted")
	}

	got, err := ReadRequestBodyWithPreallocLimit(req, int64(len(original)))
	if err != nil {
		t.Fatalf("read borrowed body at exact decoded cap: %v", err)
	}
	if !bytes.Equal(got, payload) || &got[0] != &original[0] {
		t.Fatal("preread did not return the original buffer unchanged")
	}
	if used, want := requestBodyBudgetUsed(budget), limitBudgetPriorOwnerBytes+int64(cap(original)); used != want {
		t.Fatalf("preread changed its original allocation charge: used=%d want=%d", used, want)
	}
}

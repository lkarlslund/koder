package store

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestCursorRoundTrip(t *testing.T) {
	t.Parallel()
	binding := testCursorBinding()
	want := CursorPosition{SortValue: "2026-08-22T15:04:05.123456789Z", ObjectID: "01a01688-fc5d-7f7d-8bb8-de244977f8a1"}

	cursor, err := EncodeCursor(binding, want)
	if err != nil {
		t.Fatalf("EncodeCursor() error = %v", err)
	}
	if strings.ContainsAny(cursor, "+/=") {
		t.Fatalf("EncodeCursor() = %q, want unpadded URL-safe value", cursor)
	}
	got, err := DecodeCursor(cursor, binding)
	if err != nil {
		t.Fatalf("DecodeCursor() error = %v", err)
	}
	if got != want {
		t.Fatalf("DecodeCursor() = %#v, want %#v", got, want)
	}
}

func TestCursorAllowsEmptyCanonicalSortValue(t *testing.T) {
	t.Parallel()
	binding := testCursorBinding()
	want := CursorPosition{ObjectID: "01a01688-fc5d-7f7d-8bb8-de244977f8a1"}
	cursor, err := EncodeCursor(binding, want)
	if err != nil {
		t.Fatalf("EncodeCursor() error = %v", err)
	}
	got, err := DecodeCursor(cursor, binding)
	if err != nil {
		t.Fatalf("DecodeCursor() error = %v", err)
	}
	if got != want {
		t.Fatalf("DecodeCursor() = %#v, want %#v", got, want)
	}
}

func TestCursorRejectsDifferentBinding(t *testing.T) {
	t.Parallel()
	binding := testCursorBinding()
	cursor, err := EncodeCursor(binding, CursorPosition{SortValue: "alpha", ObjectID: "object-1"})
	if err != nil {
		t.Fatalf("EncodeCursor() error = %v", err)
	}

	tests := map[string]func(*CursorBinding){
		"index":       func(value *CursorBinding) { value.Index = "entries-by-title" },
		"fingerprint": func(value *CursorBinding) { value.QueryFingerprint = "query-b" },
		"sort":        func(value *CursorBinding) { value.SortField = "title" },
		"direction":   func(value *CursorBinding) { value.Descending = !value.Descending },
	}
	for name, mutate := range tests {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			expected := binding
			mutate(&expected)
			if _, err := DecodeCursor(cursor, expected); !errors.Is(err, ErrInvalidCursor) {
				t.Fatalf("DecodeCursor() error = %v, want ErrInvalidCursor", err)
			}
		})
	}
}

func TestCursorRejectsRetiredIndexGeneration(t *testing.T) {
	t.Parallel()
	binding := testCursorBinding()
	cursor, err := EncodeCursor(binding, CursorPosition{SortValue: "alpha", ObjectID: "object-1"})
	if err != nil {
		t.Fatalf("EncodeCursor() error = %v", err)
	}
	binding.IndexGeneration++
	if _, err := DecodeCursor(cursor, binding); !errors.Is(err, ErrStaleCursor) {
		t.Fatalf("DecodeCursor() error = %v, want ErrStaleCursor", err)
	}
}

func TestCursorRejectsMalformedValues(t *testing.T) {
	t.Parallel()
	binding := testCursorBinding()
	cursor, err := EncodeCursor(binding, CursorPosition{SortValue: "alpha", ObjectID: "object-1"})
	if err != nil {
		t.Fatalf("EncodeCursor() error = %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		t.Fatalf("DecodeString() error = %v", err)
	}
	raw[len(raw)/2] ^= 1
	tampered := base64.RawURLEncoding.EncodeToString(raw)

	tests := map[string]string{
		"empty":         "",
		"not base64":    "***",
		"not JSON":      base64.RawURLEncoding.EncodeToString([]byte("nope")),
		"trailing data": base64.RawURLEncoding.EncodeToString([]byte(`{"p":{},"c":""}{}`)),
		"unknown field": base64.RawURLEncoding.EncodeToString([]byte(`{"p":{},"c":"","extra":true}`)),
		"tampered":      tampered,
	}
	for name, value := range tests {
		name, value := name, value
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := DecodeCursor(value, binding); !errors.Is(err, ErrInvalidCursor) {
				t.Fatalf("DecodeCursor() error = %v, want ErrInvalidCursor", err)
			}
		})
	}
}

func TestCursorRejectsInvalidInputs(t *testing.T) {
	t.Parallel()
	position := CursorPosition{SortValue: "alpha", ObjectID: "object-1"}
	bindings := []CursorBinding{
		{},
		{Index: "chunks", QueryFingerprint: "query", SortField: "updated_at"},
		{Index: " chunks", IndexGeneration: 1, QueryFingerprint: "query", SortField: "updated_at"},
	}
	for _, binding := range bindings {
		if _, err := EncodeCursor(binding, position); !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("EncodeCursor(%#v) error = %v, want ErrInvalidCursor", binding, err)
		}
	}

	if _, err := EncodeCursor(testCursorBinding(), CursorPosition{}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("EncodeCursor(empty position) error = %v, want ErrInvalidCursor", err)
	}

	tooLarge := strings.Repeat("a", maxCursorFieldLength)
	oversizedBinding := testCursorBinding()
	oversizedBinding.Index = tooLarge
	oversizedBinding.QueryFingerprint = tooLarge
	oversizedBinding.SortField = tooLarge
	if _, err := EncodeCursor(oversizedBinding, CursorPosition{SortValue: tooLarge, ObjectID: tooLarge}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("EncodeCursor(oversized result) error = %v, want ErrInvalidCursor", err)
	}
}

func testCursorBinding() CursorBinding {
	return CursorBinding{
		Index:            "chunks-by-updated-at",
		IndexGeneration:  7,
		QueryFingerprint: "sha256:normalized-query-a",
		SortField:        "updated_at",
		Descending:       true,
	}
}

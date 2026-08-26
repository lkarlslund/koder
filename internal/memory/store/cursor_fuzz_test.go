package store

import (
	"errors"
	"testing"
)

func FuzzCursorDecodeAndRoundTrip(f *testing.F) {
	binding := CursorBinding{Index: "entries", IndexGeneration: 7, QueryFingerprint: "query", SortField: "updated_at"}
	valid, err := EncodeCursor(binding, CursorPosition{SortValue: "2026-08-22", ObjectID: "019f132e-4f3a-739a-9ab2-5198dcd19e67"})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid)
	f.Add("")
	f.Add("not-base64")
	f.Fuzz(func(t *testing.T, encoded string) {
		position, err := DecodeCursor(encoded, binding)
		if err != nil {
			if !errors.Is(err, ErrInvalidCursor) && !errors.Is(err, ErrStaleCursor) {
				t.Fatalf("DecodeCursor() error = %v", err)
			}
			return
		}
		roundTrip, err := EncodeCursor(binding, position)
		if err != nil {
			t.Fatalf("EncodeCursor(decoded position) error = %v", err)
		}
		got, err := DecodeCursor(roundTrip, binding)
		if err != nil || got != position {
			t.Fatalf("cursor round trip = %#v, %v; want %#v", got, err, position)
		}
	})
}

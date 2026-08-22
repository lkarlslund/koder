package pebble

import (
	"bytes"
	"encoding/hex"
	"slices"
	"testing"
)

const testID = "019f132e-4f3a-739a-9ab2-5198dcd19e67"

func TestCanonicalRecordKeysAreVersionedAndDisjoint(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		got  []byte
		want string
	}{
		"metadata":        {got: metadataKey(), want: "k1/meta/store"},
		"chunk":           {got: chunkKey(testID), want: "k1/record/c/" + testID},
		"entry":           {got: entryKey(testID), want: "k1/record/e/" + testID},
		"link":            {got: linkKey(testID), want: "k1/record/l/" + testID},
		"evidence":        {got: evidenceKey(testID), want: "k1/record/v/" + testID},
		"chunk prefix":    {got: chunkPrefix(), want: "k1/record/c/"},
		"entry prefix":    {got: entryPrefix(), want: "k1/record/e/"},
		"link prefix":     {got: linkPrefix(), want: "k1/record/l/"},
		"evidence prefix": {got: evidencePrefix(), want: "k1/record/v/"},
	}
	seen := make(map[string]string, len(tests))
	for name, tt := range tests {
		if got := string(tt.got); got != tt.want {
			t.Errorf("%s key = %q, want %q", name, got, tt.want)
		}
		if previous, exists := seen[string(tt.got)]; exists {
			t.Errorf("%s key collides with %s", name, previous)
		}
		seen[string(tt.got)] = name
	}
}

func TestKeyFunctionsReturnOwnedBytes(t *testing.T) {
	t.Parallel()
	first := metadataKey()
	first[0] = 'x'
	if got := string(metadataKey()); got != "k1/meta/store" {
		t.Fatalf("metadata key was mutated through caller slice: %q", got)
	}
	first = chunkPrefix()
	first[0] = 'x'
	if got := string(chunkPrefix()); got != "k1/record/c/" {
		t.Fatalf("chunk prefix was mutated through caller slice: %q", got)
	}
}

func TestRevisionKeysSortNumerically(t *testing.T) {
	t.Parallel()
	keys := [][]byte{
		revisionKey(recordChunk, testID, 256),
		revisionKey(recordChunk, testID, 1),
		revisionKey(recordChunk, testID, 65536),
		revisionKey(recordChunk, testID, 2),
	}
	slices.SortFunc(keys, bytes.Compare)
	want := []uint64{1, 2, 256, 65536}
	for i, key := range keys {
		got, err := decodeRevisionKey(key, recordChunk, testID)
		if err != nil {
			t.Fatalf("decodeRevisionKey(%s): %v", hex.EncodeToString(key), err)
		}
		if got != want[i] {
			t.Errorf("sorted revision[%d] = %d, want %d", i, got, want[i])
		}
	}
}

func TestDecodeRevisionKeyRejectsOtherKeyspaces(t *testing.T) {
	t.Parallel()
	for _, key := range [][]byte{
		chunkKey(testID),
		revisionKey(recordEntry, testID, 1),
		revisionKey(recordChunk, testID+"0", 1),
		append(revisionPrefix(recordChunk, testID), 1),
	} {
		if _, err := decodeRevisionKey(key, recordChunk, testID); err == nil {
			t.Errorf("decodeRevisionKey(%q) unexpectedly succeeded", key)
		}
	}
}

func TestPrefixBoundsContainOnlyThePrefix(t *testing.T) {
	t.Parallel()
	prefix := chunkPrefix()
	lower, upper := prefixBounds(prefix)
	if !bytes.Equal(lower, prefix) || len(upper) == 0 {
		t.Fatalf("prefixBounds() = %q, %q", lower, upper)
	}
	for _, key := range [][]byte{chunkKey(testID), append(chunkPrefix(), 0xff)} {
		if bytes.Compare(key, lower) < 0 || bytes.Compare(key, upper) >= 0 {
			t.Errorf("key %q falls outside [%q, %q)", key, lower, upper)
		}
	}
	for _, key := range [][]byte{entryKey(testID), []byte("k1/record/b/before"), []byte("k1/record/d/after")} {
		if bytes.Compare(key, lower) >= 0 && bytes.Compare(key, upper) < 0 {
			t.Errorf("non-chunk key %q falls inside [%q, %q)", key, lower, upper)
		}
	}
}

func TestPrefixBoundsHandlesAllFFPrefix(t *testing.T) {
	t.Parallel()
	lower, upper := prefixBounds([]byte{0xff, 0xff})
	if !bytes.Equal(lower, []byte{0xff, 0xff}) || upper != nil {
		t.Fatalf("prefixBounds(ff) = %x, %x", lower, upper)
	}
}

func FuzzRevisionKeyRoundTrip(f *testing.F) {
	f.Add(testID, uint64(1))
	f.Add("", ^uint64(0))
	f.Fuzz(func(t *testing.T, id string, revision uint64) {
		key := revisionKey(recordChunk, id, revision)
		got, err := decodeRevisionKey(key, recordChunk, id)
		if err != nil || got != revision {
			t.Fatalf("decodeRevisionKey(revisionKey()) = %d, %v; want %d", got, err, revision)
		}
		if _, err := decodeRevisionKey(key, recordEntry, id); err == nil {
			t.Fatal("revision key crossed record-kind keyspace")
		}
	})
}

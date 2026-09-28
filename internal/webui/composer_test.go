package webui

import "testing"

func TestComposerByteOffset(t *testing.T) {
	for _, tc := range []struct{ units, bytes int }{
		{-1, 9}, {0, 0}, {1, 2}, {2, 2}, {3, 6}, {4, 7}, {5, 8}, {6, 9}, {99, 9},
	} {
		if got := composerByteOffset("æ😀 $x", tc.units); got != tc.bytes {
			t.Errorf("offset %d: got %d, want %d", tc.units, got, tc.bytes)
		}
	}
}

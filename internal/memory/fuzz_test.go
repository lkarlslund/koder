package memory

import (
	"strings"
	"testing"
)

func FuzzObjectRefUUIDv7Validation(f *testing.F) {
	f.Add("019f132e-4f3a-739a-9ab2-5198dcd19e67")
	f.Add("019f132e-4f3a-439a-9ab2-5198dcd19e67")
	f.Add("")
	f.Fuzz(func(t *testing.T, value string) {
		err := (ObjectRef{Kind: ObjectKindChunk, ID: value}).Validate()
		if err == nil && !hasCanonicalUUIDv7Shape(value) {
			t.Fatalf("ObjectRef.Validate() accepted non-UUIDv7 %q", value)
		}
	})
}

func hasCanonicalUUIDv7Shape(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' || value[14] != '7' || !strings.ContainsRune("89ab", rune(value[19])) {
		return false
	}
	for index := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if (value[index] < '0' || value[index] > '9') && (value[index] < 'a' || value[index] > 'f') {
			return false
		}
	}
	return true
}

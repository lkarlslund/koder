package id

import "testing"

func TestIsCanonicalV7(t *testing.T) {
	if generated := New(); !IsCanonicalV7(generated) {
		t.Fatalf("generated ID %q is not canonical UUIDv7", generated)
	}
	for _, value := range []string{
		"",
		"01a0f1c8-a90e-7569-a5f6-efca926f9df",
		"01A0F1C8-A90E-7569-A5F6-EFCA926F9DF8",
		"01a0f1c8-a90e-4569-a5f6-efca926f9df8",
		"01a0f1c8-a90e-7569-c5f6-efca926f9df8",
		"01a0f1c8xa90e-7569-a5f6-efca926f9df8",
	} {
		if IsCanonicalV7(value) {
			t.Errorf("IsCanonicalV7(%q) = true", value)
		}
	}
}

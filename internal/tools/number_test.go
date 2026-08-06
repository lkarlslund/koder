package tools

import (
	"strconv"
	"testing"
)

func TestParseToolIntAcceptsCanonicalAndProviderIntegralForms(t *testing.T) {
	for raw, want := range map[string]int{
		"2000":       2000,
		"2000.00000": 2000,
		"2e3":        2000,
		" 42 ":       42,
		"-3.0":       -3,
	} {
		t.Run(raw, func(t *testing.T) {
			got, err := ParseToolInt(raw)
			if err != nil {
				t.Fatal(err)
			}
			if got.Int() != want || got.String() != strconv.Itoa(want) {
				t.Fatalf("ParseToolInt(%q) = %d/%q, want %d", raw, got.Int(), got.String(), want)
			}
		})
	}
}

func TestParseToolIntRejectsFractionalAndOutOfRangeValues(t *testing.T) {
	for _, raw := range []string{"", "2.5", "NaN", "Inf", "999999999999999999999999999999999999"} {
		t.Run(raw, func(t *testing.T) {
			if _, err := ParseToolInt(raw); err == nil {
				t.Fatalf("ParseToolInt(%q) unexpectedly succeeded", raw)
			}
		})
	}
}

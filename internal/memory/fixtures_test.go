package memory

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type fixtureRecord interface {
	Validate() error
}

func TestCanonicalJSONFixtures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		new  func() fixtureRecord
	}{
		{name: "chunk", new: func() fixtureRecord { return new(Chunk) }},
		{name: "entry", new: func() fixtureRecord { return new(Entry) }},
		{name: "link", new: func() fixtureRecord { return new(Link) }},
		{name: "evidence", new: func() fixtureRecord { return new(Evidence) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join("..", "..", "protocol", "memory", "v1", "testdata", tt.name+".json")
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			value := tt.new()
			if err := json.Unmarshal(want, value); err != nil {
				t.Fatalf("unmarshal fixture: %v", err)
			}
			if err := value.Validate(); err != nil {
				t.Fatalf("validate fixture: %v", err)
			}
			got, err := json.MarshalIndent(value, "", "  ")
			if err != nil {
				t.Fatalf("marshal fixture: %v", err)
			}
			got = append(got, '\n')
			if !bytes.Equal(got, want) {
				t.Fatalf("fixture is not canonical\ngot:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

func TestOptionalTimesAreOmittedFromJSON(t *testing.T) {
	t.Parallel()
	data, err := json.Marshal(validEntry())
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, unwanted := range [][]byte{
		[]byte(`"verified_at"`),
		[]byte(`"valid_from"`),
		[]byte(`"valid_until"`),
		[]byte(`"observed_at"`),
		[]byte(`"review_after"`),
		[]byte(`"last_used_at"`),
		[]byte(`"0001-01-01T00:00:00Z"`),
	} {
		if bytes.Contains(data, unwanted) {
			t.Errorf("Marshal() included optional zero value %s in %s", unwanted, data)
		}
	}
}

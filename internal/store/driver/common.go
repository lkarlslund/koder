package driver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	SchemaVersion = 7
	EncodingJSON  = "json"
)

type Meta struct {
	SchemaVersion int    `json:"schema_version"`
	Encoding      string `json:"encoding"`
	Backend       string `json:"backend"`
}

func DefaultMeta(backend string) Meta {
	return Meta{
		SchemaVersion: SchemaVersion,
		Encoding:      EncodingJSON,
		Backend:       backend,
	}
}

func EnsureContext(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func CloneBytes(data []byte) []byte {
	if len(data) == 0 {
		return nil
	}
	out := make([]byte, len(data))
	copy(out, data)
	return out
}

func EncodeJSON(v any) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func FormatUnixNanos(t time.Time) string {
	return fmt.Sprintf("%020d", t.UTC().UnixNano())
}

func RecordPrefix(namespace string) string {
	return "collection/" + namespace + "/"
}

func RecordKey(namespace string, id string) string {
	return RecordPrefix(namespace) + id
}

func IndexPrefix(namespace, name, value string) string {
	return "collection-index/" + namespace + "/" + name + "/" + value + "/"
}

func IndexKey(namespace, name, value, id string) string {
	return IndexPrefix(namespace, name, value) + id
}

func WriteJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func ReadJSONFile(path string, dst any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dst)
}

func EnsureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}

func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func SortedJSONPaths(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		out = append(out, filepath.Join(root, entry.Name()))
	}
	return out, nil
}

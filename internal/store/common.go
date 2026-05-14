package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	schemaVersion = 6
	encodingJSON  = "json"
)

type metaRecord struct {
	SchemaVersion int    `json:"schema_version"`
	Encoding      string `json:"encoding"`
	Backend       string `json:"backend"`
}

func defaultMeta(backend string) metaRecord {
	return metaRecord{
		SchemaVersion: schemaVersion,
		Encoding:      encodingJSON,
		Backend:       backend,
	}
}

func ensureContext(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func cloneBytes(data []byte) []byte {
	if len(data) == 0 {
		return nil
	}
	out := make([]byte, len(data))
	copy(out, data)
	return out
}

func encodeJSON(v any) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func formatUnixNanos(t time.Time) string {
	return fmt.Sprintf("%020d", t.UTC().UnixNano())
}

func collectionRecordPrefix(namespace string) string {
	return "collection/" + namespace + "/"
}

func collectionRecordKey(namespace string, id string) string {
	return collectionRecordPrefix(namespace) + id
}

func collectionIndexKey(namespace, name, value, id string) string {
	return "collection-index/" + namespace + "/" + name + "/" + value + "/" + id
}

func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func readJSONFile(path string, dst any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dst)
}

func ensureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func sortedJSONPaths(root string) ([]string, error) {
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

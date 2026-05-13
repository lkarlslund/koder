package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	schemaVersion = 3
	encodingJSON  = "json"
)

type metaRecord struct {
	SchemaVersion  int              `json:"schema_version"`
	Encoding       string           `json:"encoding"`
	Backend        string           `json:"backend"`
	NextSessionID  int64            `json:"next_session_id"`
	NextChatID     int64            `json:"next_chat_id"`
	NextApprovalID int64            `json:"next_approval_id"`
	NextTaskID     int64            `json:"next_task_id"`
	NextTodoID     int64            `json:"next_todo_id"`
	NextIDs        map[string]int64 `json:"next_ids,omitempty"`
}

func defaultMeta(backend string) metaRecord {
	return metaRecord{
		SchemaVersion:  schemaVersion,
		Encoding:       encodingJSON,
		Backend:        backend,
		NextSessionID:  1,
		NextChatID:     1,
		NextApprovalID: 1,
		NextTaskID:     1,
		NextTodoID:     1,
		NextIDs:        map[string]int64{},
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

func formatID(id int64) string {
	return fmt.Sprintf("%020d", id)
}

func formatUnixNanos(t time.Time) string {
	return fmt.Sprintf("%020d", t.UTC().UnixNano())
}

func collectionRecordPrefix(namespace string) string {
	return "collection/" + namespace + "/"
}

func collectionRecordKey(namespace string, id int64) string {
	return collectionRecordPrefix(namespace) + formatID(id)
}

func collectionIndexKey(namespace, name, value string, id int64) string {
	return "collection-index/" + namespace + "/" + name + "/" + value + "/" + formatID(id)
}

func parseIDFromSuffix(key, prefix string) (int64, error) {
	raw := strings.TrimPrefix(key, prefix)
	return strconv.ParseInt(raw, 10, 64)
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

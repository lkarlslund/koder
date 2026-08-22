package pebble

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	cockroachpebble "github.com/cockroachdb/pebble"

	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

const (
	backendName            = "pebble"
	keyFormat              = "k1"
	recordEncoding         = "json"
	currentSchemaVersion   = uint32(1)
	initialIndexGeneration = uint64(1)
)

type metadata struct {
	Backend         string    `json:"backend"`
	KeyFormat       string    `json:"key_format"`
	Encoding        string    `json:"encoding"`
	SchemaVersion   uint32    `json:"schema_version"`
	IndexGeneration uint64    `json:"index_generation"`
	CreatedAt       time.Time `json:"created_at"`
}

func newMetadata(now time.Time) metadata {
	return metadata{
		Backend:         backendName,
		KeyFormat:       keyFormat,
		Encoding:        recordEncoding,
		SchemaVersion:   currentSchemaVersion,
		IndexGeneration: initialIndexGeneration,
		CreatedAt:       now.UTC().Round(0),
	}
}

func (m metadata) validate() error {
	if m.Backend != backendName {
		return fmt.Errorf("%w: backend %q, want %q", knowledgeStore.ErrIncompatible, m.Backend, backendName)
	}
	if m.KeyFormat != keyFormat {
		return fmt.Errorf("%w: key format %q, want %q", knowledgeStore.ErrIncompatible, m.KeyFormat, keyFormat)
	}
	if m.Encoding != recordEncoding {
		return fmt.Errorf("%w: encoding %q, want %q", knowledgeStore.ErrIncompatible, m.Encoding, recordEncoding)
	}
	if m.SchemaVersion != currentSchemaVersion {
		return fmt.Errorf("%w: schema version %d, want %d", knowledgeStore.ErrIncompatible, m.SchemaVersion, currentSchemaVersion)
	}
	if m.IndexGeneration == 0 {
		return fmt.Errorf("%w: index generation must be positive", knowledgeStore.ErrIncompatible)
	}
	if m.CreatedAt.IsZero() {
		return fmt.Errorf("%w: created_at is required", knowledgeStore.ErrIncompatible)
	}
	_, offset := m.CreatedAt.Zone()
	if offset != 0 {
		return fmt.Errorf("%w: created_at must be UTC", knowledgeStore.ErrIncompatible)
	}
	return nil
}

func initializeMetadata(db *cockroachpebble.DB, now time.Time) (metadata, error) {
	data, closer, err := db.Get(metadataKey())
	if err == nil {
		defer func() { _ = closer.Close() }()
		value, err := decodeMetadata(data)
		if err != nil {
			return metadata{}, err
		}
		return value, value.validate()
	}
	if !errors.Is(err, cockroachpebble.ErrNotFound) {
		return metadata{}, fmt.Errorf("read knowledge metadata: %w", err)
	}

	empty, err := userKeyspaceEmpty(db)
	if err != nil {
		return metadata{}, err
	}
	if !empty {
		return metadata{}, fmt.Errorf("%w: metadata is missing from a non-empty database", knowledgeStore.ErrIncompatible)
	}

	value := newMetadata(now)
	encoded, err := json.Marshal(value)
	if err != nil {
		return metadata{}, fmt.Errorf("encode knowledge metadata: %w", err)
	}
	if err := db.Set(metadataKey(), encoded, cockroachpebble.Sync); err != nil {
		return metadata{}, fmt.Errorf("write knowledge metadata: %w", err)
	}
	return value, nil
}

func decodeMetadata(data []byte) (metadata, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value metadata
	if err := decoder.Decode(&value); err != nil {
		return metadata{}, fmt.Errorf("%w: decode metadata: %v", knowledgeStore.ErrIncompatible, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return metadata{}, fmt.Errorf("%w: metadata contains trailing data", knowledgeStore.ErrIncompatible)
	}
	return value, nil
}

func userKeyspaceEmpty(db *cockroachpebble.DB) (bool, error) {
	iter, err := db.NewIter(nil)
	if err != nil {
		return false, fmt.Errorf("inspect knowledge keyspace: %w", err)
	}
	defer func() { _ = iter.Close() }()
	empty := !iter.First()
	if err := iter.Error(); err != nil {
		return false, fmt.Errorf("inspect knowledge keyspace: %w", err)
	}
	return empty, nil
}

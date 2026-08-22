package store

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	cursorFormatVersion  = uint8(1)
	maxCursorLength      = 16 << 10
	maxCursorFieldLength = 4 << 10
	cursorChecksumSize   = 16
)

// CursorBinding identifies the result set and ordering to which a cursor belongs.
// QueryFingerprint must be derived from the normalized query and lifecycle,
// authorization, and other filters; raw query content should not be placed here.
type CursorBinding struct {
	Index            string
	IndexGeneration  uint64
	QueryFingerprint string
	SortField        string
	Descending       bool
}

// CursorPosition is the exclusive position after which the next page starts. SortValue
// is the index's canonical sortable representation, not a private backend key. ObjectID
// is the deterministic final tie-breaker.
type CursorPosition struct {
	SortValue string
	ObjectID  string
}

type cursorPayload struct {
	Version          uint8  `json:"v"`
	Index            string `json:"i"`
	IndexGeneration  uint64 `json:"g"`
	QueryFingerprint string `json:"q"`
	SortField        string `json:"s"`
	Descending       bool   `json:"d"`
	SortValue        string `json:"p"`
	ObjectID         string `json:"o"`
}

type cursorEnvelope struct {
	Payload  cursorPayload `json:"p"`
	Checksum string        `json:"c"`
}

// EncodeCursor produces an opaque, URL-safe cursor. The checksum detects truncation and
// accidental mutation; cursor contents are not an authorization token and all filters
// must be reapplied when it is decoded.
func EncodeCursor(binding CursorBinding, position CursorPosition) (string, error) {
	if err := validateCursorBinding(binding); err != nil {
		return "", err
	}
	if err := validateCursorPosition(position); err != nil {
		return "", err
	}
	payload := cursorPayload{
		Version:          cursorFormatVersion,
		Index:            binding.Index,
		IndexGeneration:  binding.IndexGeneration,
		QueryFingerprint: binding.QueryFingerprint,
		SortField:        binding.SortField,
		Descending:       binding.Descending,
		SortValue:        position.SortValue,
		ObjectID:         position.ObjectID,
	}
	checksum, err := cursorChecksum(payload)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(cursorEnvelope{Payload: payload, Checksum: checksum})
	if err != nil {
		return "", fmt.Errorf("%w: encode envelope: %v", ErrInvalidCursor, err)
	}
	cursor := base64.RawURLEncoding.EncodeToString(encoded)
	if len(cursor) > maxCursorLength {
		return "", invalidCursor("encoded cursor is too large")
	}
	return cursor, nil
}

// DecodeCursor validates an opaque cursor against the current query binding. A changed
// query or ordering is invalid; a retired index generation is stale so callers can tell
// clients to restart pagination.
func DecodeCursor(encoded string, expected CursorBinding) (CursorPosition, error) {
	if err := validateCursorBinding(expected); err != nil {
		return CursorPosition{}, err
	}
	if encoded == "" || len(encoded) > maxCursorLength {
		return CursorPosition{}, invalidCursor("cursor length is invalid")
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return CursorPosition{}, invalidCursor("cursor is not URL-safe base64")
	}

	var envelope cursorEnvelope
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return CursorPosition{}, invalidCursor("decode cursor envelope")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return CursorPosition{}, invalidCursor("cursor contains trailing data")
	}
	if envelope.Payload.Version != cursorFormatVersion {
		return CursorPosition{}, invalidCursor("unsupported cursor version")
	}
	if err := validatePayload(envelope.Payload); err != nil {
		return CursorPosition{}, err
	}
	wantChecksum, err := cursorChecksum(envelope.Payload)
	if err != nil {
		return CursorPosition{}, err
	}
	if subtle.ConstantTimeCompare([]byte(envelope.Checksum), []byte(wantChecksum)) != 1 {
		return CursorPosition{}, invalidCursor("cursor checksum does not match")
	}

	actual := envelope.Payload
	if actual.Index != expected.Index ||
		actual.QueryFingerprint != expected.QueryFingerprint ||
		actual.SortField != expected.SortField ||
		actual.Descending != expected.Descending {
		return CursorPosition{}, invalidCursor("cursor belongs to a different query")
	}
	if actual.IndexGeneration != expected.IndexGeneration {
		return CursorPosition{}, fmt.Errorf("%w: index generation %d was retired", ErrStaleCursor, actual.IndexGeneration)
	}
	return CursorPosition{SortValue: actual.SortValue, ObjectID: actual.ObjectID}, nil
}

func validatePayload(payload cursorPayload) error {
	return errors.Join(
		validateCursorBinding(CursorBinding{
			Index:            payload.Index,
			IndexGeneration:  payload.IndexGeneration,
			QueryFingerprint: payload.QueryFingerprint,
			SortField:        payload.SortField,
			Descending:       payload.Descending,
		}),
		validateCursorPosition(CursorPosition{SortValue: payload.SortValue, ObjectID: payload.ObjectID}),
	)
}

func validateCursorBinding(binding CursorBinding) error {
	if !validCursorField(binding.Index, false) {
		return invalidCursor("index is required")
	}
	if binding.IndexGeneration == 0 {
		return invalidCursor("index generation must be positive")
	}
	if !validCursorField(binding.QueryFingerprint, false) {
		return invalidCursor("query fingerprint is required")
	}
	if !validCursorField(binding.SortField, false) {
		return invalidCursor("sort field is required")
	}
	return nil
}

func validateCursorPosition(position CursorPosition) error {
	if !validCursorField(position.SortValue, true) {
		return invalidCursor("sort value is too large or invalid")
	}
	if !validCursorField(position.ObjectID, false) {
		return invalidCursor("object ID is required")
	}
	return nil
}

func validCursorField(value string, allowEmpty bool) bool {
	if len(value) > maxCursorFieldLength || strings.TrimSpace(value) != value {
		return false
	}
	return allowEmpty || value != ""
}

func cursorChecksum(payload cursorPayload) (string, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("%w: encode checksum: %v", ErrInvalidCursor, err)
	}
	digest := sha256.Sum256(encoded)
	return base64.RawURLEncoding.EncodeToString(digest[:cursorChecksumSize]), nil
}

func invalidCursor(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidCursor, message)
}

// Package pebble implements the private Pebble persistence format for Koder Knowledge.
package pebble

import (
	"encoding/binary"
	"fmt"
)

const (
	keyFormatPrefix = "k1/"

	recordChunk    byte = 'c'
	recordEntry    byte = 'e'
	recordLink     byte = 'l'
	recordEvidence byte = 'v'
)

var (
	storeMetadataKey = []byte(keyFormatPrefix + "meta/store")
	canonicalPrefix  = []byte(keyFormatPrefix + "record/")
	revisionsPrefix  = []byte(keyFormatPrefix + "revision/")
)

func metadataKey() []byte {
	return append([]byte(nil), storeMetadataKey...)
}

func chunkKey(id string) []byte {
	return recordKey(recordChunk, id)
}

func entryKey(id string) []byte {
	return recordKey(recordEntry, id)
}

func linkKey(id string) []byte {
	return recordKey(recordLink, id)
}

func evidenceKey(id string) []byte {
	return recordKey(recordEvidence, id)
}

func chunkPrefix() []byte {
	return recordPrefix(recordChunk)
}

func entryPrefix() []byte {
	return recordPrefix(recordEntry)
}

func linkPrefix() []byte {
	return recordPrefix(recordLink)
}

func evidencePrefix() []byte {
	return recordPrefix(recordEvidence)
}

func recordPrefix(kind byte) []byte {
	key := make([]byte, 0, len(canonicalPrefix)+2)
	key = append(key, canonicalPrefix...)
	key = append(key, kind, '/')
	return key
}

func recordKey(kind byte, id string) []byte {
	key := recordPrefix(kind)
	return append(key, id...)
}

func revisionPrefix(kind byte, id string) []byte {
	key := make([]byte, 0, len(revisionsPrefix)+len(id)+3)
	key = append(key, revisionsPrefix...)
	key = append(key, kind, '/')
	key = append(key, id...)
	return append(key, '/')
}

func revisionKey(kind byte, id string, revision uint64) []byte {
	key := revisionPrefix(kind, id)
	return binary.BigEndian.AppendUint64(key, revision)
}

func decodeRevisionKey(key []byte, kind byte, id string) (uint64, error) {
	prefix := revisionPrefix(kind, id)
	if len(key) != len(prefix)+8 || string(key[:len(prefix)]) != string(prefix) {
		return 0, fmt.Errorf("invalid knowledge revision key")
	}
	return binary.BigEndian.Uint64(key[len(prefix):]), nil
}

// prefixBounds returns a half-open Pebble iteration range containing every key with prefix.
func prefixBounds(prefix []byte) (lower, upper []byte) {
	lower = append([]byte(nil), prefix...)
	upper = append([]byte(nil), prefix...)
	for i := len(upper) - 1; i >= 0; i-- {
		if upper[i] != 0xff {
			upper[i]++
			return lower, upper[:i+1]
		}
	}
	return lower, nil
}

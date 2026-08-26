// Package pebble implements the private Pebble persistence format for Koder Memory.
package pebble

import (
	"encoding/binary"
	"fmt"

	"github.com/lkarlslund/koder/internal/memory"
)

const (
	keyFormatPrefix = "k1/"

	recordChunk    byte = 'c'
	recordEntry    byte = 'e'
	recordLink     byte = 'l'
	recordEvidence byte = 'v'
)

var (
	storeMetadataKey   = []byte(keyFormatPrefix + "meta/store")
	canonicalPrefix    = []byte(keyFormatPrefix + "record/")
	revisionsPrefix    = []byte(keyFormatPrefix + "revision/")
	indexesPrefix      = []byte(keyFormatPrefix + "index/")
	entryUsagePrefix   = []byte(keyFormatPrefix + "usage/entry/")
	usageEventPrefix   = []byte(keyFormatPrefix + "usage-event/entry/")
	graphViewPrefix    = []byte(keyFormatPrefix + "user-view/graph/")
	packageAssetPrefix = []byte(keyFormatPrefix + "package-asset/")
)

func assetChunkPrefix(chunkID memory.ChunkID) []byte {
	return append(append([]byte(nil), packageAssetPrefix...), encodeIndexTuple(string(chunkID))...)
}

func assetKey(chunkID memory.ChunkID, path string) []byte {
	return append(assetChunkPrefix(chunkID), encodeIndexTuple(path)...)
}

func metadataKey() []byte {
	return append([]byte(nil), storeMetadataKey...)
}

func entryUsageKey(id memory.EntryID) []byte {
	return append(append([]byte(nil), entryUsagePrefix...), id...)
}

func entryUsageEventEntryPrefix(id memory.EntryID) []byte {
	return append(append([]byte(nil), usageEventPrefix...), encodeIndexTuple(string(id))...)
}

func entryUsageEventKey(id memory.EntryID, eventID string) []byte {
	return append(append([]byte(nil), usageEventPrefix...), encodeIndexTuple(string(id), eventID)...)
}

func graphViewOwnerPrefix(ownerKey string) []byte {
	return append(append([]byte(nil), graphViewPrefix...), encodeIndexTuple(ownerKey)...)
}

func graphViewKey(ownerKey, id string) []byte {
	return append(graphViewOwnerPrefix(ownerKey), encodeIndexTuple(id)...)
}

func indexGenerationPrefix(generation uint64) []byte {
	key := append([]byte(nil), indexesPrefix...)
	key = binary.BigEndian.AppendUint64(key, generation)
	return append(key, '/')
}

func indexKey(generation uint64, name string, suffix []byte) []byte {
	key := indexGenerationPrefix(generation)
	key = append(key, name...)
	key = append(key, '/')
	return append(key, suffix...)
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
		return 0, fmt.Errorf("invalid memory revision key")
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

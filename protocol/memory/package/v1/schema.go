// Package memorypackagev1 publishes the immutable .kmemory v1 wire contract.
package memorypackagev1

import (
	"bytes"
	_ "embed"
)

//go:embed manifest.schema.json
var manifestSchema []byte

func ManifestSchema() []byte {
	return bytes.Clone(manifestSchema)
}

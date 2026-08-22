// Package knowledgepackagev1 publishes the immutable .kknowledge v1 wire contract.
package knowledgepackagev1

import (
	"bytes"
	_ "embed"
)

//go:embed manifest.schema.json
var manifestSchema []byte

func ManifestSchema() []byte {
	return bytes.Clone(manifestSchema)
}

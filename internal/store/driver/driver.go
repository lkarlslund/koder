package driver

import "context"

// Backend stores opaque records grouped by namespace.
type Backend interface {
	Close() error
	Get(context.Context, string, string) ([]byte, error)
	Put(context.Context, string, string, []byte, map[string]string) error
	Delete(context.Context, string, string) error
	List(context.Context, string, *IndexLookup) ([][]byte, error)
}

// IndexLookup selects records by one secondary index.
type IndexLookup struct {
	Name  string
	Value string
}

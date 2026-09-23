package driver

import "context"

// Backend stores opaque records grouped by namespace.
type Backend interface {
	Close() error
	Get(context.Context, string, string) ([]byte, error)
	Put(context.Context, string, string, []byte, map[string]IndexValue) error
	Delete(context.Context, string, string) error
	List(context.Context, string, *IndexLookup) ([][]byte, error)
	ListIndexPage(context.Context, string, IndexPageRequest) (IndexPage, error)
	AddIndexEntries(context.Context, string, string, string, []OrderedIndexEntry) error
}

type IndexValue struct {
	Value string
	Order string
}

type OrderedIndexEntry struct {
	ID    string
	Order string
}

// IndexLookup selects records by one secondary index.
type IndexLookup struct {
	Name  string
	Value string
}

// IndexPageRequest selects a bounded page from one exact secondary-index
// value. Before and After are exclusive record-ID cursors. Tail selects the
// newest matching records; otherwise the oldest matching records are selected.
type IndexPageRequest struct {
	Name   string
	Value  string
	Before string
	After  string
	Limit  int
	Tail   bool
}

// IndexPage is a bounded index result in ascending record-ID order.
type IndexPage struct {
	Items     [][]byte
	Total     int
	HasBefore bool
	HasAfter  bool
}

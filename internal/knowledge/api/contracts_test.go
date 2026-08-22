package knowledgeapi

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
)

func TestV1FixturesStrictlyMatchContracts(t *testing.T) {
	tests := []struct {
		name  string
		file  string
		new   func() any
		check func(*testing.T, any)
	}{
		{
			name: "chunk list response", file: "chunk-list-response.json", new: func() any { return &ChunkListResponse{} },
			check: func(t *testing.T, value any) {
				response := value.(*ChunkListResponse)
				if response.APIVersion != Version || len(response.Chunks) != 1 || response.Chunks[0].Revision.Number != 3 || response.Page.NextCursor == "" {
					t.Fatalf("chunk fixture = %#v", response)
				}
			},
		},
		{
			name: "entry update request", file: "entry-update-request.json", new: func() any { return &EntryUpdateRequest{} },
			check: func(t *testing.T, value any) {
				request := value.(*EntryUpdateRequest)
				if request.ExpectedRevision != 3 || request.Entry.Kind != knowledge.EntryKindProcedure || request.Entry.Scope.Kind != knowledge.ScopeKindEnvironment {
					t.Fatalf("entry update fixture = %#v", request)
				}
			},
		},
		{
			name: "search response", file: "search-response.json", new: func() any { return &SearchResponse{} },
			check: func(t *testing.T, value any) {
				response := value.(*SearchResponse)
				if response.APIVersion != Version || len(response.Matches) != 1 || response.Matches[0].Rank.Total != 1 || response.Page.Returned != 1 {
					t.Fatalf("search fixture = %#v", response)
				}
			},
		},
		{
			name: "error response", file: "error-response.json", new: func() any { return &ErrorResponse{} },
			check: func(t *testing.T, value any) {
				response := value.(*ErrorResponse)
				if response.APIVersion != Version || response.Error == nil || response.Error.Code != "conflict" || response.Error.Details == nil || response.Error.Details.EntryID == "" {
					t.Fatalf("error fixture = %#v", response)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", "v1", test.file))
			if err != nil {
				t.Fatal(err)
			}
			value := test.new()
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(value); err != nil {
				t.Fatalf("decode %s: %v", test.file, err)
			}
			if err := requireJSONEOF(decoder); err != nil {
				t.Fatalf("trailing content in %s: %v", test.file, err)
			}
			test.check(t, value)
			encoded, err := json.Marshal(value)
			if err != nil || !json.Valid(encoded) {
				t.Fatalf("marshal %s: %s (%v)", test.file, encoded, err)
			}
		})
	}
}

func TestWriteContractsExcludeServerOwnedFields(t *testing.T) {
	request := EntryUpdateRequest{
		Entry: EntryContent{
			Kind:  knowledge.EntryKindProcedure,
			Title: "Use sfdisk",
			Scope: knowledge.Scope{Kind: knowledge.ScopeKindGlobal},
		},
		ExpectedRevision: 4,
	}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"id"`, `"chunk_id"`, `"revision"`, `"state"`, `"created_at"`, `"updated_at"`, `"last_used_at"`} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("write contract exposed server-owned field %s: %s", forbidden, data)
		}
	}
}

func TestResourceMetadataUsesStableRevisionAndExplorerLinks(t *testing.T) {
	ref := knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: "01a01688-fc5d-7f7d-8bb8-de244977f8a5"}
	revision := knowledge.Revision{ID: "01a01688-fc5d-7f7d-8bb8-de244977f8a6", Number: 7, CreatedAt: time.Unix(1, 0).UTC()}
	metadata := Resource(ref, revision)
	if metadata.ETag != `"knowledge-01a01688-fc5d-7f7d-8bb8-de244977f8a6"` ||
		metadata.ExplorerURL != "/knowledge?object_kind=entry&id=01a01688-fc5d-7f7d-8bb8-de244977f8a5" {
		t.Fatalf("resource metadata = %#v", metadata)
	}
	if got := ObjectExplorerURL(knowledge.ObjectRef{}); got != ExplorerURL {
		t.Fatalf("empty explorer URL = %q", got)
	}
	if got := Metadata(" request-7 "); got.APIVersion != Version || got.RequestID != "request-7" {
		t.Fatalf("response metadata = %#v", got)
	}
}

func TestChunkRoutesRemainVersionedAndEscaped(t *testing.T) {
	chunkID := knowledge.ChunkID("chunk with/slash")
	if got := ChunkPath(chunkID); got != "/api/knowledge/v1/chunks/chunk%20with%2Fslash" {
		t.Fatalf("ChunkPath() = %q", got)
	}
	if got := ChunkLifecyclePath(chunkID, " archive "); got != "/api/knowledge/v1/chunks/chunk%20with%2Fslash/archive" {
		t.Fatalf("ChunkLifecyclePath() = %q", got)
	}
	entryID := knowledge.EntryID("entry with/slash")
	if got := EntryEvidencePath(entryID); got != "/api/knowledge/v1/entries/entry%20with%2Fslash/evidence" {
		t.Fatalf("EntryEvidencePath() = %q", got)
	}
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if err == io.EOF {
		return nil
	}
	return err
}

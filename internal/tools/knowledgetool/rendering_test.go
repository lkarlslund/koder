package knowledgetool

import (
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/tools"
)

func TestKnowledgePresentationUsesActionSpecificLabels(t *testing.T) {
	presentation := tool{}.Presentation(tools.Request{Args: map[string]string{
		"action": "search",
		"query":  "Linux partition tools",
	}})
	if presentation.Title != "Search knowledge" || presentation.Preview != "Search knowledge for Linux partition tools" || presentation.Subtitle != presentation.Preview {
		t.Fatalf("presentation = %#v", presentation)
	}

	presentation = tool{}.Presentation(tools.Request{Args: map[string]string{
		"action": "entry_archive",
		"id":     "entry-1",
	}})
	if presentation.Title != "Archive knowledge entry" || presentation.Preview != "Knowledge archive entry entry-1" {
		t.Fatalf("archive presentation = %#v", presentation)
	}
}

func TestKnowledgeResultSummaryCoversReadAndMutationFamilies(t *testing.T) {
	tests := []struct {
		name   string
		action string
		stored any
		want   string
	}{
		{name: "search", action: "search", stored: knowledgeService.LexicalSearchResult{Matches: make([]knowledgeService.LexicalSearchMatch, 2)}, want: "Found 2 knowledge results"},
		{name: "get", action: "get", stored: recordResult{Kind: knowledgeStore.RecordKindEntry, Entry: &knowledge.Entry{Title: "Use sfdisk"}}, want: "Read knowledge entry: Use sfdisk"},
		{name: "neighbors", action: "neighbors", stored: neighborPageResult{Neighbors: []neighborResult{{}}}, want: "Found 1 linked object"},
		{name: "chunks", action: "chunk_list", stored: chunkPageResult{Chunks: []knowledge.Chunk{{}, {}}}, want: "Listed 2 knowledge chunks"},
		{name: "chunk mutation", action: "chunk_archive", stored: chunkMutationResult{Chunk: knowledge.Chunk{Title: "Linux tools"}}, want: "Archived knowledge chunk: Linux tools"},
		{name: "entry mutation", action: "verify", stored: entryMutationResult{Entry: knowledge.Entry{Title: "Back up first"}}, want: "Verified knowledge entry: Back up first"},
		{name: "link", action: "unlink", stored: linkMutationResult{}, want: "Unlinked knowledge link"},
		{name: "history", action: "history", stored: historyPageResult{Revisions: make([]historyRevisionResult, 3)}, want: "Loaded 3 knowledge revisions"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := tools.Result{Output: "full structured result", Stored: test.stored}
			summary, body := tool{}.SummarizeResult(tools.Request{Args: map[string]string{"action": test.action}}, result)
			if summary != test.want || body != result.Output {
				t.Fatalf("summary, body = %q, %q; want %q and unchanged output", summary, body, test.want)
			}
		})
	}
}

func TestKnowledgeResultSummarySurvivesStoredJSONDecoding(t *testing.T) {
	stored := map[string]any{
		"matches": []any{
			map[string]any{"entry_id": "entry-1", "document": map[string]any{"title": "Use sfdisk"}},
			map[string]any{"entry_id": "entry-2", "document": map[string]any{"title": "Back up first"}},
		},
	}
	summary, body := tool{}.SummarizeResult(
		tools.Request{Args: map[string]string{"action": "search"}},
		tools.Result{Output: `{"matches":[...]}`, Stored: stored},
	)
	if summary != "Found 2 knowledge results" || body != `{"matches":[...]}` {
		t.Fatalf("decoded summary, body = %q, %q", summary, body)
	}
}

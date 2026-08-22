package knowledgetool

import (
	"encoding/json"
	"fmt"
	"strings"

	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
	"github.com/lkarlslund/koder/internal/tools"
)

func (tool) Presentation(req tools.Request) tools.Presentation {
	preview := tool{}.Preview(req)
	return tools.Presentation{
		Title:    knowledgeActionTitle(req.Args["action"]),
		Subtitle: preview,
		Preview:  preview,
	}
}

// SummarizeResult gives clients a compact, human-readable label while leaving
// the complete structured result available to the model and transcript.
func (tool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return knowledgeResultSummary(req, result.Stored), result.Output
}

func knowledgeActionTitle(action string) string {
	switch strings.TrimSpace(action) {
	case "search":
		return "Search knowledge"
	case "get":
		return "Get knowledge"
	case "neighbors":
		return "Explore knowledge links"
	case "chunk_list":
		return "List knowledge chunks"
	case "chunk_get":
		return "Get knowledge chunk"
	case "chunk_create":
		return "Create knowledge chunk"
	case "chunk_update":
		return "Update knowledge chunk"
	case "chunk_archive":
		return "Archive knowledge chunk"
	case "chunk_restore":
		return "Restore knowledge chunk"
	case "chunk_delete":
		return "Delete knowledge chunk"
	case "entry_create":
		return "Create knowledge entry"
	case "entry_update":
		return "Update knowledge entry"
	case "entry_supersede":
		return "Supersede knowledge entry"
	case "entry_archive":
		return "Archive knowledge entry"
	case "entry_restore":
		return "Restore knowledge entry"
	case "entry_delete":
		return "Delete knowledge entry"
	case "link":
		return "Link knowledge"
	case "unlink":
		return "Unlink knowledge"
	case "verify":
		return "Verify knowledge entry"
	case "history":
		return "Review knowledge history"
	default:
		return "Use knowledge"
	}
}

func knowledgeResultSummary(req tools.Request, stored any) string {
	action := strings.TrimSpace(req.Args["action"])
	switch action {
	case "search":
		value, ok := decodeKnowledgeResult[knowledgeService.LexicalSearchResult](stored)
		if !ok {
			break
		}
		return countSummary("Found", len(value.Matches), "knowledge result")
	case "get", "chunk_get":
		value, ok := decodeKnowledgeResult[recordResult](stored)
		if !ok {
			break
		}
		title := ""
		if value.Chunk != nil {
			title = value.Chunk.Title
		}
		if value.Entry != nil {
			title = value.Entry.Title
		}
		return titledSummary("Read knowledge "+string(value.Kind), title)
	case "neighbors":
		value, ok := decodeKnowledgeResult[neighborPageResult](stored)
		if !ok {
			break
		}
		return countSummary("Found", len(value.Neighbors), "linked object")
	case "chunk_list":
		value, ok := decodeKnowledgeResult[chunkPageResult](stored)
		if !ok {
			break
		}
		return countSummary("Listed", len(value.Chunks), "knowledge chunk")
	case "chunk_create", "chunk_update", "chunk_archive", "chunk_restore":
		value, ok := decodeKnowledgeResult[chunkMutationResult](stored)
		if !ok {
			break
		}
		return titledSummary(pastAction(action, "knowledge chunk"), value.Chunk.Title)
	case "chunk_delete":
		return "Deleted knowledge chunk"
	case "entry_create", "entry_update", "entry_supersede", "entry_archive", "entry_restore", "verify":
		value, ok := decodeKnowledgeResult[entryMutationResult](stored)
		if !ok {
			break
		}
		return titledSummary(pastAction(action, "knowledge entry"), value.Entry.Title)
	case "entry_delete":
		return "Deleted knowledge entry"
	case "link", "unlink":
		return pastAction(action, "knowledge link")
	case "history":
		value, ok := decodeKnowledgeResult[historyPageResult](stored)
		if !ok {
			break
		}
		return countSummary("Loaded", len(value.Revisions), "knowledge revision")
	}
	return knowledgeActionTitle(action)
}

func decodeKnowledgeResult[T any](stored any) (T, bool) {
	var value T
	data, err := json.Marshal(stored)
	if err != nil || json.Unmarshal(data, &value) != nil {
		return value, false
	}
	return value, true
}

func pastAction(action, object string) string {
	verb := map[string]string{
		"chunk_create":    "Created",
		"chunk_update":    "Updated",
		"chunk_archive":   "Archived",
		"chunk_restore":   "Restored",
		"entry_create":    "Created",
		"entry_update":    "Updated",
		"entry_supersede": "Superseded",
		"entry_archive":   "Archived",
		"entry_restore":   "Restored",
		"verify":          "Verified",
		"link":            "Linked",
		"unlink":          "Unlinked",
	}[action]
	if verb == "" {
		return knowledgeActionTitle(action)
	}
	return verb + " " + object
}

func titledSummary(summary, title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return summary
	}
	return summary + ": " + title
}

func countSummary(verb string, count int, singular string) string {
	noun := singular
	if count != 1 {
		noun += "s"
	}
	return fmt.Sprintf("%s %d %s", verb, count, noun)
}

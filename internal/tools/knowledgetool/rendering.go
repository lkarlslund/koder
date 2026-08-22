package knowledgetool

import (
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
	switch value := stored.(type) {
	case knowledgeService.LexicalSearchResult:
		return countSummary("Found", len(value.Matches), "knowledge result")
	case recordResult:
		title := ""
		if value.Chunk != nil {
			title = value.Chunk.Title
		}
		if value.Entry != nil {
			title = value.Entry.Title
		}
		return titledSummary("Read knowledge "+string(value.Kind), title)
	case neighborPageResult:
		return countSummary("Found", len(value.Neighbors), "linked object")
	case chunkPageResult:
		return countSummary("Listed", len(value.Chunks), "knowledge chunk")
	case chunkMutationResult:
		return titledSummary(pastAction(action, "knowledge chunk"), value.Chunk.Title)
	case chunkDeleteResult:
		return "Deleted knowledge chunk"
	case entryMutationResult:
		return titledSummary(pastAction(action, "knowledge entry"), value.Entry.Title)
	case entryDeleteResult:
		return "Deleted knowledge entry"
	case linkMutationResult:
		return pastAction(action, "knowledge link")
	case historyPageResult:
		return countSummary("Loaded", len(value.Revisions), "knowledge revision")
	default:
		return knowledgeActionTitle(action)
	}
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

package memorytool

import (
	"encoding/json"
	"fmt"
	"strings"

	memoryService "github.com/lkarlslund/koder/internal/memory/service"
	"github.com/lkarlslund/koder/internal/tools"
)

func (tool) Presentation(req tools.Request) tools.Presentation {
	preview := tool{}.Preview(req)
	return tools.Presentation{
		Title:    memoryActionTitle(req.Args["action"]),
		Subtitle: preview,
		Preview:  preview,
	}
}

// SummarizeResult gives clients a compact, human-readable label while leaving
// the complete structured result available to the model and transcript.
func (tool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return memoryResultSummary(req, result.Stored), result.Output
}

func memoryActionTitle(action string) string {
	switch strings.TrimSpace(action) {
	case "recall":
		return "Recall memory"
	case "remember":
		return "Remember memory"
	case "search":
		return "Search memory"
	case "get":
		return "Get memory"
	case "neighbors":
		return "Explore memory links"
	case "chunk_list":
		return "List memory chunks"
	case "chunk_get":
		return "Get memory chunk"
	case "chunk_create":
		return "Create memory chunk"
	case "chunk_update":
		return "Update memory chunk"
	case "chunk_archive":
		return "Archive memory chunk"
	case "chunk_restore":
		return "Restore memory chunk"
	case "chunk_delete":
		return "Delete memory chunk"
	case "entry_create":
		return "Create memory entry"
	case "entry_update":
		return "Update memory entry"
	case "entry_supersede":
		return "Supersede memory entry"
	case "entry_archive":
		return "Archive memory entry"
	case "entry_restore":
		return "Restore memory entry"
	case "entry_delete":
		return "Delete memory entry"
	case "link":
		return "Link memory"
	case "unlink":
		return "Unlink memory"
	case "verify":
		return "Verify memory entry"
	case "history":
		return "Review memory history"
	case "package_preview":
		return "Preview memory package"
	case "package_stage":
		return "Stage memory package"
	case "package_activate":
		return "Activate memory package"
	case "package_discard":
		return "Discard memory package"
	case "package_export":
		return "Export memory package"
	default:
		return "Use memory"
	}
}

func memoryResultSummary(req tools.Request, stored any) string {
	action := strings.TrimSpace(req.Args["action"])
	switch action {
	case "recall", "search":
		value, ok := decodeMemoryResult[memoryService.LexicalSearchResult](stored)
		if !ok {
			break
		}
		return countSummary("Found", len(value.Matches), "memory result")
	case "remember":
		value, ok := decodeMemoryResult[rememberResult](stored)
		if !ok {
			break
		}
		if value.Duplicate {
			return titledSummary("Already remembered", value.Entry.Title)
		}
		return titledSummary("Remembered", value.Entry.Title)
	case "get", "chunk_get":
		value, ok := decodeMemoryResult[recordResult](stored)
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
		return titledSummary("Read memory "+string(value.Kind), title)
	case "neighbors":
		value, ok := decodeMemoryResult[neighborPageResult](stored)
		if !ok {
			break
		}
		return countSummary("Found", len(value.Neighbors), "linked object")
	case "chunk_list":
		value, ok := decodeMemoryResult[chunkPageResult](stored)
		if !ok {
			break
		}
		return countSummary("Listed", len(value.Chunks), "memory chunk")
	case "chunk_create", "chunk_update", "chunk_archive", "chunk_restore":
		value, ok := decodeMemoryResult[chunkMutationResult](stored)
		if !ok {
			break
		}
		return titledSummary(pastAction(action, "memory chunk"), value.Chunk.Title)
	case "chunk_delete":
		return "Deleted memory chunk"
	case "entry_create", "entry_update", "entry_supersede", "entry_archive", "entry_restore", "verify":
		value, ok := decodeMemoryResult[entryMutationResult](stored)
		if !ok {
			break
		}
		return titledSummary(pastAction(action, "memory entry"), value.Entry.Title)
	case "entry_delete":
		return "Deleted memory entry"
	case "link", "unlink":
		return pastAction(action, "memory link")
	case "history":
		value, ok := decodeMemoryResult[historyPageResult](stored)
		if !ok {
			break
		}
		return countSummary("Loaded", len(value.Revisions), "memory revision")
	case "package_preview":
		return "Previewed memory package"
	case "package_stage":
		return "Staged memory package"
	case "package_activate":
		return "Activated memory package"
	case "package_discard":
		return "Discarded memory package stage"
	case "package_export":
		value, ok := decodeMemoryResult[packageExportResult](stored)
		if ok {
			return titledSummary("Exported memory package", value.Path)
		}
	}
	return memoryActionTitle(action)
}

func decodeMemoryResult[T any](stored any) (T, bool) {
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
		return memoryActionTitle(action)
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

package memorytool

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/lkarlslund/koder/internal/memory"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
	"github.com/lkarlslund/koder/internal/tools"
)

const (
	maxMemoryTitleRunes  = 80
	maxMemoryContentSize = 64 << 10
)

type rememberResult struct {
	Created   bool                               `json:"created"`
	Duplicate bool                               `json:"duplicate,omitempty"`
	Entry     memory.Entry                       `json:"entry"`
	Matches   []memoryService.LexicalSearchMatch `json:"matches,omitempty"`
}

func normalizeRecallArgs(args map[string]string) (map[string]string, error) {
	query := strings.TrimSpace(args["query"])
	if query == "" {
		return nil, errors.New("query is required for memory recall")
	}
	if len(query) > 4<<10 {
		return nil, errors.New("memory recall query exceeds 4 KiB")
	}
	return map[string]string{"action": "recall", "query": query}, nil
}

func normalizeRememberArgs(args map[string]string) (map[string]string, error) {
	content := strings.TrimSpace(args["content"])
	if content == "" {
		return nil, errors.New("content is required to remember memory")
	}
	if len(content) > maxMemoryContentSize {
		return nil, errors.New("memory content exceeds 64 KiB")
	}
	out := map[string]string{"action": "remember", "content": content}
	if title := strings.TrimSpace(args["title"]); title != "" {
		if len([]rune(title)) > 240 {
			return nil, errors.New("memory title exceeds 240 characters")
		}
		out["title"] = title
	}
	if err := normalizeBool(args, out, "personal"); err != nil {
		return nil, err
	}
	if raw := strings.TrimSpace(args["kind"]); raw != "" {
		kind, err := memory.EntryKindString(raw)
		if err != nil || kind == memory.EntryKindUnspecified {
			return nil, fmt.Errorf("invalid memory kind %q", raw)
		}
		out["kind"] = kind.String()
	}
	if raw := strings.TrimSpace(args["scope"]); raw != "" {
		scope, err := memory.ScopeKindString(raw)
		if err != nil || scope == memory.ScopeKindUnspecified {
			return nil, fmt.Errorf("invalid memory scope %q", raw)
		}
		out["scope"] = scope.String()
	}
	if selector := strings.TrimSpace(args["scope_selector"]); selector != "" {
		if len([]rune(selector)) > 240 {
			return nil, errors.New("memory scope selector exceeds 240 characters")
		}
		out["scope_selector"] = selector
	}
	if raw := strings.TrimSpace(args["personal_origin"]); raw != "" {
		origin, err := memory.PersonalOriginString(raw)
		if err != nil || origin == memory.PersonalOriginUnspecified {
			return nil, fmt.Errorf("invalid personal memory origin %q", raw)
		}
		out["personal_origin"] = origin.String()
	}
	return out, nil
}

func memoryScope(runtime tools.Runtime, args map[string]string) (memory.Scope, error) {
	kind := memory.ScopeKindUnspecified
	if raw := strings.TrimSpace(args["scope"]); raw != "" {
		parsed, err := memory.ScopeKindString(raw)
		if err != nil || parsed == memory.ScopeKindUnspecified {
			return memory.Scope{}, fmt.Errorf("invalid memory scope %q", raw)
		}
		kind = parsed
	}
	if kind == memory.ScopeKindUnspecified {
		if boolArg(args, "personal") || args["kind"] == memory.EntryKindPreference.String() || strings.TrimSpace(args["personal_origin"]) != "" {
			kind = memory.ScopeKindPersonal
		} else if strings.TrimSpace(runtime.Workdir) != "" {
			kind = memory.ScopeKindProject
		} else {
			kind = memory.ScopeKindGlobal
		}
	}
	selector := strings.TrimSpace(args["scope_selector"])
	switch kind {
	case memory.ScopeKindGlobal:
		selector = ""
	case memory.ScopeKindPersonal:
		if selector == "" {
			selector = "me"
		}
	case memory.ScopeKindProject:
		if selector == "" && strings.TrimSpace(runtime.Workdir) != "" {
			selector = filepath.Clean(runtime.Workdir)
		}
	case memory.ScopeKindSession:
		if selector == "" {
			selector = string(runtime.SessionID)
		}
	}
	scope := memory.Scope{Kind: kind, Selector: selector}
	if err := scope.Validate(); err != nil {
		return memory.Scope{}, err
	}
	return scope, nil
}

func callRemember(ctx context.Context, service *memoryService.Service, runtime tools.Runtime, args map[string]string) (rememberResult, error) {
	scope, err := memoryScope(runtime, args)
	if err != nil {
		return rememberResult{}, err
	}
	kind := memory.EntryKindFact
	if raw := strings.TrimSpace(args["kind"]); raw != "" {
		kind, err = memory.EntryKindString(raw)
		if err != nil {
			return rememberResult{}, err
		}
	}
	content := strings.TrimSpace(args["content"])
	title := strings.TrimSpace(args["title"])
	if title == "" {
		title = memoryTitle(content)
	}
	origin := memory.PersonalOriginUnspecified
	if scope.Kind == memory.ScopeKindPersonal {
		origin = memory.PersonalOriginExplicit
		if raw := strings.TrimSpace(args["personal_origin"]); raw != "" {
			origin, err = memory.PersonalOriginString(raw)
			if err != nil {
				return rememberResult{}, err
			}
		}
	}

	search, err := service.SearchLexical(ctx, memoryService.LexicalSearchRequest{
		Query: title + " " + content, Limit: 10, Scopes: []memory.Scope{scope},
	})
	if err != nil {
		return rememberResult{}, err
	}
	for _, match := range search.Matches {
		if sameMemory(match.Document, title, content) {
			existing, entryErr := service.Entry(ctx, match.EntryID)
			if entryErr != nil {
				return rememberResult{}, entryErr
			}
			return rememberResult{
				Duplicate: true,
				Entry:     existing,
				Matches:   search.Matches,
			}, nil
		}
	}

	var chunkID memory.ChunkID
	if scope.Kind == memory.ScopeKindPersonal {
		ensured, ensureErr := service.EnsurePersonalChunk(ctx)
		if ensureErr != nil {
			return rememberResult{}, ensureErr
		}
		chunkID = ensured.Chunk.ID
	} else {
		ensured, ensureErr := service.EnsureCuratedLearningChunk(ctx)
		if ensureErr != nil {
			return rememberResult{}, ensureErr
		}
		chunkID = ensured.Chunk.ID
	}
	created, err := service.CreateEntry(ctx, memoryService.CreateEntryRequest{
		ChunkID: chunkID,
		Entry: memory.Entry{
			Kind: kind, Title: title, Summary: content, Body: content, Scope: scope, PersonalOrigin: origin,
		},
	})
	if err != nil {
		return rememberResult{}, err
	}
	return rememberResult{Created: true, Entry: created.Entry, Matches: search.Matches}, nil
}

func sameMemory(document memoryService.SearchDocument, title, content string) bool {
	return strings.EqualFold(strings.TrimSpace(document.Title), title) &&
		strings.EqualFold(strings.TrimSpace(document.Summary), content)
}

func callRecall(ctx context.Context, service *memoryService.Service, offer memoryService.ToolOffer, runtime tools.Runtime, query string) (memoryService.LexicalSearchResult, error) {
	scopes := make([]memory.Scope, 0, 4)
	if slices.Contains(offer.ScopeKinds, memory.ScopeKindGlobal) {
		scopes = append(scopes, memory.Scope{Kind: memory.ScopeKindGlobal})
	}
	if slices.Contains(offer.ScopeKinds, memory.ScopeKindPersonal) {
		scopes = append(scopes, memory.Scope{Kind: memory.ScopeKindPersonal, Selector: "me"})
	}
	if workdir := strings.TrimSpace(runtime.Workdir); workdir != "" && slices.Contains(offer.ScopeKinds, memory.ScopeKindProject) {
		scopes = append(scopes, memory.Scope{Kind: memory.ScopeKindProject, Selector: filepath.Clean(workdir)})
	}
	if runtime.SessionID != "" && slices.Contains(offer.ScopeKinds, memory.ScopeKindSession) {
		scopes = append(scopes, memory.Scope{Kind: memory.ScopeKindSession, Selector: string(runtime.SessionID)})
	}
	if len(scopes) == 0 {
		return memoryService.LexicalSearchResult{Matches: []memoryService.LexicalSearchMatch{}}, nil
	}
	return service.SearchLexical(ctx, memoryService.LexicalSearchRequest{Query: query, Limit: 10, Scopes: scopes})
}

func memoryTitle(content string) string {
	text := strings.Join(strings.Fields(content), " ")
	runes := []rune(text)
	end := len(runes)
	for index, value := range runes {
		if index >= maxMemoryTitleRunes {
			end = maxMemoryTitleRunes
			break
		}
		if index >= 24 && (value == '.' || value == '!' || value == '?') {
			end = index + 1
			break
		}
	}
	title := strings.TrimSpace(string(runes[:end]))
	if end < len(runes) {
		title = strings.TrimRightFunc(title, unicode.IsPunct) + "…"
	}
	return title
}

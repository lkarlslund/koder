package knowledgetool

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
	"github.com/lkarlslund/koder/internal/tools"
)

const (
	maxMemoryTitleRunes  = 80
	maxMemoryContentSize = 64 << 10
)

type rememberResult struct {
	Created   bool                                  `json:"created"`
	Duplicate bool                                  `json:"duplicate,omitempty"`
	Entry     knowledge.Entry                       `json:"entry"`
	Matches   []knowledgeService.LexicalSearchMatch `json:"matches,omitempty"`
}

func normalizeRecallArgs(args map[string]string) (map[string]string, error) {
	query := strings.TrimSpace(args["query"])
	if query == "" {
		return nil, errors.New("query is required for knowledge recall")
	}
	if len(query) > 4<<10 {
		return nil, errors.New("knowledge recall query exceeds 4 KiB")
	}
	return map[string]string{"action": "recall", "query": query}, nil
}

func normalizeRememberArgs(args map[string]string) (map[string]string, error) {
	content := strings.TrimSpace(args["content"])
	if content == "" {
		return nil, errors.New("content is required to remember knowledge")
	}
	if len(content) > maxMemoryContentSize {
		return nil, errors.New("knowledge memory content exceeds 64 KiB")
	}
	out := map[string]string{"action": "remember", "content": content}
	if title := strings.TrimSpace(args["title"]); title != "" {
		if len([]rune(title)) > 240 {
			return nil, errors.New("knowledge memory title exceeds 240 characters")
		}
		out["title"] = title
	}
	if err := normalizeBool(args, out, "personal"); err != nil {
		return nil, err
	}
	if raw := strings.TrimSpace(args["kind"]); raw != "" {
		kind, err := knowledge.EntryKindString(raw)
		if err != nil || kind == knowledge.EntryKindUnspecified {
			return nil, fmt.Errorf("invalid knowledge memory kind %q", raw)
		}
		out["kind"] = kind.String()
	}
	if raw := strings.TrimSpace(args["scope"]); raw != "" {
		scope, err := knowledge.ScopeKindString(raw)
		if err != nil || scope == knowledge.ScopeKindUnspecified {
			return nil, fmt.Errorf("invalid knowledge memory scope %q", raw)
		}
		out["scope"] = scope.String()
	}
	if selector := strings.TrimSpace(args["scope_selector"]); selector != "" {
		if len([]rune(selector)) > 240 {
			return nil, errors.New("knowledge memory scope selector exceeds 240 characters")
		}
		out["scope_selector"] = selector
	}
	if raw := strings.TrimSpace(args["personal_origin"]); raw != "" {
		origin, err := knowledge.PersonalOriginString(raw)
		if err != nil || origin == knowledge.PersonalOriginUnspecified {
			return nil, fmt.Errorf("invalid personal memory origin %q", raw)
		}
		out["personal_origin"] = origin.String()
	}
	return out, nil
}

func memoryScope(runtime tools.Runtime, args map[string]string) (knowledge.Scope, error) {
	kind := knowledge.ScopeKindUnspecified
	if raw := strings.TrimSpace(args["scope"]); raw != "" {
		parsed, err := knowledge.ScopeKindString(raw)
		if err != nil || parsed == knowledge.ScopeKindUnspecified {
			return knowledge.Scope{}, fmt.Errorf("invalid knowledge memory scope %q", raw)
		}
		kind = parsed
	}
	if kind == knowledge.ScopeKindUnspecified {
		if boolArg(args, "personal") || args["kind"] == knowledge.EntryKindPreference.String() || strings.TrimSpace(args["personal_origin"]) != "" {
			kind = knowledge.ScopeKindPersonal
		} else if strings.TrimSpace(runtime.Workdir) != "" {
			kind = knowledge.ScopeKindProject
		} else {
			kind = knowledge.ScopeKindGlobal
		}
	}
	selector := strings.TrimSpace(args["scope_selector"])
	switch kind {
	case knowledge.ScopeKindGlobal:
		selector = ""
	case knowledge.ScopeKindPersonal:
		if selector == "" {
			selector = "me"
		}
	case knowledge.ScopeKindProject:
		if selector == "" && strings.TrimSpace(runtime.Workdir) != "" {
			selector = filepath.Clean(runtime.Workdir)
		}
	case knowledge.ScopeKindSession:
		if selector == "" {
			selector = string(runtime.SessionID)
		}
	}
	scope := knowledge.Scope{Kind: kind, Selector: selector}
	if err := scope.Validate(); err != nil {
		return knowledge.Scope{}, err
	}
	return scope, nil
}

func callRemember(ctx context.Context, service *knowledgeService.Service, runtime tools.Runtime, args map[string]string) (rememberResult, error) {
	scope, err := memoryScope(runtime, args)
	if err != nil {
		return rememberResult{}, err
	}
	kind := knowledge.EntryKindFact
	if raw := strings.TrimSpace(args["kind"]); raw != "" {
		kind, err = knowledge.EntryKindString(raw)
		if err != nil {
			return rememberResult{}, err
		}
	}
	content := strings.TrimSpace(args["content"])
	title := strings.TrimSpace(args["title"])
	if title == "" {
		title = memoryTitle(content)
	}
	origin := knowledge.PersonalOriginUnspecified
	if scope.Kind == knowledge.ScopeKindPersonal {
		origin = knowledge.PersonalOriginExplicit
		if raw := strings.TrimSpace(args["personal_origin"]); raw != "" {
			origin, err = knowledge.PersonalOriginString(raw)
			if err != nil {
				return rememberResult{}, err
			}
		}
	}

	search, err := service.SearchLexical(ctx, knowledgeService.LexicalSearchRequest{
		Query: title + " " + content, Limit: 10, Scopes: []knowledge.Scope{scope},
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

	chunkID := knowledgeService.CuratedLearningChunkID
	if scope.Kind == knowledge.ScopeKindPersonal {
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
	created, err := service.CreateEntry(ctx, knowledgeService.CreateEntryRequest{
		ChunkID: chunkID,
		Entry: knowledge.Entry{
			Kind: kind, Title: title, Summary: content, Body: content, Scope: scope, PersonalOrigin: origin,
		},
	})
	if err != nil {
		return rememberResult{}, err
	}
	return rememberResult{Created: true, Entry: created.Entry, Matches: search.Matches}, nil
}

func sameMemory(document knowledgeService.SearchDocument, title, content string) bool {
	return strings.EqualFold(strings.TrimSpace(document.Title), title) &&
		strings.EqualFold(strings.TrimSpace(document.Summary), content)
}

func callRecall(ctx context.Context, service *knowledgeService.Service, offer knowledgeService.ToolOffer, runtime tools.Runtime, query string) (knowledgeService.LexicalSearchResult, error) {
	scopes := make([]knowledge.Scope, 0, 4)
	if slices.Contains(offer.ScopeKinds, knowledge.ScopeKindGlobal) {
		scopes = append(scopes, knowledge.Scope{Kind: knowledge.ScopeKindGlobal})
	}
	if slices.Contains(offer.ScopeKinds, knowledge.ScopeKindPersonal) {
		scopes = append(scopes, knowledge.Scope{Kind: knowledge.ScopeKindPersonal, Selector: "me"})
	}
	if workdir := strings.TrimSpace(runtime.Workdir); workdir != "" && slices.Contains(offer.ScopeKinds, knowledge.ScopeKindProject) {
		scopes = append(scopes, knowledge.Scope{Kind: knowledge.ScopeKindProject, Selector: filepath.Clean(workdir)})
	}
	if runtime.SessionID != "" && slices.Contains(offer.ScopeKinds, knowledge.ScopeKindSession) {
		scopes = append(scopes, knowledge.Scope{Kind: knowledge.ScopeKindSession, Selector: string(runtime.SessionID)})
	}
	if len(scopes) == 0 {
		return knowledgeService.LexicalSearchResult{Matches: []knowledgeService.LexicalSearchMatch{}}, nil
	}
	return service.SearchLexical(ctx, knowledgeService.LexicalSearchRequest{Query: query, Limit: 10, Scopes: scopes})
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

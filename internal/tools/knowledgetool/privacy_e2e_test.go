package knowledgetool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	knowledgePebble "github.com/lkarlslund/koder/internal/knowledge/store/pebble"
	"github.com/lkarlslund/koder/internal/tools"
)

func TestKnowledgePersonalSecretAndCrossScopeNonDisclosureVertical(t *testing.T) {
	ctx := context.Background()
	store, err := knowledgePebble.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service, err := knowledgeService.New(knowledgeService.Config{
		Store: store,
		Actor: knowledgeService.ContextActorSource(knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "system:privacy-e2e"}),
		ToolPolicy: knowledgeService.ToolOfferPolicyFunc(func(_ context.Context, _ knowledge.Actor, offer knowledgeService.ToolOffer) (knowledgeService.ToolOffer, error) {
			offer.ScopeKinds = []knowledge.ScopeKind{knowledge.ScopeKindProject}
			return offer, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	personal, err := service.EnsurePersonalChunk(ctx)
	if err != nil {
		t.Fatal(err)
	}
	personalEntry, err := service.CreateEntry(ctx, knowledgeService.CreateEntryRequest{
		ChunkID: personal.Chunk.ID,
		Entry: knowledge.Entry{
			Kind: knowledge.EntryKindPreference, Title: "privacycanary personal preference",
			Summary: "The user explicitly prefers cyan interfaces.", PersonalOrigin: knowledge.PersonalOriginExplicit,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	inferred, err := service.CreateEntry(ctx, knowledgeService.CreateEntryRequest{
		ChunkID: personal.Chunk.ID, ReviewApproved: true,
		Entry: knowledge.Entry{
			Kind: knowledge.EntryKindPreference, Title: "privacycanary possible medical preference",
			Summary: "The user may prefer a particular medicine.", PersonalOrigin: knowledge.PersonalOriginInferred,
			Confidence: 0.5, Risk: []knowledge.RiskClass{knowledge.RiskClassMedical},
		},
	})
	if err != nil || inferred.Entry.State != knowledge.EntryStateDraft {
		t.Fatalf("sensitive personal inference = %#v, %v", inferred, err)
	}

	global, err := service.CreateChunk(ctx, knowledgeService.CreateChunkRequest{Chunk: knowledge.Chunk{
		Title: "privacycanary global", Kind: knowledge.ChunkKindReference,
		Scope: knowledge.Scope{Kind: knowledge.ScopeKindGlobal},
	}})
	if err != nil {
		t.Fatal(err)
	}
	globalEntry, err := service.CreateEntry(ctx, knowledgeService.CreateEntryRequest{
		ChunkID: global.Chunk.ID,
		Entry:   knowledge.Entry{Kind: knowledge.EntryKindFact, Title: "privacycanary global fact", Summary: "hidden-global-summary"},
	})
	if err != nil {
		t.Fatal(err)
	}
	project, err := service.CreateChunk(ctx, knowledgeService.CreateChunkRequest{Chunk: knowledge.Chunk{
		Title: "privacycanary project", Kind: knowledge.ChunkKindProject,
		Scope: knowledge.Scope{Kind: knowledge.ScopeKindProject, Selector: "project:visible"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	projectEntry, err := service.CreateEntry(ctx, knowledgeService.CreateEntryRequest{
		ChunkID: project.Chunk.ID,
		Entry:   knowledge.Entry{Kind: knowledge.EntryKindFact, Title: "privacycanary project fact", Summary: "visible-project-summary"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateLink(ctx, knowledgeService.CreateLinkRequest{Link: knowledge.Link{
		Source: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(projectEntry.Entry.ID)},
		Target: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(globalEntry.Entry.ID)},
		Kind:   knowledge.LinkKindRelatedTo, Label: "hidden-cross-scope-edge",
	}}); err != nil {
		t.Fatal(err)
	}

	runtime := runtimeFor(service)
	definition, enabled := tools.DefinitionFor(tools.Knowledge, runtime)
	if !enabled || !strings.Contains(definition.Function.Description, "permitted scopes: project") {
		t.Fatalf("project-scoped Knowledge definition = enabled:%t description:%q", enabled, definition.Function.Description)
	}
	for _, hidden := range []string{personalEntry.Entry.Title, globalEntry.Entry.Summary, "hidden-cross-scope-edge"} {
		if strings.Contains(definition.Function.Description, hidden) {
			t.Fatalf("tool definition leaked hidden value %q", hidden)
		}
	}

	searchCall, err := call(ctx, runtime, map[string]string{
		"action": "search", "query": "privacycanary", "expand_graph": "true", "limit": "20",
	})
	if err != nil {
		t.Fatal(err)
	}
	search := searchCall.Stored.(knowledgeService.LexicalSearchResult)
	if len(search.Matches) != 1 || search.Matches[0].EntryID != projectEntry.Entry.ID || search.CorpusDocumentCount != 1 {
		t.Fatalf("project-scoped graph search = %#v", search)
	}
	if containsAny(searchCall.Output, personalEntry.Entry.Title, globalEntry.Entry.Title, globalEntry.Entry.Summary, "hidden-cross-scope-edge") {
		t.Fatalf("project-scoped graph search leaked hidden data: %s", searchCall.Output)
	}
	listedCall, err := call(ctx, runtime, map[string]string{"action": "chunk_list", "states": `["active"]`, "limit": "20"})
	if err != nil {
		t.Fatal(err)
	}
	listed := listedCall.Stored.(chunkPageResult)
	if len(listed.Chunks) != 1 || listed.Chunks[0].ID != project.Chunk.ID {
		t.Fatalf("project-scoped chunks = %#v", listed)
	}
	neighborsCall, err := call(ctx, runtime, map[string]string{
		"action": "neighbors", "object_kind": "entry", "id": string(projectEntry.Entry.ID), "limit": "20",
	})
	if err != nil {
		t.Fatalf("cross-scope neighbors should be indistinguishable from no neighbors: %v", err)
	}
	neighbors := neighborsCall.Stored.(neighborPageResult)
	if len(neighbors.Neighbors) != 0 || neighbors.NextCursor != "" || containsAny(neighborsCall.Output, "hidden-cross-scope-edge", globalEntry.Entry.Title) {
		t.Fatalf("cross-scope neighbor leaked degree or content: stored=%#v output=%s", neighbors, neighborsCall.Output)
	}

	for _, hidden := range []struct {
		kind knowledge.ObjectKind
		id   string
		text string
	}{
		{knowledge.ObjectKindEntry, string(personalEntry.Entry.ID), personalEntry.Entry.Title},
		{knowledge.ObjectKindEntry, string(globalEntry.Entry.ID), globalEntry.Entry.Summary},
		{knowledge.ObjectKindChunk, string(personal.Chunk.ID), personal.Chunk.Title},
	} {
		_, err := call(ctx, runtime, map[string]string{"action": "get", "object_kind": hidden.kind.String(), "id": hidden.id})
		var denied *knowledgeService.ServiceError
		if !errors.As(err, &denied) || denied.Code != knowledgeService.ErrorCodeForbidden || strings.Contains(err.Error(), hidden.text) {
			t.Fatalf("hidden %s get = %T %v", hidden.kind, err, err)
		}
	}

	const secret = "privacy-secret-7f45f6c1"
	entryJSON, err := json.Marshal(map[string]any{
		"kind": "fact", "title": "Rejected credential", "body": "password=" + secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, secretErr := call(ctx, runtime, map[string]string{
		"action": "entry_create", "chunk_id": string(project.Chunk.ID), "entry": string(entryJSON),
	})
	if secretErr == nil || strings.Contains(secretErr.Error(), secret) {
		t.Fatalf("secret write error = %v", secretErr)
	}
	secretSearch, err := call(ctx, runtime, map[string]string{"action": "search", "query": secret, "limit": "20"})
	if err != nil {
		t.Fatal(err)
	}
	secretResult := secretSearch.Stored.(knowledgeService.LexicalSearchResult)
	if len(secretResult.Matches) != 0 {
		t.Fatalf("rejected secret became retrievable: stored=%#v output=%s", secretResult, secretSearch.Output)
	}
	stats, err := store.ScanCanonical(ctx, func(knowledgeStore.CanonicalRecord) error { return nil })
	if err != nil || stats.Entries != 4 {
		t.Fatalf("canonical stats after rejected secret = %#v, %v", stats, err)
	}
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if candidate != "" && strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

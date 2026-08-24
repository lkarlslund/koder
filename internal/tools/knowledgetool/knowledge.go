// Package knowledgetool exposes Koder's durable knowledge graph through one
// model-facing, multi-action tool.
package knowledgetool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/tools"
)

var (
	supportedActions = []string{
		"search", "get", "neighbors",
		"chunk_list", "chunk_get", "chunk_create", "chunk_update", "chunk_archive", "chunk_restore", "chunk_delete",
		"entry_create", "entry_update", "entry_supersede", "entry_archive", "entry_restore", "entry_delete",
		"link", "unlink", "verify", "history",
	}
	packageActions      = []string{"package_preview", "package_stage", "package_activate", "package_discard", "package_export"}
	supportedScopeKinds = []knowledge.ScopeKind{
		knowledge.ScopeKindGlobal, knowledge.ScopeKindPersonal, knowledge.ScopeKindProject,
		knowledge.ScopeKindSession, knowledge.ScopeKindEnvironment,
	}
)

const (
	serviceKey = "knowledge"
	parameters = `{
  "type":"object",
  "properties":{
    "action":{"type":"string","enum":["recall","remember"]},
    "query":{"type":"string","description":"Natural-language terms to recall from durable memory"},
    "title":{"type":"string","description":"Short title for one durable memory"},
    "content":{"type":"string","description":"Concise, self-contained fact, preference, decision, warning, or procedure to remember"},
    "personal":{"type":"boolean","description":"Set true only when the memory is about the user, such as their stated preference"}
  },
  "required":["action"],
  "additionalProperties":false
}`
)

// RuntimeService makes an available Knowledge service visible to a chat tool
// runtime. A nil service intentionally contributes no capability.
func RuntimeService(service *knowledgeService.Service) map[string]any {
	if service == nil {
		return nil
	}
	return map[string]any{serviceKey: service}
}

func init() {
	tools.Register(tool{}, tools.ToolSpec{
		Title:       "Knowledge",
		Description: "Recall or save Koder's durable memory across chats.",
		Usage:       "Use recall with a query when prior durable memory may help. Use remember with content for one established reusable conclusion or an explicit request to remember; title is optional. Set personal=true only for a direct fact or preference about the user. Koder infers all storage metadata. Never store routine progress, guesses, transcript copies, passwords, tokens, keys, cookies, recovery codes, or other credentials.",
		Parameters:  parameters,
		ExposeToLLM: true,
	})
}

type tool struct{}

func (tool) ID() tools.ID             { return tools.Knowledge }
func (tool) BypassesPermission() bool { return false }

func (tool) Definition(runtime tools.Runtime, spec tools.ToolSpec) (tools.ToolSpec, bool) {
	service, err := requireService(runtime)
	if err != nil {
		return tools.ToolSpec{}, false
	}
	ctx := context.Background()
	if runtime.ChatID != "" {
		ctx, err = knowledgeService.WithActor(ctx, knowledge.Actor{Kind: knowledge.ActorKindChat, ID: string(runtime.ChatID)})
		if err != nil {
			return tools.ToolSpec{}, false
		}
	}
	underlying, err := service.FilterToolOffer(ctx, candidateToolOffer(runtime))
	if err != nil || len(underlying.ScopeKinds) == 0 {
		return tools.ToolSpec{}, false
	}
	offer := modelToolOffer(underlying)
	if len(offer.Actions) == 0 {
		return tools.ToolSpec{}, false
	}
	spec.Parameters, err = filterToolSchema(spec.Parameters, offer)
	if err != nil {
		return tools.ToolSpec{}, false
	}
	actions, scopes := strings.Join(offer.Actions, ", "), strings.Join(scopeKindStrings(offer.ScopeKinds), ", ")
	policyDescription := " Runtime policy permits actions: " + actions + "; permitted scopes: " + scopes + "."
	spec.Description += policyDescription
	spec.Usage += " " + toolGuidance(offer) + policyDescription
	if runtime.VoiceInteraction() {
		spec.Usage += " Voice result: tell the user only the few relevant conclusions in natural spoken sentences. Do not recite IDs, scores, JSON, metadata, or result counts unless asked. Preserve contradictions, uncertainty, and any action the user must take; the complete structured result remains available in the transcript, and deliberately visual detail belongs in a presentation."
	}
	return spec, true
}

func (tool) Preview(req tools.Request) string {
	action := strings.TrimSpace(req.Args["action"])
	switch action {
	case "recall":
		return "Recall knowledge about " + strings.TrimSpace(req.Args["query"])
	case "remember":
		if title := strings.TrimSpace(req.Args["title"]); title != "" {
			return "Remember " + title
		}
		return "Remember durable knowledge"
	case "search":
		return "Search knowledge for " + strings.TrimSpace(req.Args["query"])
	case "get", "neighbors":
		return "Knowledge " + action + " " + strings.TrimSpace(req.Args["id"])
	case "chunk_list":
		return "List knowledge chunks"
	case "chunk_create":
		return "Create knowledge chunk"
	case "chunk_get", "chunk_update", "chunk_archive", "chunk_restore", "chunk_delete":
		return "Knowledge " + strings.TrimPrefix(action, "chunk_") + " chunk " + strings.TrimSpace(req.Args["id"])
	case "entry_create":
		return "Create knowledge entry"
	case "entry_update", "entry_supersede", "entry_archive", "entry_restore", "entry_delete":
		return "Knowledge " + strings.TrimPrefix(action, "entry_") + " entry " + strings.TrimSpace(req.Args["id"])
	case "link":
		if strings.TrimSpace(req.Args["id"]) != "" {
			return "Restore knowledge link " + strings.TrimSpace(req.Args["id"])
		}
		return "Link knowledge objects"
	case "unlink":
		return "Unlink knowledge relationship " + strings.TrimSpace(req.Args["id"])
	case "verify":
		return "Verify knowledge entry " + strings.TrimSpace(req.Args["id"])
	case "history":
		return "Knowledge history for " + strings.TrimSpace(req.Args["id"])
	case "package_preview":
		return "Preview knowledge package " + strings.TrimSpace(req.Args["path"])
	case "package_stage":
		return "Stage knowledge package " + strings.TrimSpace(req.Args["path"])
	case "package_activate":
		return "Activate knowledge package stage " + strings.TrimSpace(req.Args["stage_id"])
	case "package_discard":
		return "Discard knowledge package stage " + strings.TrimSpace(req.Args["stage_id"])
	case "package_export":
		return "Export knowledge chunk " + strings.TrimSpace(req.Args["id"])
	default:
		return "Knowledge " + action
	}
}

func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	switch action := strings.TrimSpace(args["action"]); action {
	case "recall":
		return normalizeRecallArgs(args)
	case "remember":
		return normalizeRememberArgs(args)
	case "search":
		return normalizeSearchArgs(args)
	case "get":
		return normalizeGetArgs(args)
	case "neighbors":
		return normalizeNeighborArgs(args)
	case "chunk_list":
		return normalizeChunkListArgs(args)
	case "chunk_get":
		return normalizeChunkGetArgs(args)
	case "chunk_create":
		return normalizeChunkCreateArgs(args)
	case "chunk_update":
		return normalizeChunkUpdateArgs(args)
	case "chunk_archive", "chunk_restore":
		return normalizeChunkLifecycleArgs(args, action)
	case "chunk_delete":
		return normalizeChunkDeleteArgs(args)
	case "entry_create":
		return normalizeEntryCreateArgs(args)
	case "entry_update":
		return normalizeEntryUpdateArgs(args)
	case "entry_supersede":
		return normalizeEntrySupersedeArgs(args)
	case "entry_archive", "entry_restore":
		return normalizeEntryLifecycleArgs(args, action)
	case "entry_delete":
		return normalizeEntryDeleteArgs(args)
	case "link":
		return normalizeLinkArgs(args)
	case "unlink":
		return normalizeUnlinkArgs(args)
	case "verify":
		return normalizeVerifyArgs(args)
	case "history":
		return normalizeHistoryArgs(args)
	case "package_preview", "package_stage":
		return normalizePackageReadArgs(args, action)
	case "package_activate", "package_discard":
		return normalizePackageStageArgs(args, action)
	case "package_export":
		return normalizePackageExportArgs(args)
	case "":
		return nil, errors.New("action is required")
	default:
		return nil, fmt.Errorf("unsupported knowledge action %q", action)
	}
}

func (tool) Call(ctx context.Context, options tools.Options) (tools.Result, error) {
	service, err := requireService(options.Runtime)
	if err != nil {
		return tools.Result{}, err
	}
	if options.Runtime.ChatID != "" {
		ctx, err = knowledgeService.WithActor(ctx, knowledge.Actor{
			Kind: knowledge.ActorKindChat,
			ID:   string(options.Runtime.ChatID),
		})
		if err != nil {
			return tools.Result{}, knowledgeService.ClassifyError(err)
		}
	}
	offer, err := service.FilterToolOffer(ctx, candidateToolOffer(options.Runtime))
	if err != nil {
		return tools.Result{}, knowledgeService.ClassifyError(err)
	}
	requestedAction := options.Request.Args["action"]
	policyAction := requestedAction
	switch requestedAction {
	case "recall":
		policyAction = "search"
	case "remember":
		policyAction = "entry_create"
	}
	if len(offer.ScopeKinds) == 0 || !slices.Contains(offer.Actions, policyAction) {
		return tools.Result{}, knowledgeService.ClassifyError(fmt.Errorf("%w: action %s", knowledgeService.ErrToolOfferDenied, requestedAction))
	}
	if requestedAction == "remember" {
		scope, scopeErr := memoryScope(options.Runtime, options.Request.Args)
		if scopeErr != nil {
			return tools.Result{}, scopeErr
		}
		if err := requireAllowedScope(offer, scope); err != nil {
			return tools.Result{}, knowledgeService.ClassifyError(err)
		}
	} else if requestedAction != "recall" {
		if err := authorizeToolScopes(ctx, service, offer, options.Request.Args); err != nil {
			return tools.Result{}, knowledgeService.ClassifyError(err)
		}
	}
	var value any
	switch requestedAction {
	case "recall":
		value, err = callRecall(ctx, service, offer, options.Runtime, options.Request.Args["query"])
	case "remember":
		value, err = callRemember(ctx, service, options.Runtime, options.Request.Args)
	case "search":
		value, err = callSearch(ctx, service, offer, options.Request.Args)
	case "get":
		value, err = callGet(ctx, service, options.Request.Args)
	case "neighbors":
		value, err = callNeighbors(ctx, service, offer, options.Request.Args)
	case "chunk_list":
		value, err = callChunkList(ctx, service, offer, options.Request.Args)
	case "chunk_get":
		value, err = callChunkGet(ctx, service, options.Request.Args)
	case "chunk_create":
		value, err = callChunkCreate(ctx, service, options.Request.Args)
	case "chunk_update":
		value, err = callChunkUpdate(ctx, service, options.Request.Args)
	case "chunk_archive", "chunk_restore":
		value, err = callChunkLifecycle(ctx, service, options.Request.Args)
	case "chunk_delete":
		value, err = callChunkDelete(ctx, service, options.Request.Args)
	case "entry_create":
		value, err = callEntryCreate(ctx, service, options.Request.Args)
	case "entry_update":
		value, err = callEntryUpdate(ctx, service, options.Request.Args)
	case "entry_supersede":
		value, err = callEntrySupersede(ctx, service, options.Request.Args)
	case "entry_archive", "entry_restore":
		value, err = callEntryLifecycle(ctx, service, options.Request.Args)
	case "entry_delete":
		value, err = callEntryDelete(ctx, service, options.Request.Args)
	case "link":
		value, err = callLink(ctx, service, options.Request.Args)
	case "unlink":
		value, err = callUnlink(ctx, service, options.Request.Args)
	case "verify":
		value, err = callVerify(ctx, service, options.Request.Args)
	case "history":
		value, err = callHistory(ctx, service, offer, options.Request.Args)
	case "package_preview", "package_stage":
		value, err = callPackageRead(ctx, service, offer, options.Runtime, options.Request.Args)
	case "package_activate":
		value, err = service.ActivateImport(ctx, options.Request.Args["stage_id"])
	case "package_discard":
		err = service.DiscardImportStage(ctx, options.Request.Args["stage_id"])
		value = packageDiscardResult{StageID: options.Request.Args["stage_id"], Discarded: err == nil}
	case "package_export":
		value, err = callPackageExport(ctx, service, options.Runtime, options.Request.Args)
	default:
		err = fmt.Errorf("unsupported knowledge action %q", requestedAction)
	}
	if err != nil {
		return tools.Result{}, knowledgeService.ClassifyError(err)
	}
	return jsonResult(value)
}

func requireService(runtime tools.Runtime) (*knowledgeService.Service, error) {
	return tools.RequireService[*knowledgeService.Service](runtime, serviceKey)
}

func candidateToolOffer(runtime tools.Runtime) knowledgeService.ToolOffer {
	actions := slices.Clone(supportedActions)
	if runtime.ChatID != "" && strings.TrimSpace(runtime.Workdir) != "" {
		actions = append(actions, packageActions...)
	}
	return knowledgeService.ToolOffer{Actions: actions, ScopeKinds: slices.Clone(supportedScopeKinds)}
}

func modelToolOffer(underlying knowledgeService.ToolOffer) knowledgeService.ToolOffer {
	offer := knowledgeService.ToolOffer{ScopeKinds: slices.Clone(underlying.ScopeKinds)}
	if slices.Contains(underlying.Actions, "search") {
		offer.Actions = append(offer.Actions, "recall")
	}
	if slices.Contains(underlying.Actions, "entry_create") {
		offer.Actions = append(offer.Actions, "remember")
	}
	return offer
}

func scopeKindStrings(values []knowledge.ScopeKind) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value.String())
	}
	return result
}

func filterToolSchema(raw string, offer knowledgeService.ToolOffer) (string, error) {
	var schema map[string]any
	if err := json.Unmarshal([]byte(raw), &schema); err != nil {
		return "", fmt.Errorf("decode knowledge tool schema: %w", err)
	}
	properties, err := schemaObject(schema, "properties")
	if err != nil {
		return "", err
	}
	action, err := schemaObject(properties, "action")
	if err != nil {
		return "", err
	}
	action["enum"] = slices.Clone(offer.Actions)
	encoded, err := json.Marshal(schema)
	if err != nil {
		return "", fmt.Errorf("encode knowledge tool schema: %w", err)
	}
	return string(encoded), nil
}

func schemaObject(parent map[string]any, name string) (map[string]any, error) {
	value, ok := parent[name].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("knowledge tool schema has no %s object", name)
	}
	return value, nil
}

func normalizeSearchArgs(args map[string]string) (map[string]string, error) {
	query := strings.TrimSpace(args["query"])
	if query == "" {
		return nil, errors.New("query is required for knowledge search")
	}
	if len(query) > 4<<10 {
		return nil, errors.New("knowledge search query exceeds 4 KiB")
	}
	out := map[string]string{"action": "search", "query": query}
	if err := normalizeLimit(args, out, 25); err != nil {
		return nil, err
	}
	if err := normalizeCursor(args, out); err != nil {
		return nil, err
	}
	for _, name := range []string{"include_invalid", "include_superseded", "expand_graph"} {
		if err := normalizeBool(args, out, name); err != nil {
			return nil, err
		}
	}
	if raw := strings.TrimSpace(args["chunk_ids"]); raw != "" {
		ids, err := decodeStringList(raw, "chunk_ids", 25)
		if err != nil {
			return nil, err
		}
		for _, chunkID := range ids {
			if err := (knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: chunkID}).Validate(); err != nil {
				return nil, fmt.Errorf("chunk_ids: %w", err)
			}
		}
		encoded, err := encodeStringList(ids)
		if err != nil {
			return nil, err
		}
		out["chunk_ids"] = encoded
	}
	return out, nil
}

func normalizeGetArgs(args map[string]string) (map[string]string, error) {
	kind, objectID, err := normalizeObject(args, true)
	if err != nil {
		return nil, err
	}
	return map[string]string{"action": "get", "object_kind": kind.String(), "id": objectID}, nil
}

func normalizeNeighborArgs(args map[string]string) (map[string]string, error) {
	kind, objectID, err := normalizeObject(args, false)
	if err != nil {
		return nil, err
	}
	out := map[string]string{"action": "neighbors", "object_kind": kind.String(), "id": objectID}
	direction := strings.TrimSpace(args["direction"])
	if direction == "" {
		direction = string(knowledgeStore.LinkDirectionBoth)
	}
	switch knowledgeStore.LinkDirection(direction) {
	case knowledgeStore.LinkDirectionIncoming, knowledgeStore.LinkDirectionOutgoing, knowledgeStore.LinkDirectionBoth:
		out["direction"] = direction
	default:
		return nil, fmt.Errorf("invalid neighbor direction %q", direction)
	}
	if err := normalizeLimit(args, out, 100); err != nil {
		return nil, err
	}
	if err := normalizeCursor(args, out); err != nil {
		return nil, err
	}
	if raw := strings.TrimSpace(args["link_kinds"]); raw != "" {
		names, err := decodeStringList(raw, "link_kinds", 10)
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			kind, err := knowledge.LinkKindString(name)
			if err != nil || kind == knowledge.LinkKindUnspecified {
				return nil, fmt.Errorf("invalid link kind %q", name)
			}
		}
		encoded, err := encodeStringList(names)
		if err != nil {
			return nil, err
		}
		out["link_kinds"] = encoded
	}
	return out, nil
}

func normalizeObject(args map[string]string, allowLink bool) (knowledge.ObjectKind, string, error) {
	kind, err := knowledge.ObjectKindString(strings.TrimSpace(args["object_kind"]))
	if err != nil || kind == knowledge.ObjectKindUnspecified {
		return 0, "", errors.New("object_kind must be chunk, entry, or link")
	}
	if kind != knowledge.ObjectKindChunk && kind != knowledge.ObjectKindEntry && (!allowLink || kind != knowledge.ObjectKindLink) {
		if allowLink {
			return 0, "", errors.New("object_kind must be chunk, entry, or link")
		}
		return 0, "", errors.New("neighbors object_kind must be chunk or entry")
	}
	objectID := strings.TrimSpace(args["id"])
	if err := (knowledge.ObjectRef{Kind: kind, ID: objectID}).Validate(); err != nil {
		return 0, "", err
	}
	return kind, objectID, nil
}

func normalizeLimit(args, out map[string]string, maximum int) error {
	raw := strings.TrimSpace(args["limit"])
	if raw == "" {
		return nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > maximum {
		return fmt.Errorf("limit must be between 1 and %d", maximum)
	}
	out["limit"] = strconv.Itoa(limit)
	return nil
}

func normalizeCursor(args, out map[string]string) error {
	cursor := strings.TrimSpace(args["cursor"])
	if len(cursor) > 16<<10 {
		return errors.New("cursor exceeds 16 KiB")
	}
	if cursor != "" {
		out["cursor"] = cursor
	}
	return nil
}

func normalizeBool(args, out map[string]string, name string) error {
	raw := strings.TrimSpace(args[name])
	if raw == "" {
		return nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	out[name] = strconv.FormatBool(value)
	return nil
}

func decodeStringList(raw, name string, maximum int) ([]string, error) {
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if len(values) > maximum {
		return nil, fmt.Errorf("%s exceeds %d items", name, maximum)
	}
	for index := range values {
		values[index] = strings.TrimSpace(values[index])
		if values[index] == "" {
			return nil, fmt.Errorf("%s contains an empty item", name)
		}
	}
	return values, nil
}

func encodeStringList(values []string) (string, error) {
	data, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("encode string list: %w", err)
	}
	return string(data), nil
}

func callSearch(ctx context.Context, service *knowledgeService.Service, offer knowledgeService.ToolOffer, args map[string]string) (knowledgeService.LexicalSearchResult, error) {
	request := knowledgeService.LexicalSearchRequest{
		Query:             args["query"],
		Limit:             intArg(args, "limit", 5),
		Cursor:            args["cursor"],
		IncludeInvalid:    boolArg(args, "include_invalid"),
		IncludeSuperseded: boolArg(args, "include_superseded"),
		ScopeKinds:        slices.Clone(offer.ScopeKinds),
	}
	if raw := args["chunk_ids"]; raw != "" {
		var ids []string
		if err := json.Unmarshal([]byte(raw), &ids); err != nil {
			return knowledgeService.LexicalSearchResult{}, fmt.Errorf("decode normalized chunk_ids: %w", err)
		}
		for _, id := range ids {
			request.ChunkIDs = append(request.ChunkIDs, knowledge.ChunkID(id))
		}
	}
	if boolArg(args, "expand_graph") {
		request.GraphExpansion = &knowledgeService.GraphExpansionOptions{}
	}
	return service.SearchLexical(ctx, request)
}

func callGet(ctx context.Context, service *knowledgeService.Service, args map[string]string) (recordResult, error) {
	kind, err := knowledge.ObjectKindString(args["object_kind"])
	if err != nil {
		return recordResult{}, err
	}
	record, err := service.Get(ctx, knowledge.ObjectRef{Kind: kind, ID: args["id"]})
	if err != nil {
		return recordResult{}, err
	}
	return adaptRecord(record), nil
}

func callNeighbors(ctx context.Context, service *knowledgeService.Service, offer knowledgeService.ToolOffer, args map[string]string) (neighborPageResult, error) {
	kind, err := knowledge.ObjectKindString(args["object_kind"])
	if err != nil {
		return neighborPageResult{}, err
	}
	request := knowledgeService.NeighborRequest{
		Endpoint:   knowledge.ObjectRef{Kind: kind, ID: args["id"]},
		Direction:  knowledgeStore.LinkDirection(args["direction"]),
		ScopeKinds: slices.Clone(offer.ScopeKinds),
		Limit:      intArg(args, "limit", 25),
		Cursor:     args["cursor"],
	}
	if raw := args["link_kinds"]; raw != "" {
		var names []string
		if err := json.Unmarshal([]byte(raw), &names); err != nil {
			return neighborPageResult{}, fmt.Errorf("decode normalized link_kinds: %w", err)
		}
		for _, name := range names {
			kind, err := knowledge.LinkKindString(name)
			if err != nil {
				return neighborPageResult{}, err
			}
			request.Kinds = append(request.Kinds, kind)
		}
	}
	page, err := service.Neighbors(ctx, request)
	if err != nil {
		return neighborPageResult{}, err
	}
	result := neighborPageResult{NextCursor: page.NextCursor, Neighbors: make([]neighborResult, 0, len(page.Neighbors))}
	for _, neighbor := range page.Neighbors {
		if err := requireRecordScope(ctx, service, offer, neighbor.Object); err != nil {
			return neighborPageResult{}, err
		}
		result.Neighbors = append(result.Neighbors, neighborResult{
			Direction: neighbor.Direction, Link: neighbor.Link, Object: summarizeRecord(neighbor.Object),
		})
	}
	return result, nil
}

type recordResult struct {
	Kind  knowledgeStore.RecordKind `json:"kind"`
	ID    string                    `json:"id"`
	Chunk *knowledge.Chunk          `json:"chunk,omitempty"`
	Entry *knowledge.Entry          `json:"entry,omitempty"`
	Link  *knowledge.Link           `json:"link,omitempty"`
}

type recordSummary struct {
	Kind         knowledgeStore.RecordKind `json:"kind"`
	ID           string                    `json:"id"`
	SemanticKind string                    `json:"semantic_kind"`
	Title        string                    `json:"title"`
	Summary      string                    `json:"summary,omitempty"`
	Scope        knowledge.Scope           `json:"scope"`
	State        string                    `json:"state"`
}

type neighborResult struct {
	Direction knowledgeStore.LinkDirection `json:"direction"`
	Link      knowledge.Link               `json:"link"`
	Object    recordSummary                `json:"object"`
}

type neighborPageResult struct {
	Neighbors  []neighborResult `json:"neighbors"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

func adaptRecord(record knowledgeStore.CanonicalRecord) recordResult {
	return recordResult{Kind: record.Kind, ID: record.ID(), Chunk: record.Chunk, Entry: record.Entry, Link: record.Link}
}

func summarizeRecord(record knowledgeStore.CanonicalRecord) recordSummary {
	result := recordSummary{Kind: record.Kind, ID: record.ID()}
	switch record.Kind {
	case knowledgeStore.RecordKindChunk:
		if record.Chunk != nil {
			result.SemanticKind = record.Chunk.Kind.String()
			result.Title = record.Chunk.Title
			result.Summary = record.Chunk.Description
			result.Scope = record.Chunk.Scope
			result.State = record.Chunk.State.String()
		}
	case knowledgeStore.RecordKindEntry:
		if record.Entry != nil {
			result.SemanticKind = record.Entry.Kind.String()
			result.Title = record.Entry.Title
			result.Summary = record.Entry.Summary
			result.Scope = record.Entry.Scope
			result.State = record.Entry.State.String()
		}
	}
	return result
}

func intArg(args map[string]string, name string, fallback int) int {
	value, err := strconv.Atoi(args[name])
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func boolArg(args map[string]string, name string) bool {
	return args[name] == "true"
}

func jsonResult(value any) (tools.Result, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return tools.Result{}, err
	}
	return tools.Result{Output: string(data), Stored: value}, nil
}

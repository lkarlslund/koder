package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/knowledge"
	"github.com/lkarlslund/koder/internal/knowledge/curation"
	"github.com/lkarlslund/koder/internal/knowledge/curationadapter"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/provider"
)

const maxCuratorTimelineText = 48 << 10

const maxCurationPatterns = 1024

type curationPatternObservation struct {
	firstSession string
	repeated     bool
}

// StartKnowledgeCuration wires a provider-neutral queue to the current model
// runtime. Completion notifications remain non-blocking; provider and store work
// run on the coordinator's single bounded worker.
func (e *Engine) StartKnowledgeCuration(ctx context.Context, service *knowledgeService.Service) error {
	if e == nil || service == nil {
		return curation.ErrUnavailable
	}
	candidates := curation.NewMemoryCandidateStore()
	routed, err := curation.NewReviewRoutingSink(candidates)
	if err != nil {
		return err
	}
	deduplicated, err := curation.NewDeduplicatingSink(curationadapter.ServiceEntrySource{Service: service}, routed)
	if err != nil {
		return err
	}
	extractor, err := curation.NewModelExtractor(curation.ModelExtractorConfig{
		Loader: e, Model: e, Sink: deduplicated, Classifier: knowledge.RuleClassifier{},
	})
	if err != nil {
		return err
	}
	records := curation.NewMemoryStore()
	queue, err := curation.New(curation.Config{Store: records, Extractor: extractor, NewID: id.New})
	if err != nil {
		return err
	}
	reviewed := curationadapter.ReviewedApplier{Service: service}
	automatic := curationadapter.LowRiskApplier{Service: service}
	manager, err := curation.NewReviewManagerWithAutomatic(candidates, records, reviewed, automatic, reviewed)
	if err != nil {
		return err
	}
	coordinator, err := curation.NewCoordinator(ctx, queue, 128, manager)
	if err != nil {
		return err
	}
	e.curationMu.Lock()
	previous := e.curation
	e.curation, e.curationReview = coordinator, manager
	e.curationMu.Unlock()
	if previous != nil {
		previous.Close()
	}
	return nil
}

func (e *Engine) KnowledgeCuration() *curation.ReviewManager {
	if e == nil {
		return nil
	}
	e.curationMu.RLock()
	defer e.curationMu.RUnlock()
	return e.curationReview
}

func (e *Engine) StopKnowledgeCuration() {
	if e == nil {
		return
	}
	e.curationMu.Lock()
	coordinator := e.curation
	e.curation, e.curationReview = nil, nil
	e.curationMu.Unlock()
	if coordinator != nil {
		coordinator.Close()
	}
}

// ObserveCompletedTurn implements chat.CompletedTurnObserver. It performs only
// bounded string checks and a non-blocking channel send on the turn path.
func (e *Engine) ObserveCompletedTurn(turn chatpkg.CompletedTurn) {
	e.curationMu.RLock()
	coordinator := e.curation
	e.curationMu.RUnlock()
	if coordinator == nil || !turn.User.Sealed() || !turn.Assistant.Sealed() {
		return
	}
	signals := e.curationSignalsForCompletedTurn(turn)
	if len(signals) == 0 {
		return
	}
	coordinator.Observe(curation.SubmitRequest{
		Source: knowledge.CompletedTurnRef{
			SessionID: turn.Session.ID, ChatID: turn.Chat.ID, UserItemID: turn.User.ID,
			AssistantItemID: turn.Assistant.ID, SealedAt: turn.Assistant.SealedAt,
		},
		Signals: signals,
	})
}

func (e *Engine) curationSignalsForCompletedTurn(turn chatpkg.CompletedTurn) []knowledge.CurationSignal {
	signals := completedTurnSignals(turn)
	if fingerprint := workaroundFingerprint(turn.Items); fingerprint != "" && e.observeWorkaroundPattern(turn.Session.ID, fingerprint) {
		sourceIDs := curationSignalSourceIDs(signals)
		if len(sourceIDs) != 0 {
			signals = append(signals, knowledge.CurationSignal{
				Kind: knowledge.CurationSignalKindRepeatedWorkaround, SourceItemIDs: sourceIDs, Confidence: 0.85,
			})
		}
	}
	return signals
}

func curationSignalSourceIDs(signals []knowledge.CurationSignal) []string {
	if len(signals) == 0 {
		return nil
	}
	return slices.Clone(signals[0].SourceItemIDs)
}

func workaroundFingerprint(items []domain.TimelineItem) string {
	failed := make(map[string]struct{})
	succeeded := make(map[string]struct{})
	inspect := func(tool domain.ToolKind, args map[string]string, result bool, hasError bool) {
		signature := strings.ToLower(strings.TrimSpace(string(tool)))
		if action := strings.ToLower(strings.TrimSpace(args["action"])); action != "" {
			signature += ":" + action
		}
		if signature == "" {
			return
		}
		if hasError {
			failed[signature] = struct{}{}
		}
		if result {
			succeeded[signature] = struct{}{}
		}
	}
	for _, item := range items {
		switch content := item.Content.(type) {
		case domain.AssistantMessage:
			for _, call := range content.Tools {
				inspect(call.Tool, call.Args, call.Result != nil && call.Error == nil, call.Error != nil)
			}
		case domain.ToolExecution:
			inspect(content.Tool, content.Args, content.Result != nil && content.Error == nil, content.Error != nil)
		}
	}
	if len(failed) == 0 || len(succeeded) == 0 {
		return ""
	}
	failures := mapKeys(failed)
	successes := mapKeys(succeeded)
	sort.Strings(failures)
	sort.Strings(successes)
	digest := sha256.Sum256([]byte("failed=" + strings.Join(failures, ",") + "\nsucceeded=" + strings.Join(successes, ",")))
	return fmt.Sprintf("%x", digest[:])
}

func mapKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	return result
}

func (e *Engine) observeWorkaroundPattern(sessionID id.ID, fingerprint string) bool {
	if e == nil || sessionID == "" || fingerprint == "" {
		return false
	}
	e.curationPatternMu.Lock()
	defer e.curationPatternMu.Unlock()
	if e.curationPatterns == nil {
		e.curationPatterns = make(map[string]curationPatternObservation)
	}
	observation, exists := e.curationPatterns[fingerprint]
	if !exists {
		if len(e.curationPatterns) >= maxCurationPatterns {
			for key := range e.curationPatterns {
				delete(e.curationPatterns, key)
				break
			}
		}
		e.curationPatterns[fingerprint] = curationPatternObservation{firstSession: string(sessionID)}
		return false
	}
	if observation.repeated {
		return true
	}
	if observation.firstSession == string(sessionID) {
		return false
	}
	observation.repeated = true
	e.curationPatterns[fingerprint] = observation
	return true
}

func completedTurnSignals(turn chatpkg.CompletedTurn) []knowledge.CurationSignal {
	user, _ := turn.User.Content.(domain.UserMessage)
	lower := strings.ToLower(strings.TrimSpace(user.Text))
	itemIDs := make([]string, 0, len(turn.Items))
	failedTool, successfulTool, researched := false, false, false
	for _, item := range turn.Items {
		role, materialText := curationTimelineText(item)
		if item.Sealed() && item.ID != "" && role != "" && strings.TrimSpace(materialText) != "" {
			itemIDs = append(itemIDs, item.ID)
		}
		inspectTool := func(name string, result bool, failed bool) {
			failedTool = failedTool || failed
			successfulTool = successfulTool || result
			name = strings.ToLower(name)
			researched = researched || strings.Contains(name, "browser") || strings.Contains(name, "web")
		}
		switch content := item.Content.(type) {
		case domain.AssistantMessage:
			for _, call := range content.Tools {
				inspectTool(string(call.Tool), call.Result != nil && call.Error == nil, call.Error != nil)
			}
		case domain.ToolExecution:
			inspectTool(string(content.Tool), content.Result != nil && content.Error == nil, content.Error != nil)
		}
	}
	if len(itemIDs) == 0 {
		return nil
	}
	itemIDs = boundedCurationSourceIDs(itemIDs)
	type signalSpec struct {
		kind       knowledge.CurationSignalKind
		confidence float32
	}
	specs := make([]signalSpec, 0, 4)
	containsAny := func(values ...string) bool {
		return slices.ContainsFunc(values, func(value string) bool { return strings.Contains(lower, value) })
	}
	if containsAny("actually", "that's wrong", "that is wrong", "correction:", "not what i meant", "no, ") {
		specs = append(specs, signalSpec{knowledge.CurationSignalKindUserCorrection, 0.9})
	}
	if containsAny("i prefer", "i like to", "i don't want", "i do not want", "always use", "never use") {
		specs = append(specs, signalSpec{knowledge.CurationSignalKindExplicitPersonalPreference, 0.9})
	}
	if containsAny("contradicts", "inconsistent with", "but earlier you said") {
		specs = append(specs, signalSpec{knowledge.CurationSignalKindContradictingEvidence, 0.85})
	}
	if failedTool && successfulTool {
		specs = append(specs, signalSpec{knowledge.CurationSignalKindFailedThenSucceeded, 0.8})
	}
	if researched && successfulTool {
		specs = append(specs, signalSpec{knowledge.CurationSignalKindResearchedThenSucceeded, 0.75})
	}
	result := make([]knowledge.CurationSignal, 0, len(specs))
	for _, spec := range specs {
		result = append(result, knowledge.CurationSignal{Kind: spec.kind, SourceItemIDs: slices.Clone(itemIDs), Confidence: spec.confidence})
	}
	return result
}

func boundedCurationSourceIDs(itemIDs []string) []string {
	const limit = 64
	if len(itemIDs) <= limit {
		return itemIDs
	}
	// Preserve evidence from both ends of a long tool loop: the initiating user
	// request and early failures as well as the eventual recovery and final answer.
	bounded := make([]string, 0, limit)
	bounded = append(bounded, itemIDs[:limit/2]...)
	bounded = append(bounded, itemIDs[len(itemIDs)-limit/2:]...)
	return bounded
}

// Load implements curation.TurnLoader from Koder's persisted timeline and
// authorized Knowledge destinations.
func (e *Engine) Load(ctx context.Context, source knowledge.CompletedTurnRef, sourceItemIDs []string) (curation.TurnMaterial, error) {
	wanted := make(map[string]struct{}, len(sourceItemIDs))
	for _, itemID := range sourceItemIDs {
		wanted[itemID] = struct{}{}
	}
	timelineSource := chatpkg.NewSource(e.ChatDeps)
	page, err := timelineSource.TimelinePage(ctx, source.ChatID, "", 0, true)
	if err != nil {
		return curation.TurnMaterial{}, fmt.Errorf("load curation timeline: %w", err)
	}
	material := curation.TurnMaterial{Items: make([]curation.TurnItem, 0, len(wanted))}
	for _, item := range page.Items {
		if _, ok := wanted[item.ID]; !ok || !item.Sealed() {
			continue
		}
		role, text := curationTimelineText(item)
		if role == "" || strings.TrimSpace(text) == "" {
			continue
		}
		material.Items = append(material.Items, curation.TurnItem{ID: item.ID, Role: role, Text: boundedCuratorText(text)})
	}
	if service := e.KnowledgeService(); service != nil {
		chunks, err := service.ListChunks(ctx, knowledgeStore.ChunkListRequest{Limit: 50})
		if err != nil {
			return curation.TurnMaterial{}, fmt.Errorf("list curation destinations: %w", err)
		}
		for _, chunk := range chunks.Chunks {
			material.Destinations = append(material.Destinations, curation.Destination{ID: chunk.ID, Title: chunk.Title, Kind: chunk.Kind, Scope: chunk.Scope})
		}
	}
	return material, nil
}

func curationTimelineText(item domain.TimelineItem) (string, string) {
	switch content := item.Content.(type) {
	case domain.UserMessage:
		return "user", content.Text
	case domain.AssistantMessage:
		parts := []string{strings.TrimSpace(content.Text)}
		for _, call := range content.Tools {
			if encoded, err := json.Marshal(call); err == nil {
				parts = append(parts, string(encoded))
			}
		}
		return "assistant", strings.Join(parts, "\n")
	case domain.ToolExecution:
		encoded, _ := json.Marshal(content)
		return "tool", string(encoded)
	default:
		return "", ""
	}
}

func boundedCuratorText(value string) string {
	if len(value) <= maxCuratorTimelineText {
		return value
	}
	return value[:maxCuratorTimelineText] + "\n[TRUNCATED]"
}

// Draft implements curation.DraftModel using the originating Koder model when
// possible and the configured system default for non-Koder turn drivers.
func (e *Engine) Draft(ctx context.Context, record knowledge.CurationRecord, material curation.TurnMaterial, schema json.RawMessage) ([]byte, error) {
	owner, err := e.LoadSession(ctx, record.Source.SessionID)
	if err != nil {
		return nil, err
	}
	snapshot := owner.Snapshot()
	chatRecord := domain.Chat{ID: record.Source.ChatID, SessionID: record.Source.SessionID, Backend: domain.ChatBackendKoder, ProviderID: e.cfg.Defaults.ProviderID, ModelID: e.cfg.Defaults.ModelID}
	if index := slices.IndexFunc(snapshot.Chats, func(chat domain.Chat) bool { return chat.ID == record.Source.ChatID }); index >= 0 && snapshot.Chats[index].EffectiveBackend() == domain.ChatBackendKoder {
		chatRecord = snapshot.Chats[index]
	}
	client, err := e.ClientForChat(chatRecord)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(struct {
		Material curation.TurnMaterial `json:"material"`
		Schema   json.RawMessage       `json:"output_schema"`
	}{Material: material, Schema: schema})
	if err != nil {
		return nil, err
	}
	messages := []provider.Message{
		{Role: provider.RoleSystem, Content: "You curate durable reusable Knowledge from one completed chat turn. Return JSON only, exactly matching the supplied schema. Use only supplied destination chunk IDs and source item IDs. Propose nothing for transient task progress, guesses, secrets, credentials, or facts unlikely to help later. Personal facts must target the personal destination, state whether they were explicit, observed, or inferred, and keep inferences uncertain. An empty candidates array is correct when nothing is durable."},
		{Role: provider.RoleUser, Content: string(payload)},
	}
	request := e.chatRequest(snapshot.Session, chatRecord, messages, false)
	request.Tools = nil
	request.ToolChoice = ""
	response, err := client.CompleteChat(ctx, request)
	if err != nil {
		return nil, err
	}
	return []byte(strings.TrimSpace(response.Text)), nil
}

package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/assets"
	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/toolkind"
)

func (c *Controller) Providers() ProviderState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.providerStateLocked()
}

// NewProviderDraft returns a draft initialized from a provider template.
func (c *Controller) NewProviderDraft(templateID string) (ProviderDraft, error) {
	c.mu.RLock()
	cfg := c.cfg
	c.mu.RUnlock()
	templateID = strings.TrimSpace(templateID)
	if templateID == "" {
		templateID = provider.ProviderKindCompatible
	}
	draft, err := provider.BuildDraft(templateID, cfg.Providers)
	if err != nil {
		return ProviderDraft{}, err
	}
	return providerDraftFromCatalog(draft), nil
}

// TestProvider probes a provider draft by listing models.
func (c *Controller) TestProvider(ctx context.Context, draft ProviderDraft) (ProviderProbeResult, error) {
	result, err := provider.Probe(ctx, providerDraftToCatalog(draft), nil)
	if err != nil {
		return ProviderProbeResult{}, err
	}
	models := make([]string, 0, len(result.Models))
	for _, item := range result.Models {
		models = append(models, item.ID)
	}
	return ProviderProbeResult{
		ModelCount:              len(result.Models),
		Models:                  models,
		SelectedModel:           result.SelectedModel,
		PromptProgressProbed:    result.PromptProgressProbed,
		PromptProgressSupported: result.PromptProgressSupported,
	}, nil
}

// SaveProvider validates and persists a provider draft.
func (c *Controller) SaveProvider(ctx context.Context, draft ProviderDraft) (ProviderState, error) {
	catalogDraft := providerDraftToCatalog(draft)
	if err := provider.ValidateDraft(catalogDraft); err != nil {
		return ProviderState{}, err
	}
	probe, err := provider.Probe(ctx, catalogDraft, nil)
	if err != nil {
		return ProviderState{}, err
	}
	catalogDraft.Model = probe.SelectedModel
	catalogDraft.PromptProgressProbed = probe.PromptProgressProbed
	catalogDraft.PromptProgressSupported = probe.PromptProgressSupported
	draft.PromptProgressProbed = probe.PromptProgressProbed
	draft.PromptProgressSupported = probe.PromptProgressSupported
	originalID := strings.TrimSpace(catalogDraft.OriginalProviderID)
	catalogDraft.ProviderID = strings.TrimSpace(catalogDraft.ProviderID)
	if catalogDraft.ProviderID == "" {
		return ProviderState{}, fmt.Errorf("provider id is required")
	}

	c.mu.Lock()
	if c.cfg.Providers == nil {
		c.cfg.Providers = map[string]config.Provider{}
	}
	if originalID != "" && originalID != catalogDraft.ProviderID {
		if _, exists := c.cfg.Providers[catalogDraft.ProviderID]; exists {
			c.mu.Unlock()
			return ProviderState{}, fmt.Errorf("provider %q already exists", catalogDraft.ProviderID)
		}
	}
	next := catalogDraft.ToConfig()
	lookupID := catalogDraft.ProviderID
	if originalID != "" {
		lookupID = originalID
	}
	existing, ok := c.cfg.Providers[lookupID]
	if ok {
		mergeProviderEditDefaults(&next, existing)
	} else {
		applyNewProviderDefaults(&next)
	}
	applyProviderDraftPreferences(&next, draft)
	if strings.TrimSpace(next.Name) == "" {
		if desc, found := provider.Lookup(catalogDraft.TemplateID); found {
			next.Name = desc.Title
		} else {
			next.Name = catalogDraft.ProviderID
		}
	}
	if originalID != "" && originalID != catalogDraft.ProviderID {
		delete(c.cfg.Providers, originalID)
		renameModelConfigs(&c.cfg, originalID, catalogDraft.ProviderID)
	}
	c.cfg.Providers[catalogDraft.ProviderID] = next
	c.cfg.SetModelConfig(config.ModelConfig{
		ProviderID:    catalogDraft.ProviderID,
		ModelID:       catalogDraft.Model,
		ContextWindow: c.cfg.ContextWindow(catalogDraft.ProviderID, catalogDraft.Model),
		ModelPreset:   c.cfg.ModelPreset(catalogDraft.ProviderID, catalogDraft.Model),
	})
	if strings.TrimSpace(c.cfg.DefaultProvider) == "" || c.cfg.DefaultProvider == originalID || c.cfg.DefaultProvider == catalogDraft.ProviderID {
		c.cfg.DefaultProvider = catalogDraft.ProviderID
		c.cfg.DefaultModel = catalogDraft.Model
	}
	if err := c.cfg.Save(); err != nil {
		c.mu.Unlock()
		return ProviderState{}, err
	}
	if c.agent != nil {
		c.agent.UpdateConfig(c.cfg)
	}
	if c.chat.ID != "" && (strings.TrimSpace(c.chat.ProviderID) == "" || !c.cfg.HasUsableProvider(c.chat.ProviderID) || c.chat.ProviderID == originalID) {
		if c.agent == nil {
			c.mu.Unlock()
			return ProviderState{}, fmt.Errorf("no chat agent")
		}
		owner, err := c.agent.LoadSession(ctx, c.session.ID)
		if err != nil {
			c.mu.Unlock()
			return ProviderState{}, err
		}
		chatRecord, err := owner.SetChatModel(ctx, c.chat.ID, catalogDraft.ProviderID, catalogDraft.Model)
		if err != nil {
			c.mu.Unlock()
			return ProviderState{}, err
		}
		c.chat = chatRecord
		for idx := range c.chats {
			if c.chats[idx].ID == chatRecord.ID {
				c.chats[idx] = chatRecord
			}
		}
		if c.runtime != nil {
			c.runtime.SetChat(chatRecord)
			c.runtime.SetSession(c.session)
		}
	}
	state := c.providerStateLocked()
	c.mu.Unlock()
	c.broadcast("snapshot", c.State())
	return state, nil
}

// DeleteProvider removes a configured provider.
func (c *Controller) DeleteProvider(ctx context.Context, providerID string) (ProviderState, error) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return ProviderState{}, fmt.Errorf("provider id is required")
	}
	c.mu.Lock()
	if _, ok := c.cfg.Providers[providerID]; !ok {
		c.mu.Unlock()
		return ProviderState{}, fmt.Errorf("provider %q is not configured", providerID)
	}
	delete(c.cfg.Providers, providerID)
	deleteModelConfigs(&c.cfg, providerID)
	nextDefault := strings.TrimSpace(c.cfg.DefaultProvider)
	if nextDefault == providerID || !c.cfg.HasUsableProvider(nextDefault) {
		nextDefault = ""
		ids := make([]string, 0, len(c.cfg.Providers))
		for id := range c.cfg.Providers {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		if len(ids) > 0 {
			nextDefault = ids[0]
		}
	}
	c.cfg.DefaultProvider = nextDefault
	c.cfg.DefaultModel = ""
	if nextDefault != "" {
		c.cfg.DefaultModel = firstModelForProvider(c.cfg, nextDefault)
	}
	if err := c.cfg.Save(); err != nil {
		c.mu.Unlock()
		return ProviderState{}, err
	}
	if c.agent != nil {
		c.agent.UpdateConfig(c.cfg)
	}
	if c.chat.ID != "" && c.chat.ProviderID == providerID {
		if c.agent == nil {
			c.mu.Unlock()
			return ProviderState{}, fmt.Errorf("no chat agent")
		}
		owner, err := c.agent.LoadSession(ctx, c.session.ID)
		if err != nil {
			c.mu.Unlock()
			return ProviderState{}, err
		}
		chatRecord, err := owner.SetChatModel(ctx, c.chat.ID, c.cfg.DefaultProvider, c.cfg.DefaultModel)
		if err != nil {
			c.mu.Unlock()
			return ProviderState{}, err
		}
		c.chat = chatRecord
		for idx := range c.chats {
			if c.chats[idx].ID == chatRecord.ID {
				c.chats[idx] = chatRecord
			}
		}
		if c.runtime != nil {
			c.runtime.SetChat(chatRecord)
			c.runtime.SetSession(c.session)
		}
	}
	state := c.providerStateLocked()
	c.mu.Unlock()
	c.broadcast("snapshot", c.State())
	return state, nil
}

// Preferences returns the complete editable settings state.
func (c *Controller) Preferences(ctx context.Context) (PreferencesState, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.preferencesStateLocked(ctx)
}

// SavePreferences validates and persists the complete settings state.
func (c *Controller) SavePreferences(ctx context.Context, prefs PreferencesState) (PreferencesState, error) {
	c.mu.Lock()
	next := c.cfg
	repairStaleGeneralProvider(&next, &prefs)
	if err := applyGeneralPreferences(&next, prefs.General); err != nil {
		c.mu.Unlock()
		return PreferencesState{}, err
	}
	if err := applyBrowserPreferences(&next, prefs.UI); err != nil {
		c.mu.Unlock()
		return PreferencesState{}, err
	}
	if err := applyCompactionPreferences(&next, prefs.Compaction); err != nil {
		c.mu.Unlock()
		return PreferencesState{}, err
	}
	if err := applyThinkingPreferences(&next, prefs.Thinking); err != nil {
		c.mu.Unlock()
		return PreferencesState{}, err
	}
	if err := applyModelConfigPreferences(&next, prefs.ModelConfigs); err != nil {
		c.mu.Unlock()
		return PreferencesState{}, err
	}
	if err := applyMCPPreferences(&next, prefs.MCPServers); err != nil {
		c.mu.Unlock()
		return PreferencesState{}, err
	}
	if err := applyAccessPreferences(&next, prefs.Access); err != nil {
		c.mu.Unlock()
		return PreferencesState{}, err
	}
	applyToolDefaultPreferences(&next, prefs.ToolDefaults)
	if err := writePromptPreferences(prefs.Prompts); err != nil {
		c.mu.Unlock()
		return PreferencesState{}, err
	}
	c.cfg = next
	c.theme = normalizeTheme(next.UI.Theme)
	if err := c.cfg.Save(); err != nil {
		c.mu.Unlock()
		return PreferencesState{}, err
	}
	if c.agent != nil {
		c.agent.UpdateConfig(c.cfg)
	}
	state, err := c.preferencesStateLocked(ctx)
	c.mu.Unlock()
	if err != nil {
		return PreferencesState{}, err
	}
	c.broadcast("snapshot", c.State())
	c.broadcast("theme", map[string]string{"theme": c.theme})
	return state, nil
}

// ResetPrompt restores one managed prompt file from embedded defaults.
func (c *Controller) ResetPrompt(target string) (PromptPreference, error) {
	target = strings.TrimSpace(target)
	if target != "system-prompt.md" && target != "compaction-prompt.md" {
		return PromptPreference{}, fmt.Errorf("unknown prompt %q", target)
	}
	content, err := assets.DefaultContent(target)
	if err != nil {
		return PromptPreference{}, err
	}
	path, err := managedPromptPath(target)
	if err != nil {
		return PromptPreference{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return PromptPreference{}, fmt.Errorf("create prompt dir: %w", err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return PromptPreference{}, fmt.Errorf("write prompt %s: %w", target, err)
	}
	return promptPreference(target)
}

// ModelOptions lists selectable models across configured providers.
func (c *Controller) ModelOptions(ctx context.Context) ([]ModelOption, error) {
	c.mu.RLock()
	cfg := c.cfg
	currentProvider := strings.TrimSpace(c.chat.ProviderID)
	currentModel := strings.TrimSpace(c.chat.ModelID)
	c.mu.RUnlock()
	return modelOptionsForConfig(ctx, cfg, currentProvider, currentModel)
}

func (c *Controller) modelOptionsLocked(ctx context.Context) ([]ModelOption, error) {
	return modelOptionsForConfig(ctx, c.cfg, strings.TrimSpace(c.chat.ProviderID), strings.TrimSpace(c.chat.ModelID))
}

func modelOptionsForConfig(ctx context.Context, cfg config.Config, currentProvider, currentModel string) ([]ModelOption, error) {
	seen := map[string]struct{}{}
	detected := map[string]domain.Model{}
	options := make([]ModelOption, 0, len(cfg.Providers))
	add := func(providerID string, providerCfg config.Provider, model domain.Model) {
		providerID = strings.TrimSpace(providerID)
		modelID := strings.TrimSpace(model.ID)
		if providerID == "" || modelID == "" {
			return
		}
		key := providerID + "\x00" + modelID
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		detected[key] = model
		options = append(options, ModelOption{
			ProviderID:       providerID,
			ProviderLabel:    providerEntryLabel(providerID, providerCfg),
			ModelID:          modelID,
			SourceProviderID: providerID,
			SourceModelID:    modelID,
			OwnedBy:          strings.TrimSpace(model.OwnedBy),
			Detected:         true,
			BackingDetected:  true,
			Editable:         false,
			Current:          providerID == currentProvider && modelID == currentModel,
		})
	}

	ids := make([]string, 0, len(cfg.Providers))
	for id, providerCfg := range cfg.Providers {
		if providerCfg.Disabled {
			continue
		}
		ids = append(ids, id)
	}
	slices.Sort(ids)

	var failures []string
	for _, providerID := range ids {
		providerCfg, ok := cfg.Provider(providerID)
		if !ok {
			continue
		}
		client, err := provider.New(providerID, providerCfg, nil)
		if err != nil {
			failures = append(failures, providerID)
			continue
		}
		models, err := client.ListModels(ctx)
		if err != nil {
			failures = append(failures, providerID)
			continue
		}
		for _, model := range models {
			add(providerID, providerCfg, model)
		}
	}
	for _, model := range cfg.Models {
		model.ProviderID = strings.TrimSpace(model.ProviderID)
		model.ModelID = strings.TrimSpace(model.ModelID)
		model.SourceProviderID = strings.TrimSpace(model.SourceProviderID)
		model.SourceModelID = strings.TrimSpace(model.SourceModelID)
		if model.ProviderID == "" || model.ModelID == "" || model.SourceModelID == "" {
			continue
		}
		sourceProviderID := model.SourceProviderID
		if sourceProviderID == "" {
			sourceProviderID = model.ProviderID
		}
		providerCfg, ok := cfg.Provider(model.ProviderID)
		if !ok || providerCfg.Disabled {
			continue
		}
		key := model.ProviderID + "\x00" + model.ModelID
		if _, ok := seen[key]; ok {
			continue
		}
		sourceKey := sourceProviderID + "\x00" + model.SourceModelID
		source, sourceDetected := detected[sourceKey]
		options = append(options, ModelOption{
			ProviderID:       model.ProviderID,
			ProviderLabel:    providerEntryLabel(model.ProviderID, providerCfg),
			ModelID:          model.ModelID,
			SourceProviderID: sourceProviderID,
			SourceModelID:    model.SourceModelID,
			OwnedBy:          strings.TrimSpace(source.OwnedBy),
			Custom:           true,
			BackingDetected:  sourceDetected,
			Editable:         true,
			Current:          model.ProviderID == currentProvider && model.ModelID == currentModel,
		})
		seen[key] = struct{}{}
	}
	slices.SortFunc(options, func(a, b ModelOption) int {
		if cmp := strings.Compare(a.ProviderLabel, b.ProviderLabel); cmp != 0 {
			return cmp
		}
		if a.Custom != b.Custom {
			if a.Custom {
				return -1
			}
			return 1
		}
		return strings.Compare(a.ModelID, b.ModelID)
	})
	if len(options) == 0 && len(failures) > 0 {
		return nil, fmt.Errorf("failed to load models from %s", strings.Join(failures, ", "))
	}
	return options, nil
}

// ModelConfig returns editable settings for a provider/model pair.
func (c *Controller) ModelConfig(providerID, modelID string) ModelConfigPreference {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return modelConfigPreferenceFromConfig(modelConfigForPair(c.cfg, providerID, modelID))
}

// SaveModelConfig validates and persists one provider/model settings row.
func (c *Controller) SaveModelConfig(ctx context.Context, pref ModelConfigPreference) (ModelConfigPreference, error) {
	providerID := strings.TrimSpace(pref.ProviderID)
	modelID := strings.TrimSpace(pref.ModelID)
	if providerID == "" {
		return ModelConfigPreference{}, fmt.Errorf("provider id is required")
	}
	if modelID == "" {
		return ModelConfigPreference{}, fmt.Errorf("model id is required")
	}
	model, err := configModelFromPreference(pref)
	if err != nil {
		return ModelConfigPreference{}, err
	}
	c.mu.Lock()
	if !c.cfg.HasUsableProvider(providerID) {
		c.mu.Unlock()
		return ModelConfigPreference{}, fmt.Errorf("provider %q is not configured", providerID)
	}
	if sourceProviderID := strings.TrimSpace(model.SourceProviderID); sourceProviderID != "" && !c.cfg.HasUsableProvider(sourceProviderID) {
		c.mu.Unlock()
		return ModelConfigPreference{}, fmt.Errorf("source provider %q is not configured", sourceProviderID)
	}
	c.cfg.SetModelConfig(model)
	if err := c.cfg.Save(); err != nil {
		c.mu.Unlock()
		return ModelConfigPreference{}, err
	}
	if c.agent != nil {
		c.agent.UpdateConfig(c.cfg)
	}
	saved := modelConfigPreferenceFromConfig(modelConfigForPair(c.cfg, providerID, modelID))
	c.mu.Unlock()
	c.broadcast("snapshot", c.State())
	return saved, nil
}

// SetModelForSelection persists the selected chat model and updates its live runtime.
func (c *Controller) SetModelForSelection(ctx context.Context, selection Selection, providerID, modelID string) error {
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(modelID)
	if providerID == "" {
		return fmt.Errorf("provider id is required")
	}
	if modelID == "" {
		return fmt.Errorf("model id is required")
	}
	if !c.cfg.HasUsableProvider(providerID) {
		return fmt.Errorf("provider %q is not configured", providerID)
	}
	c.ensureModelConfig(ctx, providerID, modelID)
	owner, _, chatRecord, rt, err := c.resolveSelectedRuntime(ctx, selection, true)
	if err != nil {
		return err
	}
	chatRecord, err = owner.SetChatModel(ctx, chatRecord.ID, providerID, modelID)
	if err != nil {
		return err
	}
	session := owner.Snapshot().Session
	rt.SetChat(chatRecord)
	rt.SetSession(session)
	c.mu.Lock()
	for idx := range c.sessions {
		if c.sessions[idx].ID == session.ID {
			c.sessions[idx] = session
		}
	}
	if snapshot, ok := c.snapshots[chatRecord.ID]; ok {
		snapshot.Chat = chatRecord
		snapshot.Session = session
		c.snapshots[chatRecord.ID] = snapshot
	}
	c.mu.Unlock()
	return nil
}

// SetAccessSettingsForSelection updates the selected session sandbox access settings.
func (c *Controller) SetAccessSettingsForSelection(ctx context.Context, selection Selection, settings accesssettings.Settings) error {
	settings = accesssettings.Normalize(settings)
	if err := accesssettings.Validate(settings); err != nil {
		return err
	}
	if selection.SessionID == "" {
		return fmt.Errorf("session id is required")
	}
	if c.agent == nil {
		return fmt.Errorf("no chat agent")
	}
	owner, err := c.agent.LoadSession(ctx, selection.SessionID)
	if err != nil {
		return err
	}
	session, err := owner.SetAccessSettings(ctx, settings)
	if err != nil {
		return err
	}
	snapshot := owner.Snapshot()
	runtimes := make([]*chat.Chat, 0, len(snapshot.Snapshots))
	for _, item := range snapshot.Chats {
		rt, err := owner.Chat(ctx, item.ID)
		if err == nil && rt != nil {
			runtimes = append(runtimes, rt)
		}
	}
	c.mu.Lock()
	for idx := range c.sessions {
		if c.sessions[idx].ID == session.ID {
			c.sessions[idx] = session
		}
	}
	for id, item := range c.snapshots {
		if item.Session.ID == session.ID {
			item.Session = session
			c.snapshots[id] = item
		}
	}
	c.mu.Unlock()
	for _, rt := range runtimes {
		rt.SetSession(session)
	}
	return nil
}

func providerEntryLabel(providerID string, cfg config.Provider) string {
	if label := strings.TrimSpace(cfg.Name); label != "" {
		return label
	}
	return providerID
}

func (c *Controller) ensureModelConfig(ctx context.Context, providerID, modelID string) {
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(modelID)
	if providerID == "" || modelID == "" {
		return
	}
	c.mu.RLock()
	sourceProviderID, sourceModelID := c.cfg.ResolveModel(providerID, modelID)
	providerCfg, providerOK := c.cfg.Provider(sourceProviderID)
	existing, existingOK := c.cfg.ModelConfig(providerID, modelID)
	c.mu.RUnlock()
	if !providerOK {
		return
	}
	contextWindow := existing.ContextWindow
	if !existingOK || contextWindow <= 0 || contextWindow == 32768 {
		if detected, err := provider.DetectContextWindow(ctx, sourceProviderID, providerCfg, sourceModelID, nil); err == nil && detected > 0 {
			contextWindow = detected
		}
	}
	if contextWindow <= 0 {
		contextWindow = 32768
	}
	preset := strings.TrimSpace(existing.ModelPreset)
	if preset == "" {
		preset = "auto"
	}
	if existingOK && existing.ContextWindow == contextWindow && strings.TrimSpace(existing.ModelPreset) == preset {
		return
	}
	c.mu.Lock()
	c.cfg.SetModelConfig(config.ModelConfig{
		ProviderID:       providerID,
		ModelID:          modelID,
		SourceProviderID: existing.SourceProviderID,
		SourceModelID:    existing.SourceModelID,
		ContextWindow:    contextWindow,
		ModelPreset:      preset,
	})
	if err := c.cfg.Save(); err == nil && c.agent != nil {
		c.agent.UpdateConfig(c.cfg)
	}
	c.mu.Unlock()
}

func firstModelForProvider(cfg config.Config, providerID string) string {
	providerID = strings.TrimSpace(providerID)
	for _, model := range cfg.Models {
		if strings.TrimSpace(model.ProviderID) == providerID && strings.TrimSpace(model.ModelID) != "" {
			return strings.TrimSpace(model.ModelID)
		}
	}
	return ""
}

func renameModelConfigs(cfg *config.Config, oldProviderID, newProviderID string) {
	oldProviderID = strings.TrimSpace(oldProviderID)
	newProviderID = strings.TrimSpace(newProviderID)
	if cfg == nil || oldProviderID == "" || newProviderID == "" || oldProviderID == newProviderID {
		return
	}
	for idx := range cfg.Models {
		if strings.TrimSpace(cfg.Models[idx].ProviderID) == oldProviderID {
			cfg.Models[idx].ProviderID = newProviderID
		}
	}
}

func deleteModelConfigs(cfg *config.Config, providerID string) {
	providerID = strings.TrimSpace(providerID)
	if cfg == nil || providerID == "" {
		return
	}
	out := cfg.Models[:0]
	for _, model := range cfg.Models {
		if strings.TrimSpace(model.ProviderID) != providerID {
			out = append(out, model)
		}
	}
	cfg.Models = out
}

func (c *Controller) preferencesStateLocked(ctx context.Context) (PreferencesState, error) {
	models, _ := c.modelOptionsLocked(ctx)
	liveModels := slices.Clone(models)
	models = ensureModelOption(models, c.cfg, strings.TrimSpace(c.cfg.CompactionProvider), strings.TrimSpace(c.cfg.CompactionModel))
	models = ensureModelOption(models, c.cfg, strings.TrimSpace(c.cfg.Thinking.CavemanProvider), strings.TrimSpace(c.cfg.Thinking.CavemanModel))
	prompts, err := promptPreferences()
	if err != nil {
		return PreferencesState{}, err
	}
	state := PreferencesState{
		General: GeneralPreferences{
			DefaultProvider:  strings.TrimSpace(c.cfg.DefaultProvider),
			DefaultModel:     strings.TrimSpace(c.cfg.DefaultModel),
			MaxToolLoopSteps: c.cfg.MaxToolLoopSteps,
			StoreBackend:     strings.TrimSpace(c.cfg.Store.Backend),
		},
		UI:           browserPreferencesFromConfig(c.cfg.UI),
		Compaction:   compactionPreferencesFromConfig(c.cfg),
		Thinking:     thinkingPreferencesFromConfig(c.cfg),
		Prompts:      prompts,
		Providers:    c.providerStateLocked(),
		Models:       models,
		ModelConfigs: modelConfigPreferencesFromConfig(c.cfg.Models, models),
		MCPServers:   mcpPreferencesFromConfig(c.cfg.MCPServers),
		Access:       accessPreferencesFromConfig(c.cfg.Access),
		ToolDefaults: toolDefaultPreferencesFromConfig(c.cfg.ToolDefaults),
	}
	repairPreferencesDefaultModel(&state, liveModels)
	if c.cfg.Store.Backend != config.Default().Store.Backend {
		state.RestartKeys = append(state.RestartKeys, "store.backend")
	}
	return state, nil
}

func repairPreferencesDefaultModel(state *PreferencesState, liveModels []ModelOption) {
	if state == nil || len(liveModels) == 0 {
		return
	}
	for _, option := range liveModels {
		if option.ProviderID == state.General.DefaultProvider && option.ModelID == state.General.DefaultModel {
			return
		}
	}
	first := liveModels[0]
	state.General.DefaultProvider = first.ProviderID
	state.General.DefaultModel = first.ModelID
	state.Providers.DefaultProvider = first.ProviderID
	state.Providers.DefaultModel = first.ModelID
}

func ensureModelOption(options []ModelOption, cfg config.Config, providerID, modelID string) []ModelOption {
	if providerID == "" || modelID == "" {
		return options
	}
	for _, option := range options {
		if option.ProviderID == providerID && option.ModelID == modelID {
			return options
		}
	}
	providerCfg, ok := cfg.Provider(providerID)
	label := providerID
	if ok {
		label = providerEntryLabel(providerID, providerCfg)
	}
	sourceProviderID, sourceModelID := cfg.ResolveModel(providerID, modelID)
	custom := sourceModelID != "" && (sourceProviderID != providerID || sourceModelID != modelID)
	options = append(options, ModelOption{
		ProviderID:       providerID,
		ProviderLabel:    label,
		ModelID:          modelID,
		SourceProviderID: sourceProviderID,
		SourceModelID:    sourceModelID,
		Custom:           custom,
		Editable:         custom,
	})
	slices.SortFunc(options, func(a, b ModelOption) int {
		if cmp := strings.Compare(a.ProviderLabel, b.ProviderLabel); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.ModelID, b.ModelID)
	})
	return options
}

func modelConfigPreferencesFromConfig(src []config.ModelConfig, options []ModelOption) []ModelConfigPreference {
	detected := map[string]bool{}
	for _, option := range options {
		if option.Detected {
			detected[option.ProviderID+"\x00"+option.ModelID] = true
		}
		if option.SourceModelID != "" && option.BackingDetected {
			sourceProviderID := option.SourceProviderID
			if sourceProviderID == "" {
				sourceProviderID = option.ProviderID
			}
			detected[sourceProviderID+"\x00"+option.SourceModelID] = true
		}
	}
	models := make([]config.ModelConfig, len(src))
	copy(models, src)
	slices.SortFunc(models, func(a, b config.ModelConfig) int {
		if cmp := strings.Compare(a.ProviderID, b.ProviderID); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.ModelID, b.ModelID)
	})
	out := make([]ModelConfigPreference, 0, len(models))
	for _, model := range models {
		model.ProviderID = strings.TrimSpace(model.ProviderID)
		model.ModelID = strings.TrimSpace(model.ModelID)
		model.SourceModelID = strings.TrimSpace(model.SourceModelID)
		if model.ProviderID == "" || model.ModelID == "" || model.SourceModelID == "" {
			continue
		}
		pref := modelConfigPreferenceFromConfig(model)
		sourceProviderID := pref.SourceProviderID
		if sourceProviderID == "" {
			sourceProviderID = pref.ProviderID
		}
		pref.BackingDetected = detected[sourceProviderID+"\x00"+pref.SourceModelID]
		out = append(out, pref)
	}
	return out
}

func modelConfigPreferenceFromConfig(model config.ModelConfig) ModelConfigPreference {
	custom := modelConfigIsCustom(model)
	return ModelConfigPreference{
		OriginalProviderID: model.ProviderID,
		OriginalModelID:    model.ModelID,
		ProviderID:         model.ProviderID,
		ModelID:            model.ModelID,
		SourceProviderID:   strings.TrimSpace(model.SourceProviderID),
		SourceModelID:      strings.TrimSpace(model.SourceModelID),
		Custom:             custom,
		Editable:           custom,
		ContextWindow:      model.ContextWindow,
		ModelPreset:        strings.TrimSpace(model.ModelPreset),
		Temperature:        model.Temperature,
		TopP:               model.TopP,
		MinP:               model.MinP,
		TopK:               model.TopK,
		RepeatPenalty:      model.RepeatPenalty,
		ThinkingMode:       strings.TrimSpace(model.ThinkingMode),
		ThinkingBudget:     model.ThinkingBudget,
	}
}

func modelConfigIsCustom(model config.ModelConfig) bool {
	sourceModelID := strings.TrimSpace(model.SourceModelID)
	if sourceModelID == "" {
		return false
	}
	sourceProviderID := strings.TrimSpace(model.SourceProviderID)
	if sourceProviderID == "" {
		sourceProviderID = strings.TrimSpace(model.ProviderID)
	}
	return sourceProviderID != strings.TrimSpace(model.ProviderID) || sourceModelID != strings.TrimSpace(model.ModelID)
}

func modelConfigForPair(cfg config.Config, providerID, modelID string) config.ModelConfig {
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(modelID)
	model, ok := cfg.ModelConfig(providerID, modelID)
	if !ok {
		sourceProviderID, sourceModelID := cfg.ResolveModel(providerID, modelID)
		model = config.ModelConfig{
			ProviderID:       providerID,
			ModelID:          modelID,
			SourceProviderID: sourceProviderID,
			SourceModelID:    sourceModelID,
			ContextWindow:    cfg.ContextWindow(providerID, modelID),
			ModelPreset:      cfg.ModelPreset(providerID, modelID),
			ThinkingMode:     "auto",
		}
	}
	if model.ContextWindow <= 0 {
		model.ContextWindow = cfg.ContextWindow(providerID, modelID)
	}
	return model
}

func (c *Controller) providerStateLocked() ProviderState {
	catalog := make([]ProviderCatalogItem, 0, len(provider.Catalog()))
	for _, item := range provider.Catalog() {
		catalog = append(catalog, ProviderCatalogItem{
			ID:             item.ID,
			Title:          item.Title,
			Description:    item.Description,
			DefaultBaseURL: item.DefaultBaseURL,
			ModelHint:      item.ModelHint,
			Local:          item.Local,
		})
	}

	ids := make([]string, 0, len(c.cfg.Providers))
	for id := range c.cfg.Providers {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	providers := make([]ProviderConfigItem, 0, len(ids))
	drafts := make(map[string]ProviderDraft, len(ids))
	for _, id := range ids {
		cfg := c.cfg.Providers[id]
		templateID := strings.TrimSpace(cfg.TemplateID)
		if templateID == "" {
			if draft, err := provider.BuildDraftForExisting(id, cfg); err == nil {
				templateID = draft.TemplateID
			}
		}
		providers = append(providers, ProviderConfigItem{
			ID:                      id,
			Name:                    providerEntryLabel(id, cfg),
			TemplateID:              templateID,
			Kind:                    strings.TrimSpace(cfg.Kind),
			BaseURL:                 strings.TrimSpace(cfg.BaseURL),
			Disabled:                cfg.Disabled,
			Default:                 id == c.cfg.DefaultProvider,
			PromptProgressMode:      config.NormalizePromptProgressMode(cfg.PromptProgressMode),
			PromptProgressProbed:    cfg.PromptProgressProbed,
			PromptProgressSupported: cfg.PromptProgressSupported,
		})
		if draft, err := provider.BuildDraftForExisting(id, cfg); err == nil {
			drafts[id] = providerDraftFromCatalog(draft)
		}
	}

	return ProviderState{
		DefaultProvider: strings.TrimSpace(c.cfg.DefaultProvider),
		DefaultModel:    strings.TrimSpace(c.cfg.DefaultModel),
		Catalog:         catalog,
		Providers:       providers,
		Drafts:          drafts,
	}
}

func providerDraftFromCatalog(draft provider.ConnectDraft) ProviderDraft {
	return ProviderDraft{
		OriginalProviderID:      strings.TrimSpace(draft.OriginalProviderID),
		ProviderID:              strings.TrimSpace(draft.ProviderID),
		TemplateID:              strings.TrimSpace(draft.TemplateID),
		Kind:                    strings.TrimSpace(draft.Kind),
		AuthMethod:              strings.TrimSpace(draft.AuthMethod),
		Name:                    strings.TrimSpace(draft.Name),
		BaseURL:                 strings.TrimSpace(draft.BaseURL),
		APIKey:                  strings.TrimSpace(draft.APIKey),
		APIKeyEnv:               strings.TrimSpace(draft.APIKeyEnv),
		Model:                   strings.TrimSpace(draft.Model),
		Stream:                  draft.Stream,
		Timeout:                 durationString(draft.Timeout),
		Disabled:                draft.Disabled,
		Headers:                 cloneHeaderMap(draft.Headers),
		PromptProgressMode:      config.NormalizePromptProgressMode(draft.PromptProgressMode),
		PromptProgressProbed:    draft.PromptProgressProbed,
		PromptProgressSupported: draft.PromptProgressSupported,
	}
}

func providerDraftToCatalog(draft ProviderDraft) provider.ConnectDraft {
	return provider.ConnectDraft{
		OriginalProviderID:      strings.TrimSpace(draft.OriginalProviderID),
		ProviderID:              strings.TrimSpace(draft.ProviderID),
		TemplateID:              strings.TrimSpace(draft.TemplateID),
		Kind:                    strings.TrimSpace(draft.Kind),
		AuthMethod:              strings.TrimSpace(draft.AuthMethod),
		Name:                    strings.TrimSpace(draft.Name),
		BaseURL:                 strings.TrimSpace(draft.BaseURL),
		APIKey:                  strings.TrimSpace(draft.APIKey),
		APIKeyEnv:               strings.TrimSpace(draft.APIKeyEnv),
		Model:                   strings.TrimSpace(draft.Model),
		Stream:                  draft.Stream,
		Timeout:                 parseDurationOrZero(draft.Timeout),
		Disabled:                draft.Disabled,
		Headers:                 cloneHeaderMap(draft.Headers),
		PromptProgressMode:      config.NormalizePromptProgressMode(draft.PromptProgressMode),
		PromptProgressProbed:    draft.PromptProgressProbed,
		PromptProgressSupported: draft.PromptProgressSupported,
	}
}

func cloneHeaderMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return map[string]string{}
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		dst[key] = strings.TrimSpace(value)
	}
	return dst
}

func mergeProviderEditDefaults(next *config.Provider, existing config.Provider) {
	if strings.TrimSpace(next.AuthMethod) == "" {
		next.AuthMethod = existing.AuthMethod
	}
	if strings.TrimSpace(next.APIKeyEnv) == "" {
		next.APIKeyEnv = existing.APIKeyEnv
	}
	if next.Timeout == 0 {
		next.Timeout = existing.Timeout
	}
}

func applyNewProviderDefaults(next *config.Provider) {
	next.Stream = true
	next.Timeout = 2 * time.Minute
	next.Disabled = false
	next.PromptProgressMode = "auto"
}

func applyProviderDraftPreferences(next *config.Provider, draft ProviderDraft) {
	next.AuthMethod = strings.TrimSpace(draft.AuthMethod)
	next.APIKeyEnv = strings.TrimSpace(draft.APIKeyEnv)
	if timeout := parseDurationOrZero(draft.Timeout); timeout > 0 {
		next.Timeout = timeout
	}
	next.Stream = draft.Stream
	next.Disabled = draft.Disabled
	next.PromptProgressMode = config.NormalizePromptProgressMode(draft.PromptProgressMode)
	next.PromptProgressProbed = draft.PromptProgressProbed
	next.PromptProgressSupported = draft.PromptProgressSupported
}

func browserPreferencesFromConfig(ui config.UI) BrowserPreferences {
	return BrowserPreferences{
		Theme:        normalizeTheme(ui.Theme),
		AutoContinue: ui.AutoContinue,
	}
}

func compactionPreferencesFromConfig(cfg config.Config) CompactionPreferences {
	providerID := strings.TrimSpace(cfg.CompactionProvider)
	modelID := strings.TrimSpace(cfg.CompactionModel)
	text := "Chat model"
	if providerID != "" || modelID != "" {
		text = providerID + " / " + modelID
	}
	return CompactionPreferences{
		AutoCompactAt:        cfg.AutoCompactAt,
		KeepToolCalls:        config.NormalizeCompactionKeepToolCalls(cfg.CompactionKeepToolCalls),
		ProviderID:           providerID,
		ModelID:              modelID,
		UseChatModel:         providerID == "" && modelID == "",
		CurrentSelectionText: text,
	}
}

func thinkingPreferencesFromConfig(cfg config.Config) ThinkingPreferences {
	providerID := strings.TrimSpace(cfg.Thinking.CavemanProvider)
	modelID := strings.TrimSpace(cfg.Thinking.CavemanModel)
	text := "Chat model"
	if providerID != "" || modelID != "" {
		text = providerID + " / " + modelID
	}
	return ThinkingPreferences{
		CavemanEnabled:       cfg.Thinking.CavemanEnabled,
		ProviderID:           providerID,
		ModelID:              modelID,
		UseChatModel:         providerID == "" && modelID == "",
		CavemanPrompt:        strings.TrimSpace(cfg.Thinking.CavemanPrompt),
		CurrentSelectionText: text,
	}
}

func mcpPreferencesFromConfig(src map[string]config.MCPServer) []MCPServerPreference {
	ids := make([]string, 0, len(src))
	for id := range src {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	out := make([]MCPServerPreference, 0, len(ids))
	for _, id := range ids {
		server := src[id]
		out = append(out, MCPServerPreference{
			ID:                   id,
			Name:                 strings.TrimSpace(server.Name),
			URL:                  strings.TrimSpace(server.URL),
			Headers:              cloneHeaderMap(server.Headers),
			Disabled:             server.Disabled,
			StartupTimeout:       durationString(server.StartupTimeout),
			RequestTimeout:       durationString(server.RequestTimeout),
			DisableStandaloneSSE: server.DisableStandaloneSSE,
			BearerToken:          strings.TrimSpace(server.BearerToken),
			BearerTokenEnv:       strings.TrimSpace(server.BearerTokenEnv),
		})
	}
	return out
}

func accessPreferencesFromConfig(src accesssettings.Settings) AccessPreferences {
	return AccessPreferences{
		Settings: accesssettings.Normalize(src),
		Presets:  accesssettings.Presets(),
	}
}

func toolDefaultPreferencesFromConfig(src map[domain.ToolKind]bool) []ToolDefaultPreference {
	kinds := toolkind.KindValues()
	out := make([]ToolDefaultPreference, 0, len(kinds))
	for _, kind := range kinds {
		if hideToolDefault(kind) {
			continue
		}
		enabled := true
		if value, ok := src[kind]; ok {
			enabled = value
		}
		group, groupLabel := toolDefaultGroup(kind)
		out = append(out, ToolDefaultPreference{
			Tool:       kind,
			Enabled:    enabled,
			Label:      kind.String(),
			Group:      group,
			GroupLabel: groupLabel,
		})
	}
	return out
}

func hideToolDefault(kind domain.ToolKind) bool {
	switch kind {
	case domain.ToolKindMilestonePlan, domain.ToolKindMilestoneWrite, domain.ToolKindTaskAddItems, domain.ToolKindTaskUpdateItem, domain.ToolKindChatPoll, domain.ToolKindChatStartDecomposition, domain.ToolKindChatStartExecution:
		return true
	default:
		return false
	}
}

func toolDefaultGroup(kind domain.ToolKind) (string, string) {
	switch kind {
	case domain.ToolKindFileRead, domain.ToolKindFileWrite, domain.ToolKindFileEdit, domain.ToolKindFileGrep, domain.ToolKindFileGlob:
		return "file", "File"
	case domain.ToolKindWebFetch, domain.ToolKindWebSearch:
		return "web", "Web"
	case domain.ToolKindExecCommand, domain.ToolKindExecStatus, domain.ToolKindExecList, domain.ToolKindExecWriteStdin, domain.ToolKindExecResize, domain.ToolKindExecTerminate, domain.ToolKindExecCleanup:
		return "exec", "Exec"
	case domain.ToolKindChatList, domain.ToolKindChatStart, domain.ToolKindChatSend, domain.ToolKindChatCancel, domain.ToolKindChatArchive, domain.ToolKindChatRename, domain.ToolKindChatPoll, domain.ToolKindChatStartDecomposition, domain.ToolKindChatStartExecution:
		return "chat", "Chat"
	case domain.ToolKindMilestoneList, domain.ToolKindMilestoneAdd, domain.ToolKindMilestoneUpdate, domain.ToolKindMilestonePlan, domain.ToolKindMilestoneWrite:
		return "milestone", "Milestone"
	case domain.ToolKindTaskList, domain.ToolKindTaskAddItems, domain.ToolKindTaskUpdateItem, domain.ToolKindTaskFetchNext, domain.ToolKindTasksAdd, domain.ToolKindTasksUpdate:
		return "task", "Task"
	case domain.ToolKindViewImage, domain.ToolKindShowImage:
		return "image", "Image"
	default:
		key := kind.String()
		return key, kind.DisplayName()
	}
}

func applyGeneralPreferences(cfg *config.Config, prefs GeneralPreferences) error {
	cfg.DefaultProvider = strings.TrimSpace(prefs.DefaultProvider)
	cfg.DefaultModel = strings.TrimSpace(prefs.DefaultModel)
	if cfg.DefaultProvider != "" && !cfg.HasUsableProvider(cfg.DefaultProvider) {
		return fmt.Errorf("default provider %q is not configured or is disabled", cfg.DefaultProvider)
	}
	if prefs.MaxToolLoopSteps <= 0 {
		return fmt.Errorf("max tool loop steps must be greater than zero")
	}
	cfg.MaxToolLoopSteps = prefs.MaxToolLoopSteps
	if backend := strings.TrimSpace(prefs.StoreBackend); backend != "" {
		cfg.Store.Backend = backend
	}
	return nil
}

func repairStaleGeneralProvider(cfg *config.Config, prefs *PreferencesState) {
	if prefs == nil {
		return
	}
	defaultProvider := strings.TrimSpace(prefs.Providers.DefaultProvider)
	if defaultProvider == "" {
		return
	}
	if !cfg.HasUsableProvider(defaultProvider) {
		return
	}
	if cfg.HasUsableProvider(strings.TrimSpace(prefs.General.DefaultProvider)) {
		return
	}
	prefs.General.DefaultProvider = defaultProvider
	prefs.General.DefaultModel = strings.TrimSpace(prefs.Providers.DefaultModel)
}

func applyModelConfigPreferences(cfg *config.Config, prefs []ModelConfigPreference) error {
	next := make([]config.ModelConfig, 0, len(prefs))
	for _, pref := range prefs {
		providerID := strings.TrimSpace(pref.ProviderID)
		modelID := strings.TrimSpace(pref.ModelID)
		if providerID == "" && modelID == "" {
			continue
		}
		if providerID == "" || modelID == "" {
			return fmt.Errorf("model provider and model id are required")
		}
		if !cfg.HasUsableProvider(providerID) {
			continue
		}
		model, err := configModelFromPreference(pref)
		if err != nil {
			return err
		}
		if sourceProviderID := strings.TrimSpace(model.SourceProviderID); sourceProviderID != "" && !cfg.HasUsableProvider(sourceProviderID) {
			return fmt.Errorf("source provider %q is not configured", sourceProviderID)
		}
		next = append(next, model)
	}
	cfg.Models = nil
	for _, model := range next {
		cfg.SetModelConfig(model)
	}
	return nil
}

func configModelFromPreference(pref ModelConfigPreference) (config.ModelConfig, error) {
	providerID := strings.TrimSpace(pref.ProviderID)
	modelID := strings.TrimSpace(pref.ModelID)
	sourceProviderID := strings.TrimSpace(pref.SourceProviderID)
	sourceModelID := strings.TrimSpace(pref.SourceModelID)
	if sourceModelID != "" && sourceProviderID == "" {
		sourceProviderID = providerID
	}
	if pref.ContextWindow <= 0 {
		return config.ModelConfig{}, fmt.Errorf("context window for %s/%s must be greater than zero", providerID, modelID)
	}
	for name, value := range map[string]*float64{
		"temperature":    pref.Temperature,
		"top_p":          pref.TopP,
		"min_p":          pref.MinP,
		"repeat_penalty": pref.RepeatPenalty,
	} {
		if value != nil && *value < 0 {
			return config.ModelConfig{}, fmt.Errorf("%s for %s/%s must not be negative", name, providerID, modelID)
		}
	}
	if pref.TopK < 0 {
		return config.ModelConfig{}, fmt.Errorf("top_k for %s/%s must not be negative", providerID, modelID)
	}
	if pref.ThinkingBudget < 0 {
		return config.ModelConfig{}, fmt.Errorf("thinking budget for %s/%s must not be negative", providerID, modelID)
	}
	return config.ModelConfig{
		ProviderID:       providerID,
		ModelID:          modelID,
		SourceProviderID: sourceProviderID,
		SourceModelID:    sourceModelID,
		ContextWindow:    pref.ContextWindow,
		ModelPreset:      strings.TrimSpace(pref.ModelPreset),
		Temperature:      pref.Temperature,
		TopP:             pref.TopP,
		MinP:             pref.MinP,
		TopK:             pref.TopK,
		RepeatPenalty:    pref.RepeatPenalty,
		ThinkingMode:     strings.TrimSpace(pref.ThinkingMode),
		ThinkingBudget:   pref.ThinkingBudget,
	}, nil
}

func applyBrowserPreferences(cfg *config.Config, prefs BrowserPreferences) error {
	cfg.UI = config.UI{
		Theme:        normalizeTheme(prefs.Theme),
		AutoContinue: prefs.AutoContinue,
	}
	return nil
}

func applyCompactionPreferences(cfg *config.Config, prefs CompactionPreferences) error {
	if prefs.AutoCompactAt <= 0 {
		return fmt.Errorf("auto compact threshold must be greater than zero")
	}
	cfg.AutoCompactAt = prefs.AutoCompactAt
	cfg.CompactionKeepToolCalls = config.NormalizeCompactionKeepToolCalls(prefs.KeepToolCalls)
	if prefs.UseChatModel {
		cfg.CompactionProvider = ""
		cfg.CompactionModel = ""
		return nil
	}
	providerID := strings.TrimSpace(prefs.ProviderID)
	modelID := strings.TrimSpace(prefs.ModelID)
	if providerID == "" && modelID == "" {
		cfg.CompactionProvider = ""
		cfg.CompactionModel = ""
		return nil
	}
	if providerID == "" || modelID == "" {
		return fmt.Errorf("compaction provider and model must both be set, or both empty for chat model")
	}
	if !cfg.HasUsableProvider(providerID) {
		return fmt.Errorf("compaction provider %q is not configured or is disabled", providerID)
	}
	cfg.CompactionProvider = providerID
	cfg.CompactionModel = modelID
	return nil
}

func applyThinkingPreferences(cfg *config.Config, prefs ThinkingPreferences) error {
	cfg.Thinking.CavemanEnabled = prefs.CavemanEnabled
	cfg.Thinking.CavemanPrompt = strings.TrimSpace(prefs.CavemanPrompt)
	if cfg.Thinking.CavemanPrompt == "" {
		cfg.Thinking.CavemanPrompt = config.DefaultCavemanThinkingPrompt
	}
	if prefs.UseChatModel {
		cfg.Thinking.CavemanProvider = ""
		cfg.Thinking.CavemanModel = ""
		return nil
	}
	providerID := strings.TrimSpace(prefs.ProviderID)
	modelID := strings.TrimSpace(prefs.ModelID)
	if providerID == "" && modelID == "" {
		cfg.Thinking.CavemanProvider = ""
		cfg.Thinking.CavemanModel = ""
		return nil
	}
	if providerID == "" || modelID == "" {
		return fmt.Errorf("thinking provider and model must both be set, or both empty for chat model")
	}
	if !cfg.HasUsableProvider(providerID) {
		return fmt.Errorf("thinking provider %q is not configured or is disabled", providerID)
	}
	cfg.Thinking.CavemanProvider = providerID
	cfg.Thinking.CavemanModel = modelID
	return nil
}

func applyMCPPreferences(cfg *config.Config, prefs []MCPServerPreference) error {
	next := map[string]config.MCPServer{}
	for _, item := range prefs {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		startup := parseDurationOrZero(item.StartupTimeout)
		request := parseDurationOrZero(item.RequestTimeout)
		next[id] = config.MCPServer{
			Name:                 strings.TrimSpace(item.Name),
			URL:                  strings.TrimSpace(item.URL),
			Headers:              cloneHeaderMap(item.Headers),
			Disabled:             item.Disabled,
			StartupTimeout:       startup,
			RequestTimeout:       request,
			DisableStandaloneSSE: item.DisableStandaloneSSE,
			BearerToken:          strings.TrimSpace(item.BearerToken),
			BearerTokenEnv:       strings.TrimSpace(item.BearerTokenEnv),
		}
	}
	cfg.MCPServers = next
	return nil
}

func applyAccessPreferences(cfg *config.Config, prefs AccessPreferences) error {
	settings := prefs.Settings
	if accesssettings.IsZero(settings) {
		settings = accesssettings.Default()
	}
	settings = accesssettings.Normalize(settings)
	if err := accesssettings.Validate(settings); err != nil {
		return err
	}
	cfg.Access = settings
	return nil
}

func applyToolDefaultPreferences(cfg *config.Config, prefs []ToolDefaultPreference) {
	next := map[domain.ToolKind]bool{}
	for _, item := range prefs {
		next[item.Tool] = item.Enabled
	}
	for _, kind := range toolkind.KindValues() {
		if _, ok := next[kind]; !ok {
			next[kind] = true
		}
	}
	cfg.ToolDefaults = next
}

func promptPreferences() ([]PromptPreference, error) {
	out := make([]PromptPreference, 0, 2)
	for _, target := range []string{"system-prompt.md", "compaction-prompt.md"} {
		item, err := promptPreference(target)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func promptPreference(target string) (PromptPreference, error) {
	path, err := managedPromptPath(target)
	if err != nil {
		return PromptPreference{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		data, err = assets.DefaultContent(target)
		if err != nil {
			return PromptPreference{}, err
		}
	}
	return PromptPreference{
		Name:    strings.TrimSuffix(target, ".md"),
		Target:  target,
		Path:    path,
		Content: string(data),
	}, nil
}

func writePromptPreferences(prompts []PromptPreference) error {
	for _, prompt := range prompts {
		target := strings.TrimSpace(prompt.Target)
		if target != "system-prompt.md" && target != "compaction-prompt.md" {
			continue
		}
		path, err := managedPromptPath(target)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("create prompt dir: %w", err)
		}
		if err := os.WriteFile(path, []byte(prompt.Content), 0o644); err != nil {
			return fmt.Errorf("write prompt %s: %w", target, err)
		}
	}
	return nil
}

func managedPromptPath(target string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", fmt.Errorf("locate home directory for prompt assets: %w", err)
	}
	return filepath.Join(home, ".koder", target), nil
}

func normalizeTheme(theme string) string {
	theme = strings.ToLower(strings.TrimSpace(theme))
	if theme != "dark" && theme != "light" {
		return "auto"
	}
	return theme
}

func durationString(value time.Duration) string {
	if value <= 0 {
		return ""
	}
	return value.String()
}

func parseDurationOrZero(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0
	}
	return duration
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

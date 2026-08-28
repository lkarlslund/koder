package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/agent"
	"github.com/lkarlslund/koder/internal/assets"
	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/browserapi"
	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/mcp"
	"github.com/lkarlslund/koder/internal/modeloverlay"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/skills"
	"github.com/lkarlslund/koder/internal/tools"
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
	started := time.Now()
	result, err := provider.Probe(ctx, providerDraftToCatalog(draft), nil)
	if err != nil {
		c.recordProviderProbe(draft.ProviderID, nil, started, err)
		return ProviderProbeResult{}, err
	}
	c.recordProviderProbe(draft.ProviderID, result.Models, started, nil)
	models := make([]string, 0, len(result.Models))
	for _, item := range result.Models {
		models = append(models, item.ID)
	}
	return ProviderProbeResult{
		ModelCount:              len(result.Models),
		Models:                  models,
		Capabilities:            providerProbeCapabilities(result.Models),
		MaxContextWindow:        providerProbeMaxContextWindow(result.Models),
		SelectedModel:           result.SelectedModel,
		PromptProgressProbed:    result.PromptProgressProbed,
		PromptProgressSupported: result.PromptProgressSupported,
		PromptProgressCheckedAt: timePointer(result.PromptProgressCheckedAt),
	}, nil
}

func providerProbeCapabilities(models []domain.Model) []string {
	var chat, stt, tts, tools, images, pdfs, jsonOutput, reasoning, hasUnknown bool
	for _, model := range models {
		chat = chat || model.SupportsChat
		stt = stt || model.SupportsSTT
		tts = tts || model.SupportsTTS
		tools = tools || model.SupportsTools
		images = images || model.SupportsImages
		pdfs = pdfs || model.SupportsPDFs
		jsonOutput = jsonOutput || model.SupportsJSON
		reasoning = reasoning || model.SupportsReasoning
		hasUnknown = hasUnknown || !model.CapabilitiesKnown
	}
	// OpenAI-compatible model listings often omit capability metadata. Such a
	// provider is still chat-capable when its connection probe succeeds unless
	// every advertised model explicitly identifies another role (for example,
	// a TTS-only endpoint).
	chat = chat || hasUnknown
	capabilities := make([]string, 0, 8)
	for _, item := range []struct {
		label   string
		enabled bool
	}{
		{label: "Chat", enabled: chat},
		{label: "STT", enabled: stt},
		{label: "Tools", enabled: tools},
		{label: "Vision", enabled: images},
		{label: "PDF", enabled: pdfs},
		{label: "JSON", enabled: jsonOutput},
		{label: "Reasoning", enabled: reasoning},
		{label: "TTS", enabled: tts},
	} {
		if item.enabled {
			capabilities = append(capabilities, item.label)
		}
	}
	return capabilities
}

func providerProbeMaxContextWindow(models []domain.Model) int {
	maximum := 0
	for _, model := range models {
		maximum = max(maximum, model.ContextWindow, model.MaxContextWindow)
	}
	return maximum
}

// SaveProvider validates and persists a provider draft.
func (c *Controller) SaveProvider(ctx context.Context, draft ProviderDraft) (ProviderState, error) {
	catalogDraft := providerDraftToCatalog(draft)
	if err := provider.ValidateDraft(catalogDraft); err != nil {
		return ProviderState{}, err
	}
	started := time.Now()
	probe, err := provider.Probe(ctx, catalogDraft, nil)
	if err != nil {
		c.recordProviderProbe(catalogDraft.ProviderID, nil, started, err)
		return ProviderState{}, err
	}
	c.recordProviderProbe(catalogDraft.ProviderID, probe.Models, started, nil)
	catalogDraft.Model = probe.SelectedModel
	catalogDraft.PromptProgressProbed = probe.PromptProgressProbed
	catalogDraft.PromptProgressSupported = probe.PromptProgressSupported
	catalogDraft.PromptProgressCheckedAt = probe.PromptProgressCheckedAt
	catalogDraft.PromptProgressTarget = probe.PromptProgressTarget
	draft.PromptProgressProbed = probe.PromptProgressProbed
	draft.PromptProgressSupported = probe.PromptProgressSupported
	draft.PromptProgressCheckedAt = timePointer(probe.PromptProgressCheckedAt)
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
	if strings.TrimSpace(c.cfg.Defaults.ProviderID) == "" || c.cfg.Defaults.ProviderID == originalID || c.cfg.Defaults.ProviderID == catalogDraft.ProviderID {
		c.cfg.Defaults.ProviderID = catalogDraft.ProviderID
		c.cfg.Defaults.ModelID = catalogDraft.Model
	}
	if err := c.cfg.Save(); err != nil {
		c.mu.Unlock()
		return ProviderState{}, err
	}
	if c.agent != nil {
		c.agent.UpdateConfig(c.cfg)
	}
	state := c.providerStateLocked()
	c.mu.Unlock()
	c.broadcast("snapshot", c.State())
	return state, nil
}

func (c *Controller) recordProviderProbe(providerID string, models []domain.Model, started time.Time, probeErr error) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return
	}
	modelIDs := make([]string, 0, len(models))
	for _, model := range models {
		modelIDs = append(modelIDs, model.ID)
	}
	c.providerHealth.ObserveModels(providerID, modelIDs, started, probeErr)
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
	nextDefault := strings.TrimSpace(c.cfg.Defaults.ProviderID)
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
	c.cfg.Defaults.ProviderID = nextDefault
	c.cfg.Defaults.ModelID = ""
	if nextDefault != "" {
		c.cfg.Defaults.ModelID = firstModelForProvider(c.cfg, nextDefault)
	}
	if err := c.cfg.Save(); err != nil {
		c.mu.Unlock()
		return ProviderState{}, err
	}
	if c.agent != nil {
		c.agent.UpdateConfig(c.cfg)
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
	oldModels := slices.Clone(next.Models)
	repairStaleGeneralProvider(&next, &prefs)
	if err := applyGeneralPreferences(&next, prefs.General); err != nil {
		c.mu.Unlock()
		return PreferencesState{}, err
	}
	if err := applyBrowserPreferences(&next, prefs.UI); err != nil {
		c.mu.Unlock()
		return PreferencesState{}, err
	}
	if err := applyNativeBrowserPreferences(&next, prefs.Browser); err != nil {
		c.mu.Unlock()
		return PreferencesState{}, err
	}
	if err := applyCodexPreferences(&next, prefs.Codex); err != nil {
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
	repairRemovedCustomModelReferences(ctx, &next, oldModels)
	if err := applyMCPPreferences(&next, prefs.MCPServers); err != nil {
		c.mu.Unlock()
		return PreferencesState{}, err
	}
	if err := applyAccessPreferences(&next, prefs.Access); err != nil {
		c.mu.Unlock()
		return PreferencesState{}, err
	}
	applyToolDefaultPreferences(&next, prefs.ToolDefaults)
	applyBrowserToolDefaults(&next)
	if err := applySkillsPreferences(&next, prefs.Skills); err != nil {
		c.mu.Unlock()
		return PreferencesState{}, err
	}
	if err := writePromptPreferences(next.ManagedAssetsDir(), prefs.Prompts); err != nil {
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

func (c *Controller) SetTTSEnabled(enabled bool) (TTSPreferences, error) {
	c.mu.Lock()
	c.cfg.UI.TTS.Enabled = enabled
	if err := c.cfg.Save(); err != nil {
		c.mu.Unlock()
		return TTSPreferences{}, err
	}
	if c.agent != nil {
		c.agent.UpdateConfig(c.cfg)
	}
	prefs := ttsPreferencesFromConfig(c.cfg.UI.TTS)
	c.mu.Unlock()
	c.broadcast("tts", prefs)
	return prefs, nil
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
	path, err := managedPromptPath(c.cfg.ManagedAssetsDir(), target)
	if err != nil {
		return PromptPreference{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return PromptPreference{}, fmt.Errorf("create prompt dir: %w", err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return PromptPreference{}, fmt.Errorf("write prompt %s: %w", target, err)
	}
	return promptPreference(c.cfg.ManagedAssetsDir(), target)
}

// ModelOptionsForSelection lists selectable models across configured providers,
// marking the model for the explicitly selected chat when one is selected.
func (c *Controller) ModelOptionsForSelection(ctx context.Context, selection Selection) ([]ModelOption, error) {
	c.mu.RLock()
	cfg := c.cfg
	c.mu.RUnlock()
	currentProvider := ""
	currentModel := ""
	if selection.SessionID != "" && selection.ChatID != "" {
		_, _, chatRecord, _, err := c.resolveStateRuntime(ctx, selection)
		if err != nil {
			return nil, err
		}
		currentProvider = strings.TrimSpace(chatRecord.ProviderID)
		currentModel = strings.TrimSpace(chatRecord.ModelID)
	}
	options, err := c.discoverModelOptions(ctx, cfg, currentProvider, currentModel)
	if err != nil {
		return nil, err
	}
	return options, nil
}

// SynthesizeSpeech renders text with the configured TTS model.
func (c *Controller) SynthesizeSpeech(ctx context.Context, text string) (TTSSpeech, error) {
	return c.SynthesizeSpeechWithTTS(ctx, text, nil)
}

// SynthesizeSpeechWithTTS renders text with optional unsaved TTS preferences.
func (c *Controller) SynthesizeSpeechWithTTS(ctx context.Context, text string, prefs *TTSPreferences) (TTSSpeech, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return TTSSpeech{}, fmt.Errorf("tts text is required")
	}
	c.mu.RLock()
	cfg := c.cfg
	c.mu.RUnlock()
	if prefs != nil {
		tts, err := configTTSFromPreference(*prefs)
		if err != nil {
			return TTSSpeech{}, err
		}
		cfg.UI.TTS = tts
	}
	sourceProviderID := strings.TrimSpace(cfg.UI.TTS.ProviderID)
	sourceModelID := strings.TrimSpace(cfg.UI.TTS.ModelID)
	if sourceProviderID == "" || sourceModelID == "" {
		var err error
		sourceProviderID, sourceModelID, err = firstDetectedTTSModel(ctx, cfg, c.providerHealth)
		if err != nil {
			return TTSSpeech{}, err
		}
	}
	providerCfg, ok := cfg.Provider(sourceProviderID)
	if !ok || providerCfg.Disabled {
		return TTSSpeech{}, fmt.Errorf("tts provider %q is not configured", sourceProviderID)
	}
	client, err := provider.New(sourceProviderID, providerCfg, nil, c.providerHealth)
	if err != nil {
		return TTSSpeech{}, err
	}
	speech, err := client.CreateSpeech(ctx, provider.SpeechRequest{
		Model:          sourceModelID,
		Input:          text,
		Voice:          cfg.UI.TTS.Voice,
		ResponseFormat: cfg.UI.TTS.ResponseFormat,
		Speed:          cfg.UI.TTS.Speed,
	})
	if err != nil {
		return TTSSpeech{}, err
	}
	contentType, audio := playableSpeechAudio(speech.ContentType, speech.Audio, cfg.UI.TTS.PCMSampleRate)
	return TTSSpeech{
		ProviderID:  sourceProviderID,
		ModelID:     sourceModelID,
		ContentType: contentType,
		Audio:       audio,
	}, nil
}

func firstDetectedTTSModel(ctx context.Context, cfg config.Config, healthTrackers ...*provider.HealthTracker) (string, string, error) {
	var health *provider.HealthTracker
	if len(healthTrackers) > 0 {
		health = healthTrackers[0]
	}
	options, err := discoverModelOptionsForConfig(ctx, cfg, "", "", health)
	if err != nil {
		return "", "", err
	}
	for _, option := range options {
		if !option.SupportsTTS {
			continue
		}
		providerID := strings.TrimSpace(option.SourceProviderID)
		modelID := strings.TrimSpace(option.SourceModelID)
		if providerID == "" {
			providerID = strings.TrimSpace(option.ProviderID)
		}
		if modelID == "" {
			modelID = strings.TrimSpace(option.ModelID)
		}
		if providerID != "" && modelID != "" {
			return providerID, modelID, nil
		}
	}
	return "", "", fmt.Errorf("no configured tts model detected")
}

func playableSpeechAudio(contentType string, audio []byte, sampleRate int) (string, []byte) {
	normalized := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if normalized != "audio/pcm" && normalized != "application/octet-stream" {
		return contentType, audio
	}
	if sampleRate <= 0 {
		sampleRate = 24000
	}
	return "audio/wav", wavFromPCM16(audio, sampleRate, 1)
}

func wavFromPCM16(pcm []byte, sampleRate int, channels int) []byte {
	if channels <= 0 {
		channels = 1
	}
	var out bytes.Buffer
	dataSize := uint32(len(pcm))
	byteRate := uint32(sampleRate * channels * 2)
	blockAlign := uint16(channels * 2)
	out.WriteString("RIFF")
	_ = binary.Write(&out, binary.LittleEndian, uint32(36)+dataSize)
	out.WriteString("WAVEfmt ")
	_ = binary.Write(&out, binary.LittleEndian, uint32(16))
	_ = binary.Write(&out, binary.LittleEndian, uint16(1))
	_ = binary.Write(&out, binary.LittleEndian, uint16(channels))
	_ = binary.Write(&out, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(&out, binary.LittleEndian, byteRate)
	_ = binary.Write(&out, binary.LittleEndian, blockAlign)
	_ = binary.Write(&out, binary.LittleEndian, uint16(16))
	out.WriteString("data")
	_ = binary.Write(&out, binary.LittleEndian, dataSize)
	out.Write(pcm)
	return out.Bytes()
}

func (c *Controller) modelOptionsLocked(ctx context.Context) ([]ModelOption, error) {
	return c.discoverModelOptions(ctx, c.cfg, "", "")
}

func modelOptionsForConfig(ctx context.Context, cfg config.Config, currentProvider, currentModel string) ([]ModelOption, error) {
	return discoverModelOptionsForConfig(ctx, cfg, currentProvider, currentModel, nil)
}

func (c *Controller) discoverModelOptions(ctx context.Context, cfg config.Config, currentProvider, currentModel string) ([]ModelOption, error) {
	return discoverModelOptionsForConfig(ctx, cfg, currentProvider, currentModel, c.providerHealth)
}

func discoverModelOptionsForConfig(ctx context.Context, cfg config.Config, currentProvider, currentModel string, health *provider.HealthTracker) ([]ModelOption, error) {
	seen := map[string]struct{}{}
	detected := map[string]domain.Model{}
	options := make([]ModelOption, 0, len(cfg.Providers))
	capabilities := provider.NewCapabilityStore(cfg.StateDir())
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
			ProviderID:             providerID,
			ProviderLabel:          providerEntryLabel(providerID, providerCfg),
			ModelID:                modelID,
			SourceProviderID:       providerID,
			SourceModelID:          modelID,
			OwnedBy:                strings.TrimSpace(model.OwnedBy),
			ContextWindow:          model.ContextWindow,
			MaxContextWindow:       model.MaxContextWindow,
			MaxOutputTokens:        model.MaxOutputTokens,
			MetadataSource:         strings.TrimSpace(model.MetadataSource),
			SupportsChat:           model.SupportsChat,
			SupportsSTT:            model.SupportsSTT,
			SupportsTTS:            model.SupportsTTS,
			SupportsTools:          model.SupportsTools,
			SupportsImages:         model.SupportsImages,
			SupportsPDFs:           model.SupportsPDFs,
			SupportsJSON:           model.SupportsJSON,
			SupportsReasoning:      model.SupportsReasoning,
			ReasoningEfforts:       append([]string(nil), model.ReasoningEfforts...),
			DefaultReasoningEffort: model.DefaultReasoningEffort,
			CapabilitiesKnown:      model.CapabilitiesKnown,
			CapabilitySource:       strings.TrimSpace(model.CapabilitySource),
			Detected:               true,
			BackingDetected:        true,
			Editable:               false,
			Current:                providerID == currentProvider && modelID == currentModel,
			Default:                providerID == cfg.Defaults.ProviderID && modelID == cfg.Defaults.ModelID,
			Health:                 health.Model(providerID, modelID),
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
		started := time.Now()
		client, err := provider.New(providerID, providerCfg, nil, health)
		if err != nil {
			failures = append(failures, providerID)
			health.Observe(providerID, "", "create_client", started, err)
			continue
		}
		models, err := client.ListModels(ctx)
		if err != nil {
			failures = append(failures, providerID)
			continue
		}
		for _, model := range models {
			if enriched, err := capabilities.EnrichModel(providerID, providerCfg, model); err == nil {
				model = enriched
			}
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
		modelRuntimeHealth := health.Model(sourceProviderID, model.SourceModelID)
		if !sourceDetected {
			checkedAt := time.Now()
			modelRuntimeHealth = RuntimeHealth{Status: provider.HealthUnhealthy, Detail: "Backing model is not available", Operation: "list_models", CheckedAt: &checkedAt}
		}
		options = append(options, ModelOption{
			ProviderID:             model.ProviderID,
			ProviderLabel:          providerEntryLabel(model.ProviderID, providerCfg),
			ModelID:                model.ModelID,
			SourceProviderID:       sourceProviderID,
			SourceModelID:          model.SourceModelID,
			OwnedBy:                strings.TrimSpace(source.OwnedBy),
			ContextWindow:          model.ContextWindow,
			SupportsChat:           source.SupportsChat || !source.CapabilitiesKnown,
			SupportsSTT:            source.SupportsSTT,
			SupportsTTS:            source.SupportsTTS,
			SupportsTools:          source.SupportsTools,
			SupportsImages:         source.SupportsImages,
			SupportsPDFs:           source.SupportsPDFs,
			SupportsJSON:           source.SupportsJSON,
			SupportsReasoning:      source.SupportsReasoning,
			ReasoningEfforts:       append([]string(nil), source.ReasoningEfforts...),
			DefaultReasoningEffort: source.DefaultReasoningEffort,
			CapabilitiesKnown:      source.CapabilitiesKnown,
			CapabilitySource:       strings.TrimSpace(source.CapabilitySource),
			Custom:                 true,
			BackingDetected:        sourceDetected,
			Editable:               true,
			Current:                model.ProviderID == currentProvider && model.ModelID == currentModel,
			Default:                model.ProviderID == cfg.Defaults.ProviderID && model.ModelID == cfg.Defaults.ModelID,
			Health:                 modelRuntimeHealth,
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

// SetDefaultModel persists the system model used by creation fallbacks and
// dynamic default-model chat assignments.
func (c *Controller) SetDefaultModel(ctx context.Context, providerID, modelID string) (PreferencesState, error) {
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(modelID)
	if providerID == "" {
		return PreferencesState{}, fmt.Errorf("provider id is required")
	}
	if modelID == "" {
		return PreferencesState{}, fmt.Errorf("model id is required")
	}
	c.mu.RLock()
	cfg := c.cfg
	c.mu.RUnlock()
	if !cfg.HasUsableProvider(providerID) {
		return PreferencesState{}, fmt.Errorf("provider %q is not configured", providerID)
	}
	options, err := modelOptionsForConfig(ctx, cfg, "", "")
	if err != nil {
		return PreferencesState{}, err
	}
	found := false
	for _, option := range options {
		if option.ProviderID == providerID && option.ModelID == modelID {
			if !selectableChatModel(option) {
				return PreferencesState{}, fmt.Errorf("model %s/%s is not currently available for chat", providerID, modelID)
			}
			found = true
			break
		}
	}
	if !found {
		return PreferencesState{}, fmt.Errorf("model %s/%s is not configured or detected", providerID, modelID)
	}
	c.mu.Lock()
	c.cfg.Defaults.ProviderID = providerID
	c.cfg.Defaults.ModelID = modelID
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
	return state, nil
}

// DeleteModelConfig deletes a custom model configuration.
func (c *Controller) DeleteModelConfig(ctx context.Context, providerID, modelID string) (PreferencesState, error) {
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(modelID)
	if providerID == "" {
		return PreferencesState{}, fmt.Errorf("provider id is required")
	}
	if modelID == "" {
		return PreferencesState{}, fmt.Errorf("model id is required")
	}
	c.mu.RLock()
	cfg := c.cfg
	model, ok := cfg.ModelConfig(providerID, modelID)
	c.mu.RUnlock()
	if !ok {
		return PreferencesState{}, fmt.Errorf("model %s/%s is not configured", providerID, modelID)
	}
	if !modelConfigIsCustom(model) {
		return PreferencesState{}, fmt.Errorf("only custom model configurations can be deleted")
	}
	next := cfg
	next.Models = slices.Clone(cfg.Models)
	removeModelConfig(&next, providerID, modelID)
	repairDeletedModelReferences(ctx, &next, providerID, modelID)
	c.mu.Lock()
	c.cfg = next
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
	return state, nil
}

// ModelConfig returns editable settings for a provider/model pair.
func (c *Controller) ModelConfig(ctx context.Context, providerID, modelID string) ModelConfigPreference {
	c.mu.RLock()
	cfg := c.cfg
	c.mu.RUnlock()
	model := modelConfigForPair(cfg, providerID, modelID)
	pref := modelConfigPreferenceFromConfig(model)
	catalog := modeloverlay.Load(cfg.ManagedAssetsDir())
	pref = decorateModelConfigPreference(cfg, catalog, pref)
	pref.ModelOverlays = &catalog
	if pref.Custom {
		return pref
	}
	sourceProviderID := strings.TrimSpace(pref.SourceProviderID)
	if sourceProviderID == "" {
		sourceProviderID = strings.TrimSpace(pref.ProviderID)
	}
	sourceModelID := strings.TrimSpace(pref.SourceModelID)
	if sourceModelID == "" {
		sourceModelID = strings.TrimSpace(pref.ModelID)
	}
	providerCfg, ok := cfg.Provider(sourceProviderID)
	if !ok || !provider.SupportsContextWindowDetection(providerCfg) {
		return pref
	}
	if detected, err := provider.DetectContextWindow(ctx, sourceProviderID, providerCfg, sourceModelID, nil); err == nil && detected > 0 {
		pref.ContextWindow = detected
	}
	return pref
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
	if err := validateModelOverlay(c.cfg, model); err != nil {
		c.mu.Unlock()
		return ModelConfigPreference{}, err
	}
	c.cfg.SetModelConfig(model)
	if err := c.cfg.Save(); err != nil {
		c.mu.Unlock()
		return ModelConfigPreference{}, err
	}
	if c.agent != nil {
		c.agent.UpdateConfig(c.cfg)
	}
	saved := decorateModelConfigPreference(c.cfg, modeloverlay.Load(c.cfg.ManagedAssetsDir()), modelConfigPreferenceFromConfig(modelConfigForPair(c.cfg, providerID, modelID)))
	catalog := modeloverlay.Load(c.cfg.ManagedAssetsDir())
	saved.ModelOverlays = &catalog
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
	c.mu.RLock()
	cfg := c.cfg
	c.mu.RUnlock()
	if !cfg.HasUsableProvider(providerID) {
		return fmt.Errorf("provider %q is not configured", providerID)
	}
	options, err := modelOptionsForConfig(ctx, cfg, "", "")
	if err != nil {
		return err
	}
	found := false
	for _, option := range options {
		if option.ProviderID != providerID || option.ModelID != modelID {
			continue
		}
		if !selectableChatModel(option) {
			return &ModelUnavailableError{ProviderID: providerID, ModelID: modelID}
		}
		found = true
		break
	}
	if !found {
		return fmt.Errorf("model %s/%s is not detected or customized", providerID, modelID)
	}
	c.ensureModelConfig(ctx, providerID, modelID)
	owner, _, chatRecord, rt, err := c.resolveSelectedRuntime(ctx, selection, true)
	if err != nil {
		return err
	}
	if chatRecord.RequiresImages {
		if err := c.ensureModelSupportsImages(ctx, providerID, modelID); err != nil {
			return err
		}
	}
	chatRecord, err = owner.SetChatModel(ctx, chatRecord.ID, providerID, modelID)
	if err != nil {
		return err
	}
	session := owner.Snapshot().Session
	rt.SetChat(chatRecord)
	rt.SetSession(session)
	return nil
}

// SetModelForSelectionResult returns metadata for the model accepted by the
// mutation itself. RPC acknowledgements use this instead of re-reading a
// concurrently broadcast session snapshot that may still describe the prior
// model.
func (c *Controller) SetModelForSelectionResult(ctx context.Context, selection Selection, providerID, modelID string) (ModelInfo, int, error) {
	if err := c.SetModelForSelection(ctx, selection, providerID, modelID); err != nil {
		return ModelInfo{}, 0, err
	}
	chatRecord := domain.Chat{ProviderID: strings.TrimSpace(providerID), ModelID: strings.TrimSpace(modelID)}
	return c.modelInfoForChat(chatRecord), c.contextWindowForChat(chatRecord), nil
}

func (c *Controller) ensureModelSupportsImages(ctx context.Context, providerID, modelID string) error {
	c.mu.RLock()
	cfg := c.cfg
	c.mu.RUnlock()
	sourceProviderID, sourceModelID := cfg.ResolveModel(providerID, modelID)
	providerCfg, ok := cfg.Provider(sourceProviderID)
	if !ok || providerCfg.Disabled {
		return fmt.Errorf("provider %q is not configured", sourceProviderID)
	}
	supported, err := provider.NewCapabilityStore(cfg.StateDir()).SupportsAttachment(sourceProviderID, providerCfg, sourceModelID, attachment.KindImage)
	if err != nil {
		return err
	}
	if supported {
		return nil
	}
	return fmt.Errorf("chat history contains image context, but provider %s model %s does not support image attachments; choose an image-capable model to continue", providerID, modelID)
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
	if strings.TrimSpace(providerID) == domain.DefaultModelReference && strings.TrimSpace(modelID) == domain.DefaultModelReference {
		c.mu.RLock()
		providerID = c.cfg.Defaults.ProviderID
		modelID = c.cfg.Defaults.ModelID
		c.mu.RUnlock()
	}
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
	if !existingOK {
		client, err := provider.New(sourceProviderID, providerCfg, nil, c.providerHealth)
		if err != nil {
			return
		}
		models, err := client.ListModels(ctx)
		if err != nil {
			return
		}
		detected := false
		for _, model := range models {
			if strings.TrimSpace(model.ID) == sourceModelID {
				detected = true
				break
			}
		}
		if !detected {
			return
		}
	}
	contextWindow := existing.ContextWindow
	if provider.SupportsContextWindowDetection(providerCfg) && (!existingOK || contextWindow <= 0 || contextWindow == 32768 || !modelConfigIsCustom(existing)) {
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
	model := existing
	model.ProviderID = providerID
	model.ModelID = modelID
	model.ContextWindow = contextWindow
	model.ModelPreset = preset
	c.mu.Lock()
	changed := false
	if !existingOK || existing.ContextWindow != contextWindow || strings.TrimSpace(existing.ModelPreset) != preset {
		c.cfg.SetModelConfig(model)
		changed = true
	}
	if changed && c.cfg.Path() != "" {
		if err := c.cfg.Save(); err == nil && c.agent != nil {
			c.agent.UpdateConfig(c.cfg)
		}
	} else if changed && c.agent != nil {
		c.agent.UpdateConfig(c.cfg)
	}
	c.mu.Unlock()
}

func (c *Controller) warmConfiguredModelDetection(ctx context.Context) {
	c.mu.RLock()
	cfg := c.cfg
	c.mu.RUnlock()
	seen := map[string]struct{}{}
	add := func(providerID, modelID string) {
		providerID = strings.TrimSpace(providerID)
		modelID = strings.TrimSpace(modelID)
		if providerID == "" || modelID == "" {
			return
		}
		key := providerID + "\x00" + modelID
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		c.ensureModelConfig(ctx, providerID, modelID)
	}
	add(cfg.Defaults.ProviderID, cfg.Defaults.ModelID)
	add(cfg.Compaction.ProviderID, cfg.Compaction.ModelID)
	add(cfg.Thinking.CavemanProviderID, cfg.Thinking.CavemanModelID)
	add(cfg.UI.TTS.ProviderID, cfg.UI.TTS.ModelID)
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

func removeModelConfig(cfg *config.Config, providerID, modelID string) {
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(modelID)
	if cfg == nil || providerID == "" || modelID == "" {
		return
	}
	out := cfg.Models[:0]
	for _, model := range cfg.Models {
		if strings.TrimSpace(model.ProviderID) == providerID && strings.TrimSpace(model.ModelID) == modelID {
			continue
		}
		out = append(out, model)
	}
	cfg.Models = out
}

func repairDeletedModelReferences(ctx context.Context, cfg *config.Config, providerID, modelID string) {
	if cfg == nil {
		return
	}
	matches := func(p, m string) bool {
		return strings.TrimSpace(p) == providerID && strings.TrimSpace(m) == modelID
	}
	if matches(cfg.UI.TTS.ProviderID, cfg.UI.TTS.ModelID) {
		cfg.UI.TTS.ProviderID = ""
		cfg.UI.TTS.ModelID = ""
	}
	if matches(cfg.Compaction.ProviderID, cfg.Compaction.ModelID) {
		cfg.Compaction.ProviderID = ""
		cfg.Compaction.ModelID = ""
	}
	if matches(cfg.Thinking.CavemanProviderID, cfg.Thinking.CavemanModelID) {
		cfg.Thinking.CavemanProviderID = ""
		cfg.Thinking.CavemanModelID = ""
	}
	if !matches(cfg.Defaults.ProviderID, cfg.Defaults.ModelID) {
		return
	}
	cfg.Defaults.ProviderID = ""
	cfg.Defaults.ModelID = ""
	options, err := modelOptionsForConfig(ctx, *cfg, "", "")
	if err != nil {
		return
	}
	for _, option := range options {
		if !option.SupportsChat {
			continue
		}
		cfg.Defaults.ProviderID = option.ProviderID
		cfg.Defaults.ModelID = option.ModelID
		return
	}
}

func repairRemovedCustomModelReferences(ctx context.Context, cfg *config.Config, oldModels []config.ModelConfig) {
	if cfg == nil {
		return
	}
	for _, oldModel := range oldModels {
		if !modelConfigIsCustom(oldModel) {
			continue
		}
		providerID := strings.TrimSpace(oldModel.ProviderID)
		modelID := strings.TrimSpace(oldModel.ModelID)
		if providerID == "" || modelID == "" || modelConfigExists(cfg.Models, providerID, modelID) {
			continue
		}
		repairDeletedModelReferences(ctx, cfg, providerID, modelID)
	}
}

func modelConfigExists(models []config.ModelConfig, providerID, modelID string) bool {
	for _, model := range models {
		if strings.TrimSpace(model.ProviderID) == providerID && strings.TrimSpace(model.ModelID) == modelID {
			return true
		}
	}
	return false
}

func (c *Controller) preferencesStateLocked(ctx context.Context) (PreferencesState, error) {
	models, _ := c.modelOptionsLocked(ctx)
	liveModels := slices.Clone(models)
	models = ensureModelOption(models, c.cfg, strings.TrimSpace(c.cfg.Compaction.ProviderID), strings.TrimSpace(c.cfg.Compaction.ModelID))
	models = ensureModelOption(models, c.cfg, strings.TrimSpace(c.cfg.Thinking.CavemanProviderID), strings.TrimSpace(c.cfg.Thinking.CavemanModelID))
	prompts, err := promptPreferences(c.cfg.ManagedAssetsDir())
	if err != nil {
		return PreferencesState{}, err
	}
	state := PreferencesState{
		General: GeneralPreferences{
			DefaultProvider:      strings.TrimSpace(c.cfg.Defaults.ProviderID),
			DefaultModel:         strings.TrimSpace(c.cfg.Defaults.ModelID),
			FollowForNewSessions: c.cfg.Defaults.FollowForNewSessions,
			FollowForNewChats:    c.cfg.Defaults.FollowForNewChats,
			MaxToolLoopSteps:     c.cfg.MaxToolLoopSteps,
			MaxChildChats:        c.cfg.MaxChildChats,
		},
		UI:             browserPreferencesFromConfig(c.cfg.UI),
		Compaction:     compactionPreferencesFromConfig(c.cfg),
		Thinking:       thinkingPreferencesFromConfig(c.cfg),
		Prompts:        prompts,
		Providers:      c.providerStateLocked(),
		Models:         models,
		ModelConfigs:   modelConfigPreferencesFromConfig(c.cfg.Models, models),
		ModelOverlays:  modeloverlay.Load(c.cfg.ManagedAssetsDir()),
		MCPServers:     mcpPreferencesFromConfig(c.cfg.MCPServers),
		MCPRuntime:     mcpRuntimeState(c.agent),
		Access:         accessPreferencesFromConfig(c.cfg.Access, c.cfg.GlobalMounts),
		ToolDefaults:   toolDefaultPreferencesFromConfig(c.cfg.Tools.Enabled),
		Skills:         skillsPreferences(c.cfg, c.projectRoot),
		Browser:        nativeBrowserPreferencesFromConfig(c.cfg.Browser),
		BrowserRuntime: nativeBrowserRuntimeState(c.agent),
		Codex:          codexPreferencesFromConfig(c.cfg.Codex),
		Health:         c.settingsHealthLocked(),
	}
	for idx := range state.ModelConfigs {
		state.ModelConfigs[idx] = decorateModelConfigPreference(c.cfg, state.ModelOverlays, state.ModelConfigs[idx])
	}
	repairPreferencesDefaultModel(&state, liveModels)
	return state, nil
}

func codexPreferencesFromConfig(cfg config.Codex) CodexPreferences {
	return CodexPreferences{Configured: true, Enabled: cfg.Enabled, Executable: cfg.Executable, Home: cfg.Home}
}

func applyCodexPreferences(cfg *config.Config, prefs CodexPreferences) error {
	if !prefs.Configured {
		return nil
	}
	home := strings.TrimSpace(prefs.Home)
	if home != "" && !filepath.IsAbs(home) {
		return fmt.Errorf("codex home must be an absolute path")
	}
	cfg.Codex = config.Codex{Enabled: prefs.Enabled, Executable: strings.TrimSpace(prefs.Executable), Home: home}
	return nil
}

func nativeBrowserPreferencesFromConfig(cfg config.Browser) NativeBrowserPreferences {
	return NativeBrowserPreferences{Enabled: cfg.Enabled, Executable: cfg.Executable, Headed: cfg.Headed, OperationTimeout: int(cfg.OperationTimeout / time.Second), MaxTabsPerChat: cfg.MaxTabsPerChat, MaxTabsGlobal: cfg.MaxTabsGlobal}
}

func nativeBrowserRuntimeState(engine *agent.Engine) browserapi.Status {
	if engine != nil {
		return engine.BrowserStatus(browserapi.Chat{})
	}
	return browserapi.Status{State: "unavailable"}
}

func applyNativeBrowserPreferences(cfg *config.Config, prefs NativeBrowserPreferences) error {
	if prefs.OperationTimeout == 0 && prefs.MaxTabsPerChat == 0 && prefs.MaxTabsGlobal == 0 && strings.TrimSpace(prefs.Executable) == "" && !prefs.Enabled && !prefs.Headed {
		return nil
	}
	if prefs.OperationTimeout < 1 || prefs.OperationTimeout > 120 {
		return fmt.Errorf("browser operation timeout must be between 1 and 120 seconds")
	}
	if prefs.MaxTabsPerChat < 1 || prefs.MaxTabsGlobal < prefs.MaxTabsPerChat {
		return fmt.Errorf("browser global tab limit must be at least the per-chat limit")
	}
	cfg.Browser = config.Browser{Enabled: prefs.Enabled, Executable: strings.TrimSpace(prefs.Executable), Headed: prefs.Headed, OperationTimeout: time.Duration(prefs.OperationTimeout) * time.Second, MaxTabsPerChat: prefs.MaxTabsPerChat, MaxTabsGlobal: prefs.MaxTabsGlobal}
	return nil
}

func repairPreferencesDefaultModel(state *PreferencesState, liveModels []ModelOption) {
	liveModels = selectableChatModelOptions(liveModels)
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

// selectableChatModel is the single availability rule for model choices that
// can start a Koder chat turn. Configured aliases remain visible in model
// settings even when their source disappears, but interactive clients must not
// offer or accept them until their detected backing model returns.
func selectableChatModel(option ModelOption) bool {
	return option.SupportsChat && (option.Detected || option.BackingDetected)
}

func selectableChatModelOptions(options []ModelOption) []ModelOption {
	out := make([]ModelOption, 0, len(options))
	defaultFound := false
	for _, option := range options {
		if !selectableChatModel(option) {
			continue
		}
		defaultFound = defaultFound || option.Default
		out = append(out, option)
	}
	if !defaultFound && len(out) > 0 {
		out[0].Default = true
	}
	return out
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
		SupportsChat:     true,
		Custom:           custom,
		Editable:         custom,
		Default:          providerID == cfg.Defaults.ProviderID && modelID == cfg.Defaults.ModelID,
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
		ModelPreset:        modeloverlay.NormalizeSelection(model.ModelPreset),
		Options:            provider.ModelOptionValues(model),
		ExtraBody:          cloneExtraBodyMap(model.ExtraBody),
		Temperature:        model.Temperature,
		TopP:               model.TopP,
		MinP:               model.MinP,
		TopK:               model.TopK,
		RepeatPenalty:      model.RepeatPenalty,
		ThinkingMode:       strings.TrimSpace(model.ThinkingMode),
		ThinkingBudget:     model.ThinkingBudget,
		ReasoningEffort:    strings.TrimSpace(model.ReasoningEffort),
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
			Default:                 id == c.cfg.Defaults.ProviderID,
			PromptProgressMode:      config.NormalizePromptProgressMode(cfg.PromptProgressMode),
			PromptProgressProbed:    cfg.PromptProgressProbed,
			PromptProgressSupported: cfg.PromptProgressSupported,
			PromptProgressCheckedAt: timePointer(cfg.PromptProgressCheckedAt),
			Health:                  c.providerRuntimeHealth(id, cfg.Disabled),
		})
		if draft, err := provider.BuildDraftForExisting(id, cfg); err == nil {
			drafts[id] = providerDraftFromCatalog(draft)
		}
	}

	return ProviderState{
		DefaultProvider: strings.TrimSpace(c.cfg.Defaults.ProviderID),
		DefaultModel:    strings.TrimSpace(c.cfg.Defaults.ModelID),
		Catalog:         catalog,
		Providers:       providers,
		Drafts:          drafts,
	}
}

func (c *Controller) providerRuntimeHealth(providerID string, disabled bool) RuntimeHealth {
	if disabled {
		return RuntimeHealth{Status: provider.HealthDisabled, Detail: "Provider is disabled"}
	}
	return c.providerHealth.Provider(providerID)
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
		PromptProgressCheckedAt: timePointer(draft.PromptProgressCheckedAt),
	}
}

func providerDraftToCatalog(draft ProviderDraft) provider.ConnectDraft {
	return provider.ConnectDraft{
		OriginalProviderID: strings.TrimSpace(draft.OriginalProviderID),
		ProviderID:         strings.TrimSpace(draft.ProviderID),
		TemplateID:         strings.TrimSpace(draft.TemplateID),
		Kind:               strings.TrimSpace(draft.Kind),
		AuthMethod:         strings.TrimSpace(draft.AuthMethod),
		Name:               strings.TrimSpace(draft.Name),
		BaseURL:            strings.TrimSpace(draft.BaseURL),
		APIKey:             strings.TrimSpace(draft.APIKey),
		APIKeyEnv:          strings.TrimSpace(draft.APIKeyEnv),
		Model:              strings.TrimSpace(draft.Model),
		Stream:             draft.Stream,
		Timeout:            parseDurationOrZero(draft.Timeout),
		Disabled:           draft.Disabled,
		Headers:            cloneHeaderMap(draft.Headers),
		PromptProgressMode: config.NormalizePromptProgressMode(draft.PromptProgressMode),
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
}

func browserPreferencesFromConfig(ui config.UI) BrowserPreferences {
	return BrowserPreferences{
		Theme:        normalizeTheme(ui.Theme),
		AutoContinue: ui.AutoContinue,
		TTS:          ttsPreferencesFromConfig(ui.TTS),
	}
}

func ttsPreferencesFromConfig(tts config.TTS) TTSPreferences {
	voice := strings.TrimSpace(tts.Voice)
	if voice == "" {
		voice = "alloy"
	}
	format := strings.ToLower(strings.TrimSpace(tts.ResponseFormat))
	if format == "" {
		format = "wav"
	}
	speed := tts.Speed
	if speed <= 0 {
		speed = 1
	}
	sampleRate := tts.PCMSampleRate
	if sampleRate <= 0 {
		sampleRate = 24000
	}
	return TTSPreferences{
		Enabled:        tts.Enabled,
		ProviderID:     strings.TrimSpace(tts.ProviderID),
		ModelID:        strings.TrimSpace(tts.ModelID),
		Voice:          voice,
		ResponseFormat: format,
		Speed:          speed,
		PCMSampleRate:  sampleRate,
	}
}

func compactionPreferencesFromConfig(cfg config.Config) CompactionPreferences {
	providerID := strings.TrimSpace(cfg.Compaction.ProviderID)
	modelID := strings.TrimSpace(cfg.Compaction.ModelID)
	text := "Chat model"
	if providerID != "" || modelID != "" {
		text = providerID + " / " + modelID
	}
	return CompactionPreferences{
		AutoCompactAt:        cfg.Compaction.AutoAtPercent,
		KeepToolCalls:        config.NormalizeCompactionKeepToolCalls(cfg.Compaction.KeepToolCalls),
		ProviderID:           providerID,
		ModelID:              modelID,
		UseChatModel:         providerID == "" && modelID == "",
		CurrentSelectionText: text,
	}
}

func thinkingPreferencesFromConfig(cfg config.Config) ThinkingPreferences {
	providerID := strings.TrimSpace(cfg.Thinking.CavemanProviderID)
	modelID := strings.TrimSpace(cfg.Thinking.CavemanModelID)
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
		CavemanMinTokens:     cfg.Thinking.CavemanMinTokens,
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

func mcpRuntimeState(engine *agent.Engine) []MCPRuntimeState {
	if engine == nil {
		return nil
	}
	return mcpRuntimeStateFromServers(engine.ListMCPServers())
}

func mcpRuntimeStateFromServers(states []mcp.ServerState) []MCPRuntimeState {
	out := make([]MCPRuntimeState, 0, len(states))
	for _, state := range states {
		out = append(out, MCPRuntimeState{
			ID:                    state.ID,
			Status:                string(state.Status),
			LastError:             state.LastError,
			SessionID:             state.SessionID,
			ToolCount:             state.ToolCount,
			ResourceCount:         state.ResourceCount,
			ResourceTemplateCount: state.ResourceTemplateCount,
			PromptCount:           state.PromptCount,
		})
	}
	slices.SortFunc(out, func(a, b MCPRuntimeState) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// TestMCPServer connects to an MCP draft and reports its discovered offerings
// without changing the persisted configuration or live agent manager.
func (c *Controller) TestMCPServer(ctx context.Context, draft MCPServerDraft) (MCPRuntimeState, error) {
	id, server, err := mcpServerConfigFromDraft(draft)
	if err != nil {
		return MCPRuntimeState{}, err
	}
	if server.Disabled {
		return MCPRuntimeState{}, fmt.Errorf("enable MCP server %q before testing it", id)
	}
	manager, err := mcp.NewManager(map[string]config.MCPServer{id: server})
	if err != nil {
		return MCPRuntimeState{}, err
	}
	defer func() { _ = manager.DisconnectServer(id) }()
	if err := manager.ConnectServer(ctx, id); err != nil {
		return MCPRuntimeState{}, err
	}
	states := mcpRuntimeStateFromServers(manager.ListServers())
	if len(states) != 1 {
		return MCPRuntimeState{}, fmt.Errorf("MCP server %q returned no runtime state", id)
	}
	return states[0], nil
}

// SaveMCPServer verifies and persists one MCP draft, then reloads the live MCP
// manager. Save is intentionally immediate, matching provider editor behavior.
func (c *Controller) SaveMCPServer(ctx context.Context, draft MCPServerDraft) (MCPRuntimeState, error) {
	id, server, err := mcpServerConfigFromDraft(draft)
	if err != nil {
		return MCPRuntimeState{}, err
	}
	if !server.Disabled {
		if _, err := c.TestMCPServer(ctx, draft); err != nil {
			return MCPRuntimeState{}, err
		}
	}

	originalID := strings.TrimSpace(draft.OriginalID)
	c.mu.Lock()
	if c.cfg.MCPServers == nil {
		c.cfg.MCPServers = map[string]config.MCPServer{}
	}
	if originalID != "" && originalID != id {
		if _, exists := c.cfg.MCPServers[id]; exists {
			c.mu.Unlock()
			return MCPRuntimeState{}, fmt.Errorf("MCP server %q already exists", id)
		}
		delete(c.cfg.MCPServers, originalID)
	}
	c.cfg.MCPServers[id] = server
	if err := c.cfg.Save(); err != nil {
		c.mu.Unlock()
		return MCPRuntimeState{}, err
	}
	engine := c.agent
	next := c.cfg
	c.mu.Unlock()

	state := MCPRuntimeState{ID: id, Status: string(mcp.ServerStatusDisconnected)}
	if engine != nil {
		if err := engine.UpdateConfigAndReloadMCP(ctx, next); err != nil {
			return MCPRuntimeState{}, err
		}
		for _, runtime := range mcpRuntimeState(engine) {
			if runtime.ID == id {
				state = runtime
				break
			}
		}
	}
	c.broadcast("snapshot", c.State())
	return state, nil
}

func mcpServerConfigFromDraft(draft MCPServerDraft) (string, config.MCPServer, error) {
	id := strings.TrimSpace(draft.ID)
	startup, err := parseOptionalDuration("startup timeout", draft.StartupTimeout)
	if err != nil {
		return "", config.MCPServer{}, err
	}
	request, err := parseOptionalDuration("request timeout", draft.RequestTimeout)
	if err != nil {
		return "", config.MCPServer{}, err
	}
	server := config.MCPServer{
		Name:                 strings.TrimSpace(draft.Name),
		URL:                  strings.TrimSpace(draft.URL),
		Headers:              cloneHeaderMap(draft.Headers),
		Disabled:             draft.Disabled,
		StartupTimeout:       startup,
		RequestTimeout:       request,
		DisableStandaloneSSE: draft.DisableStandaloneSSE,
		BearerToken:          strings.TrimSpace(draft.BearerToken),
		BearerTokenEnv:       strings.TrimSpace(draft.BearerTokenEnv),
	}
	if err := mcp.ValidateServerConfig(id, server); err != nil {
		return "", config.MCPServer{}, err
	}
	return id, server, nil
}

func parseOptionalDuration(label, value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration < 0 {
		return 0, fmt.Errorf("%s must be a non-negative duration such as 10s", label)
	}
	return duration, nil
}

func accessPreferencesFromConfig(src accesssettings.Settings, globalMounts []accesssettings.Mount) AccessPreferences {
	return AccessPreferences{
		Settings:     accesssettings.Normalize(src),
		Presets:      accesssettings.Presets(),
		GlobalMounts: accesssettings.NormalizeMounts(globalMounts),
	}
}

func toolDefaultPreferencesFromConfig(src map[tools.ID]bool) []ToolDefaultPreference {
	kinds := tools.RegisteredIDs()
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

func hideToolDefault(kind tools.ID) bool {
	if tools.Info(kind).Legacy {
		return true
	}
	if isBrowserToolDefault(kind) {
		return true
	}
	switch kind {
	case tools.MilestonePlan, tools.MilestoneWrite, tools.TaskAddItems, tools.TaskUpdateItem:
		return true
	default:
		return false
	}
}

func isBrowserToolDefault(kind tools.ID) bool {
	group, _ := toolDefaultGroup(kind)
	return group == "browser"
}

func applyBrowserToolDefaults(cfg *config.Config) {
	if cfg.Tools.Enabled == nil {
		cfg.Tools.Enabled = config.ToolDefaults{}
	}
	for _, kind := range tools.RegisteredIDs() {
		if isBrowserToolDefault(kind) {
			cfg.Tools.Enabled[kind] = cfg.Browser.Enabled
		}
	}
}

func skillDiscoverOptions(cfg config.Config) skills.DiscoverOptions {
	return skills.DiscoverOptions{
		ManagedRoots:    []string{filepath.Join(cfg.ManagedAssetsDir(), "skills")},
		DisabledPaths:   cfg.Skills.Disabled,
		CatalogMaxChars: cfg.Skills.CatalogMaxChars,
	}
}

func skillsPreferences(cfg config.Config, workdir string) SkillsPreferences {
	catalog := skills.InspectWithOptions(workdir, skillDiscoverOptions(cfg))
	state := SkillsPreferences{
		Items:           make([]SkillPreference, 0, len(catalog.Items)),
		Roots:           make([]SkillRootPreference, 0, len(catalog.Roots)),
		DisabledPaths:   slices.Clone(cfg.Skills.Disabled),
		CatalogMaxChars: cfg.Skills.CatalogMaxChars,
	}
	for _, root := range catalog.Roots {
		state.Roots = append(state.Roots, SkillRootPreference{Path: root.Path, Scope: string(root.Scope), Exists: root.Exists})
	}
	for _, skill := range catalog.Items {
		state.Items = append(state.Items, skillPreference(skill))
	}
	return state
}

func skillPreference(skill skills.Skill) SkillPreference {
	return SkillPreference{
		Name: skill.Name, DisplayName: skill.Presentation.DisplayName,
		Description: skill.Description, ShortDescription: skill.Presentation.ShortDescription,
		License: skill.License, Compatibility: skill.Compatibility, Metadata: skill.Metadata,
		Path: skill.Path, CanonicalPath: skill.CanonicalPath, Directory: skill.Directory, CanonicalDirectory: skill.CanonicalDirectory,
		Scope: string(skill.Scope), Enabled: skill.Enabled, Valid: skill.Valid, Effective: skill.Effective,
		ShadowedBy: skill.ShadowedBy, Error: skill.Error, Warnings: slices.Clone(skill.Warnings),
		Logo: skill.Presentation.Logo, LogoDataURL: skillLogoDataURL(skill.Presentation.LogoPath),
		BrandColor: skill.Presentation.BrandColor,
	}
}

func skillLogoDataURL(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 256*1024 {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	mime := map[string]string{
		".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
		".webp": "image/webp", ".gif": "image/gif",
	}[strings.ToLower(filepath.Ext(path))]
	if mime == "" {
		return ""
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func applySkillsPreferences(cfg *config.Config, prefs SkillsPreferences) error {
	if prefs.CatalogMaxChars == 0 && prefs.DisabledPaths == nil {
		return nil
	}
	maxChars := prefs.CatalogMaxChars
	if maxChars == 0 {
		maxChars = cfg.Skills.CatalogMaxChars
	}
	if maxChars < 512 || maxChars > 100_000 {
		return fmt.Errorf("skill catalog prompt budget must be between 512 and 100000 characters")
	}
	disabled := make([]string, 0, len(prefs.DisabledPaths))
	seen := map[string]struct{}{}
	for _, path := range prefs.DisabledPaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			return fmt.Errorf("disabled skill path %q must be absolute", path)
		}
		path = filepath.Clean(path)
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		disabled = append(disabled, path)
	}
	cfg.Skills = config.Skills{Disabled: disabled, CatalogMaxChars: maxChars}
	return nil
}

func (c *Controller) SkillsForSelection(ctx context.Context, selection Selection) (SkillsPreferences, error) {
	c.mu.RLock()
	cfg := c.cfg
	workdir := c.projectRoot
	c.mu.RUnlock()
	if selection.SessionID != "" {
		session, err := c.SessionByID(ctx, selection.SessionID)
		if err != nil {
			return SkillsPreferences{}, err
		}
		workdir = session.ProjectRoot
	}
	return skillsPreferences(cfg, workdir), nil
}

func (c *Controller) InspectSkill(ctx context.Context, selection Selection, path string) (SkillInspection, error) {
	state, err := c.SkillsForSelection(ctx, selection)
	if err != nil {
		return SkillInspection{}, err
	}
	path = filepath.Clean(strings.TrimSpace(path))
	var selected *SkillPreference
	for idx := range state.Items {
		if state.Items[idx].CanonicalPath == path || state.Items[idx].Path == path {
			selected = &state.Items[idx]
			break
		}
	}
	if selected == nil {
		return SkillInspection{}, fmt.Errorf("skill is not in the current catalog")
	}
	body, err := os.ReadFile(selected.Path)
	if err != nil {
		return SkillInspection{}, err
	}
	if len(body) > 1024*1024 {
		return SkillInspection{}, fmt.Errorf("SKILL.md is larger than 1 MiB")
	}
	files := make([]SkillFile, 0)
	resourceDir := selected.CanonicalDirectory
	if resourceDir == "" {
		resourceDir = selected.Directory
	}
	_ = filepath.WalkDir(resourceDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || len(files) >= 200 || entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(resourceDir, path)
		if err == nil {
			files = append(files, SkillFile{Path: filepath.ToSlash(rel), Size: info.Size()})
		}
		return nil
	})
	return SkillInspection{Skill: *selected, Content: string(body), Files: files}, nil
}

func (c *Controller) settingsHealthLocked() SettingsHealth {
	issues := make([]SettingsIssue, 0)
	add := func(area, target, source, severity, message string) {
		issues = append(issues, SettingsIssue{Area: area, Target: target, Source: source, Severity: severity, Message: message})
	}

	usableProviders := 0
	for providerID, providerCfg := range c.cfg.Providers {
		if providerCfg.Disabled {
			continue
		}
		usableProviders++
		health := c.providerHealth.Provider(providerID)
		if health.Status == provider.HealthUnhealthy {
			add("models", "providers", providerID, "error", providerEntryLabel(providerID, providerCfg)+": "+health.Detail)
		} else if health.Status == provider.HealthHealthy && health.Operation == "list_models" && health.ModelCount == 0 {
			add("models", "providers", providerID, "warning", providerEntryLabel(providerID, providerCfg)+" returned no models")
		}
	}
	if usableProviders == 0 {
		message := "Add a model provider to start chatting"
		if len(c.cfg.Providers) > 0 {
			message = "Enable or add a model provider to start chatting"
		}
		add("models", "providers", "model-providers", "setup", message)
	}

	if usableProviders > 0 {
		defaultProvider := strings.TrimSpace(c.cfg.Defaults.ProviderID)
		defaultModel := strings.TrimSpace(c.cfg.Defaults.ModelID)
		if defaultProvider == "" || defaultModel == "" || !c.cfg.HasUsableProvider(defaultProvider) {
			add("models", "models", "default-model", "error", "Choose an available default model")
		} else {
			sourceProvider, sourceModel := c.cfg.ResolveModel(defaultProvider, defaultModel)
			if advertised, known := c.providerHealth.Advertises(sourceProvider, sourceModel); known && !advertised {
				add("models", "models", "default-model", "error", "The default model "+defaultProvider+" / "+defaultModel+" is no longer advertised; choose a replacement")
			}
		}
	}
	for _, model := range c.cfg.Models {
		providerID := strings.TrimSpace(model.SourceProviderID)
		if providerID == "" {
			providerID = strings.TrimSpace(model.ProviderID)
		}
		modelID := strings.TrimSpace(model.SourceModelID)
		if modelID == "" {
			modelID = strings.TrimSpace(model.ModelID)
		}
		health := c.providerHealth.Model(providerID, modelID)
		if health.Status == provider.HealthUnhealthy {
			add("models", "models", providerID+"/"+modelID, "error", providerID+" / "+modelID+": "+health.Detail)
		} else if advertised, known := c.providerHealth.Advertises(providerID, modelID); known && !advertised {
			add("models", "models", providerID+"/"+modelID, "error", providerID+" / "+modelID+": backing model is no longer advertised by the provider")
		}
	}

	if c.cfg.Codex.Enabled {
		executable := strings.TrimSpace(c.cfg.Codex.Executable)
		if executable == "" {
			executable = "codex"
		}
		if _, err := exec.LookPath(executable); err != nil {
			add("backends", "codex", "codex", "error", "Codex is enabled but its executable was not found")
		}
		if _, err := exec.LookPath("bwrap"); err != nil {
			add("backends", "codex", "codex-sandbox", "error", "Codex is enabled but bubblewrap (bwrap) was not found")
		}
	}

	if c.cfg.Browser.Enabled {
		status := nativeBrowserRuntimeState(c.agent)
		if strings.TrimSpace(status.Error) != "" || status.State == "error" {
			message := strings.TrimSpace(status.Error)
			if message == "" {
				message = "The managed browser reported an error"
			}
			add("tools", "browser", "browser", "error", message)
		} else if strings.TrimSpace(status.Executable) == "" {
			add("tools", "browser", "browser", "error", "Browser tools are enabled but no compatible Chrome or Chromium executable was found")
		}
		if _, err := exec.LookPath("bwrap"); err != nil {
			add("tools", "browser", "browser-sandbox", "error", "Browser tools are enabled but bubblewrap (bwrap) was not found")
		}
	}

	for _, runtime := range mcpRuntimeState(c.agent) {
		if runtime.Status != string(mcp.ServerStatusError) {
			continue
		}
		message := strings.TrimSpace(runtime.LastError)
		if message == "" {
			message = "MCP server connection failed"
		}
		add("tools", "mcp", runtime.ID, "error", runtime.ID+": "+message)
	}
	for _, skill := range skills.InspectWithOptions(c.projectRoot, skillDiscoverOptions(c.cfg)).Items {
		if !skill.Valid {
			add("tools", "skills", skill.CanonicalPath, "error", "Skill "+skill.Name+": "+skill.Error)
		}
	}

	slices.SortFunc(issues, func(a, b SettingsIssue) int {
		if cmp := strings.Compare(a.Area, b.Area); cmp != 0 {
			return cmp
		}
		if cmp := strings.Compare(a.Target, b.Target); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.Source, b.Source)
	})
	health := SettingsHealth{IssueCount: len(issues), Issues: issues}
	for _, issue := range issues {
		if issue.Severity == "setup" {
			health.NeedsSetup = true
			break
		}
	}
	return health
}

func toolDefaultGroup(kind tools.ID) (string, string) {
	switch kind {
	case tools.FileRead, tools.FileWrite, tools.FileEdit, tools.FileGrep, tools.FileGlob:
		return "file", "File"
	case tools.WebFetch, tools.WebSearch:
		return "web", "Web"
	case tools.ExecCommand, tools.ExecSession, tools.ExecStatus, tools.ExecList, tools.ExecWriteStdin, tools.ExecResize, tools.ExecTerminate, tools.ExecCleanup:
		return "exec", "Exec"
	case tools.Chats, tools.ChatList, tools.ChatStart, tools.ChatSend, tools.ChatCancel, tools.ChatArchive, tools.ChatRename, tools.ChatCleanup:
		return "chat", "Chat"
	case tools.Sessions, tools.SessionList, tools.SessionDelegate, tools.SessionStart:
		return "session", "Session"
	case tools.MilestoneList, tools.MilestoneAdd, tools.MilestoneUpdate, tools.MilestoneArchive, tools.MilestoneDelete, tools.MilestonePlan, tools.MilestoneWrite:
		return "milestone", "Milestone"
	case tools.TaskList, tools.TaskAddItems, tools.TaskUpdateItem, tools.TaskFetchNext, tools.TaskArchive, tools.TaskDelete, tools.TasksAdd, tools.TasksUpdate:
		return "task", "Task"
	case tools.ViewImage, tools.ViewPDF, tools.ShowImage, tools.ShowMedia, tools.Present:
		return "image", "Image"
	case tools.BrowserStatus, tools.BrowserTabs, tools.BrowserNavigation, tools.BrowserPage, tools.BrowserInteract, tools.BrowserCapture, tools.BrowserConsole, tools.BrowserEvaluate, tools.BrowserNetwork, tools.BrowserDownloads:
		return "browser", "Browser"
	case tools.PhoneDevice, tools.PhoneLocation, tools.PhoneContacts, tools.PhoneCalendar, tools.PhoneMessages, tools.PhoneCalls, tools.PhoneNotifications, tools.PhoneClock, tools.PhoneClipboard, tools.PhoneApps, tools.PhoneMedia, tools.PhoneShare, tools.PhoneOpen, tools.PhonePhotos:
		return "phone", "Phone"
	default:
		key := kind.String()
		return key, kind.DisplayName()
	}
}

func applyGeneralPreferences(cfg *config.Config, prefs GeneralPreferences) error {
	cfg.Defaults.ProviderID = strings.TrimSpace(prefs.DefaultProvider)
	cfg.Defaults.ModelID = strings.TrimSpace(prefs.DefaultModel)
	cfg.Defaults.FollowForNewSessions = prefs.FollowForNewSessions
	cfg.Defaults.FollowForNewChats = prefs.FollowForNewChats
	if cfg.Defaults.ProviderID != "" && !cfg.HasUsableProvider(cfg.Defaults.ProviderID) {
		return fmt.Errorf("default provider %q is not configured or is disabled", cfg.Defaults.ProviderID)
	}
	if prefs.MaxToolLoopSteps <= 0 {
		return fmt.Errorf("max tool loop steps must be greater than zero")
	}
	cfg.MaxToolLoopSteps = prefs.MaxToolLoopSteps
	if prefs.MaxChildChats <= 0 {
		return fmt.Errorf("max child chats must be greater than zero")
	}
	cfg.MaxChildChats = prefs.MaxChildChats
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
		if err := validateModelOverlay(*cfg, model); err != nil {
			return err
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
	model := config.ModelConfig{
		ProviderID:       providerID,
		ModelID:          modelID,
		SourceProviderID: sourceProviderID,
		SourceModelID:    sourceModelID,
		ContextWindow:    pref.ContextWindow,
		ModelPreset:      strings.TrimSpace(pref.ModelPreset),
		Options:          cloneExtraBodyMap(pref.Options),
		ExtraBody:        cloneExtraBodyMap(pref.ExtraBody),
		Temperature:      pref.Temperature,
		TopP:             pref.TopP,
		MinP:             pref.MinP,
		TopK:             pref.TopK,
		RepeatPenalty:    pref.RepeatPenalty,
		ThinkingMode:     strings.TrimSpace(pref.ThinkingMode),
		ThinkingBudget:   pref.ThinkingBudget,
		ReasoningEffort:  strings.TrimSpace(pref.ReasoningEffort),
	}
	clearShadowedLegacyModelOptions(&model)
	return model, nil
}

// clearShadowedLegacyModelOptions keeps overlay options canonical when a model
// also carries fields from the pre-overlay settings format. ModelOptionValues
// intentionally gives Options precedence; clearing only duplicated fields makes
// the persisted config and the request agree without discarding legacy-only
// settings that the selected overlay does not expose.
func clearShadowedLegacyModelOptions(model *config.ModelConfig) {
	if model == nil || len(model.Options) == 0 {
		return
	}
	if _, ok := model.Options["temperature"]; ok {
		model.Temperature = nil
	}
	if _, ok := model.Options["top_p"]; ok {
		model.TopP = nil
	}
	if _, ok := model.Options["min_p"]; ok {
		model.MinP = nil
	}
	if _, ok := model.Options["top_k"]; ok {
		model.TopK = 0
	}
	if _, ok := model.Options["repeat_penalty"]; ok {
		model.RepeatPenalty = nil
	}
	if _, ok := model.Options["thinking_mode"]; ok {
		model.ThinkingMode = ""
	}
	if _, ok := model.Options["thinking_budget"]; ok {
		model.ThinkingBudget = 0
	}
	if _, ok := model.Options["reasoning_effort"]; ok {
		model.ReasoningEffort = ""
	}
}

func decorateModelConfigPreference(cfg config.Config, catalog modeloverlay.Catalog, pref ModelConfigPreference) ModelConfigPreference {
	sourceProviderID := strings.TrimSpace(pref.SourceProviderID)
	if sourceProviderID == "" {
		sourceProviderID = strings.TrimSpace(pref.ProviderID)
	}
	sourceModelID := strings.TrimSpace(pref.SourceModelID)
	if sourceModelID == "" {
		sourceModelID = strings.TrimSpace(pref.ModelID)
	}
	providerCfg, _ := cfg.Provider(sourceProviderID)
	pref.ResolvedOverlay = catalog.Resolve(sourceModelID, pref.ModelPreset, provider.OverlayTransport(providerCfg))
	pref.Options = pref.ResolvedOverlay.EffectiveValues(pref.Options)
	return pref
}

func validateModelOverlay(cfg config.Config, model config.ModelConfig) error {
	sourceProviderID := strings.TrimSpace(model.SourceProviderID)
	if sourceProviderID == "" {
		sourceProviderID = strings.TrimSpace(model.ProviderID)
	}
	sourceModelID := strings.TrimSpace(model.SourceModelID)
	if sourceModelID == "" {
		sourceModelID = strings.TrimSpace(model.ModelID)
	}
	providerCfg, _ := cfg.Provider(sourceProviderID)
	resolved := modeloverlay.Load(cfg.ManagedAssetsDir()).Resolve(sourceModelID, model.ModelPreset, provider.OverlayTransport(providerCfg))
	if err := resolved.ValidateValues(model.Options); err != nil {
		return fmt.Errorf("model options for %s/%s: %w", model.ProviderID, model.ModelID, err)
	}
	return nil
}

func cloneExtraBodyMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]any, len(src))
	for key, value := range src {
		if strings.TrimSpace(key) == "" {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func applyBrowserPreferences(cfg *config.Config, prefs BrowserPreferences) error {
	tts, err := configTTSFromPreference(prefs.TTS)
	if err != nil {
		return err
	}
	cfg.UI = config.UI{
		Theme:        normalizeTheme(prefs.Theme),
		AutoContinue: prefs.AutoContinue,
		TTS:          tts,
	}
	return nil
}

func configTTSFromPreference(pref TTSPreferences) (config.TTS, error) {
	providerID := strings.TrimSpace(pref.ProviderID)
	modelID := strings.TrimSpace(pref.ModelID)
	if (providerID == "") != (modelID == "") {
		return config.TTS{}, fmt.Errorf("tts provider and model must be set together")
	}
	format := strings.ToLower(strings.TrimSpace(pref.ResponseFormat))
	if format == "" {
		format = "wav"
	}
	switch format {
	case "mp3", "opus", "aac", "flac", "wav", "pcm":
	default:
		return config.TTS{}, fmt.Errorf("unsupported tts response format %q", format)
	}
	speed := pref.Speed
	if speed <= 0 {
		speed = 1
	}
	if speed < 0.25 || speed > 4 {
		return config.TTS{}, fmt.Errorf("tts speed must be between 0.25 and 4")
	}
	sampleRate := pref.PCMSampleRate
	if sampleRate <= 0 {
		sampleRate = 24000
	}
	if sampleRate < 8000 || sampleRate > 192000 {
		return config.TTS{}, fmt.Errorf("tts pcm sample rate must be between 8000 and 192000")
	}
	voice := strings.TrimSpace(pref.Voice)
	if voice == "" {
		voice = "alloy"
	}
	return config.TTS{
		Enabled:        pref.Enabled,
		ProviderID:     providerID,
		ModelID:        modelID,
		Voice:          voice,
		ResponseFormat: format,
		Speed:          speed,
		PCMSampleRate:  sampleRate,
	}, nil
}

func applyCompactionPreferences(cfg *config.Config, prefs CompactionPreferences) error {
	if prefs.AutoCompactAt <= 0 {
		return fmt.Errorf("auto compact threshold must be greater than zero")
	}
	cfg.Compaction.AutoAtPercent = prefs.AutoCompactAt
	cfg.Compaction.KeepToolCalls = config.NormalizeCompactionKeepToolCalls(prefs.KeepToolCalls)
	if prefs.UseChatModel {
		cfg.Compaction.ProviderID = ""
		cfg.Compaction.ModelID = ""
		return nil
	}
	providerID := strings.TrimSpace(prefs.ProviderID)
	modelID := strings.TrimSpace(prefs.ModelID)
	if providerID == "" && modelID == "" {
		cfg.Compaction.ProviderID = ""
		cfg.Compaction.ModelID = ""
		return nil
	}
	if providerID == "" || modelID == "" {
		return fmt.Errorf("compaction provider and model must both be set, or both empty for chat model")
	}
	if !cfg.HasUsableProvider(providerID) {
		return fmt.Errorf("compaction provider %q is not configured or is disabled", providerID)
	}
	cfg.Compaction.ProviderID = providerID
	cfg.Compaction.ModelID = modelID
	return nil
}

func applyThinkingPreferences(cfg *config.Config, prefs ThinkingPreferences) error {
	cfg.Thinking.CavemanEnabled = prefs.CavemanEnabled
	cfg.Thinking.CavemanPrompt = strings.TrimSpace(prefs.CavemanPrompt)
	if cfg.Thinking.CavemanPrompt == "" {
		cfg.Thinking.CavemanPrompt = config.DefaultCavemanThinkingPrompt
	}
	cfg.Thinking.CavemanMinTokens = prefs.CavemanMinTokens
	if cfg.Thinking.CavemanMinTokens <= 0 {
		cfg.Thinking.CavemanMinTokens = config.DefaultCavemanMinTokens
	}
	if prefs.UseChatModel {
		cfg.Thinking.CavemanProviderID = ""
		cfg.Thinking.CavemanModelID = ""
		return nil
	}
	providerID := strings.TrimSpace(prefs.ProviderID)
	modelID := strings.TrimSpace(prefs.ModelID)
	if providerID == "" && modelID == "" {
		cfg.Thinking.CavemanProviderID = ""
		cfg.Thinking.CavemanModelID = ""
		return nil
	}
	if providerID == "" || modelID == "" {
		return fmt.Errorf("thinking provider and model must both be set, or both empty for chat model")
	}
	if !cfg.HasUsableProvider(providerID) {
		return fmt.Errorf("thinking provider %q is not configured or is disabled", providerID)
	}
	cfg.Thinking.CavemanProviderID = providerID
	cfg.Thinking.CavemanModelID = modelID
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
	globalMounts := accesssettings.NormalizeMounts(prefs.GlobalMounts)
	if err := accesssettings.ValidateMounts(globalMounts); err != nil {
		return fmt.Errorf("global mount: %w", err)
	}
	cfg.Access = settings
	cfg.GlobalMounts = globalMounts
	return nil
}

func applyToolDefaultPreferences(cfg *config.Config, prefs []ToolDefaultPreference) {
	next := map[tools.ID]bool{}
	for _, item := range prefs {
		next[item.Tool] = item.Enabled
	}
	for _, kind := range tools.RegisteredIDs() {
		if _, ok := next[kind]; !ok {
			next[kind] = true
		}
	}
	cfg.Tools.Enabled = next
}

func promptPreferences(root string) ([]PromptPreference, error) {
	out := make([]PromptPreference, 0, 2)
	for _, target := range []string{"system-prompt.md", "compaction-prompt.md"} {
		item, err := promptPreference(root, target)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func promptPreference(root string, target string) (PromptPreference, error) {
	path, err := managedPromptPath(root, target)
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

func writePromptPreferences(root string, prompts []PromptPreference) error {
	for _, prompt := range prompts {
		target := strings.TrimSpace(prompt.Target)
		if target != "system-prompt.md" && target != "compaction-prompt.md" {
			continue
		}
		path, err := managedPromptPath(root, target)
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

func managedPromptPath(root string, target string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("managed asset root is empty")
	}
	return filepath.Join(root, target), nil
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

func timePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copy := value
	return &copy
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

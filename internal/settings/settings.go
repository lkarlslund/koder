package settings

import (
	"fmt"
	"strings"
	"sync"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
)

type Store struct {
	mu  sync.RWMutex
	cfg config.Config
}

type NewSessionDefaults struct {
	ProviderID string
	ModelID    string
	Access     accesssettings.Settings
}

type ToolSettings struct {
	Enabled map[domain.ToolKind]bool
}

type ModelSettings struct {
	ProviderID       string
	ModelID          string
	SourceProviderID string
	SourceModelID    string
	Provider         config.Provider
	Model            config.ModelConfig
	ContextWindow    int
	Streaming        bool
}

type CompactionSettings struct {
	ThresholdPercent int
	KeepToolCalls    int
	ProviderID       string
	ModelID          string
	Provider         config.Provider
	Model            config.ModelConfig
	ContextWindow    int
	Prompt           string
}

type ThinkingSettings struct {
	CavemanEnabled   bool
	ProviderID       string
	ModelID          string
	Provider         config.Provider
	Model            config.ModelConfig
	Prompt           string
	Parallelism      int
	MinTokens        int
	PreserveThinking bool
}

func New(cfg config.Config) *Store {
	return &Store{cfg: cfg}
}

func (s *Store) Update(cfg config.Config) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
}

func (s *Store) Snapshot() config.Config {
	if s == nil {
		return config.Config{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

func (s *Store) NewSessionDefaults() NewSessionDefaults {
	cfg := s.Snapshot()
	return NewSessionDefaults{
		ProviderID: strings.TrimSpace(cfg.DefaultProvider),
		ModelID:    strings.TrimSpace(cfg.DefaultModel),
		Access:     accesssettings.Normalize(cfg.Access),
	}
}

func (s *Store) Access(session domain.Session) accesssettings.Settings {
	cfg := s.Snapshot()
	settings := session.AccessSettings
	if accesssettings.IsZero(settings) {
		settings = cfg.Access
	}
	return accesssettings.Normalize(settings)
}

func (s *Store) Tools(_ domain.Session) ToolSettings {
	cfg := s.Snapshot()
	enabled := make(map[domain.ToolKind]bool, len(domain.BuiltinToolKinds()))
	for _, kind := range domain.BuiltinToolKinds() {
		value := true
		if cfg.ToolDefaults != nil {
			if configured, ok := cfg.ToolDefaults[kind]; ok {
				value = configured
			}
		}
		enabled[kind] = value
	}
	return ToolSettings{Enabled: enabled}
}

func (s *Store) Model(chat domain.Chat) (ModelSettings, error) {
	cfg := s.Snapshot()
	providerID := strings.TrimSpace(chat.ProviderID)
	modelID := strings.TrimSpace(chat.ModelID)
	if providerID == "" {
		return ModelSettings{}, fmt.Errorf("chat %s has no provider", chat.ID)
	}
	if modelID == "" {
		return ModelSettings{}, fmt.Errorf("chat %s has no model", chat.ID)
	}
	return modelSettings(cfg, providerID, modelID)
}

func (s *Store) Compaction(chat domain.Chat, prompt string) (CompactionSettings, error) {
	cfg := s.Snapshot()
	providerID := strings.TrimSpace(cfg.CompactionProvider)
	modelID := strings.TrimSpace(cfg.CompactionModel)
	if providerID == "" {
		providerID = strings.TrimSpace(chat.ProviderID)
	}
	if modelID == "" {
		modelID = strings.TrimSpace(chat.ModelID)
	}
	model, err := modelSettings(cfg, providerID, modelID)
	if err != nil {
		return CompactionSettings{}, err
	}
	return CompactionSettings{
		ThresholdPercent: max(1, cfg.AutoCompactAt),
		KeepToolCalls:    config.NormalizeCompactionKeepToolCalls(cfg.CompactionKeepToolCalls),
		ProviderID:       model.ProviderID,
		ModelID:          model.ModelID,
		Provider:         model.Provider,
		Model:            model.Model,
		ContextWindow:    model.ContextWindow,
		Prompt:           strings.TrimSpace(prompt),
	}, nil
}

func (s *Store) Thinking(chat domain.Chat, prompt string, preserveThinking bool) (ThinkingSettings, error) {
	cfg := s.Snapshot()
	providerID := strings.TrimSpace(cfg.Thinking.CavemanProvider)
	modelID := strings.TrimSpace(cfg.Thinking.CavemanModel)
	if providerID == "" {
		providerID = strings.TrimSpace(chat.ProviderID)
	}
	if modelID == "" {
		modelID = strings.TrimSpace(chat.ModelID)
	}
	model, err := modelSettings(cfg, providerID, modelID)
	if err != nil {
		return ThinkingSettings{}, err
	}
	return ThinkingSettings{
		CavemanEnabled:   cfg.Thinking.CavemanEnabled,
		ProviderID:       model.ProviderID,
		ModelID:          model.ModelID,
		Provider:         model.Provider,
		Model:            model.Model,
		Prompt:           strings.TrimSpace(prompt),
		Parallelism:      cfg.Thinking.CavemanParallelism,
		MinTokens:        cfg.Thinking.CavemanMinTokens,
		PreserveThinking: preserveThinking,
	}, nil
}

func modelSettings(cfg config.Config, providerID, modelID string) (ModelSettings, error) {
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(modelID)
	sourceProviderID, sourceModelID := cfg.ResolveModel(providerID, modelID)
	providerCfg, ok := cfg.Provider(sourceProviderID)
	if !ok {
		return ModelSettings{}, fmt.Errorf("provider %q not found", sourceProviderID)
	}
	modelCfg := cfg.ModelRequestOptions(providerID, modelID)
	if modelCfg.ProviderID == "" {
		modelCfg.ProviderID = sourceProviderID
	}
	if modelCfg.ModelID == "" {
		modelCfg.ModelID = sourceModelID
	}
	return ModelSettings{
		ProviderID:       providerID,
		ModelID:          modelID,
		SourceProviderID: sourceProviderID,
		SourceModelID:    sourceModelID,
		Provider:         providerCfg,
		Model:            modelCfg,
		ContextWindow:    cfg.ContextWindow(providerID, modelID),
		Streaming:        providerCfg.Stream,
	}, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

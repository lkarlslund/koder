package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
)

const capabilityCacheTTL = 7 * 24 * time.Hour

type CapabilityStore struct {
	path string
	mu   sync.Mutex
}

type capabilityEntry struct {
	ProviderID        string    `json:"provider_id"`
	BaseURL           string    `json:"base_url"`
	ModelID           string    `json:"model_id"`
	SupportsImages    bool      `json:"supports_images"`
	SupportsPDFs      bool      `json:"supports_pdfs"`
	CapabilitySource  string    `json:"capability_source"`
	CapabilitiesKnown bool      `json:"capabilities_known"`
	DetectedAt        time.Time `json:"detected_at"`
}

type capabilityFile struct {
	Entries map[string]capabilityEntry `json:"entries"`
}

func NewCapabilityStore(stateDir string) *CapabilityStore {
	return &CapabilityStore{path: filepath.Join(stateDir, "model-capabilities.json")}
}

func (s *CapabilityStore) EnrichModels(providerID string, cfg config.Provider, models []domain.Model) ([]domain.Model, error) {
	out := make([]domain.Model, 0, len(models))
	for _, model := range models {
		enriched, err := s.EnrichModel(providerID, cfg, model)
		if err != nil {
			return nil, err
		}
		out = append(out, enriched)
	}
	return out, nil
}

func (s *CapabilityStore) EnrichModel(providerID string, cfg config.Provider, model domain.Model) (domain.Model, error) {
	if s == nil {
		return inferCapabilities(providerID, cfg, model), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	cache, err := s.load()
	if err != nil {
		return domain.Model{}, err
	}
	key := capabilityKey(providerID, cfg.BaseURL, model.ID)
	if entry, ok := cache.Entries[key]; ok && time.Since(entry.DetectedAt) < capabilityCacheTTL {
		return applyEntry(model, entry), nil
	}
	inferred := inferCapabilities(providerID, cfg, model)
	cache.Entries[key] = capabilityEntry{
		ProviderID:        providerID,
		BaseURL:           strings.TrimSpace(cfg.BaseURL),
		ModelID:           model.ID,
		SupportsImages:    inferred.SupportsImages,
		SupportsPDFs:      inferred.SupportsPDFs,
		CapabilitySource:  inferred.CapabilitySource,
		CapabilitiesKnown: inferred.CapabilitiesKnown,
		DetectedAt:        time.Now().UTC(),
	}
	if err := s.save(cache); err != nil {
		return domain.Model{}, err
	}
	return inferred, nil
}

func (s *CapabilityStore) SupportsAttachment(providerID string, cfg config.Provider, modelID string, kind attachment.Kind) (bool, error) {
	model, err := s.EnrichModel(providerID, cfg, domain.Model{ID: modelID})
	if err != nil {
		return false, err
	}
	switch kind {
	case attachment.KindText:
		return true, nil
	case attachment.KindImage:
		return model.SupportsImages, nil
	case attachment.KindPDF:
		return model.SupportsPDFs, nil
	default:
		return false, nil
	}
}

func inferCapabilities(providerID string, cfg config.Provider, model domain.Model) domain.Model {
	desc, hasDesc := Lookup(providerID)
	supportsImages := false
	if (hasDesc && desc.SupportsImages) || strings.TrimSpace(providerID) == ProviderKindCompatible {
		supportsImages = modelLikelySupportsImages(model.ID)
	}
	supportsPDFs := false
	source := "heuristic"
	known := true
	if strings.TrimSpace(model.ID) == "" {
		known = false
	}
	model.SupportsImages = supportsImages
	model.SupportsPDFs = supportsPDFs
	model.CapabilitySource = source
	model.CapabilitiesKnown = known
	return model
}

func capabilityKey(providerID, baseURL, modelID string) string {
	return strings.TrimSpace(providerID) + "|" + strings.TrimSpace(baseURL) + "|" + strings.TrimSpace(modelID)
}

func applyEntry(model domain.Model, entry capabilityEntry) domain.Model {
	model.SupportsImages = entry.SupportsImages
	model.SupportsPDFs = entry.SupportsPDFs
	model.CapabilitySource = entry.CapabilitySource
	model.CapabilitiesKnown = entry.CapabilitiesKnown
	return model
}

func (s *CapabilityStore) load() (capabilityFile, error) {
	cache := capabilityFile{Entries: map[string]capabilityEntry{}}
	if strings.TrimSpace(s.path) == "" {
		return cache, fmt.Errorf("capability cache path is empty")
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return cache, nil
		}
		return cache, fmt.Errorf("read capability cache: %w", err)
	}
	if err := json.Unmarshal(data, &cache); err != nil {
		return cache, fmt.Errorf("decode capability cache: %w", err)
	}
	if cache.Entries == nil {
		cache.Entries = map[string]capabilityEntry{}
	}
	return cache, nil
}

func (s *CapabilityStore) save(cache capabilityFile) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create capability cache dir: %w", err)
	}
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf("encode capability cache: %w", err)
	}
	if err := os.WriteFile(s.path, data, 0o644); err != nil {
		return fmt.Errorf("write capability cache: %w", err)
	}
	return nil
}

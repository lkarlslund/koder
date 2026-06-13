package provider

import (
	"context"
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
	SupportsChat      bool      `json:"supports_chat"`
	SupportsTTS       bool      `json:"supports_tts"`
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
	current := inferCapabilities(providerID, cfg, model)
	if entry, ok := cache.Entries[key]; ok && time.Since(entry.DetectedAt) < capabilityCacheTTL {
		if strings.TrimSpace(entry.CapabilitySource) == "probe" {
			return applyEntry(current, entry), nil
		}
	}
	if shouldProbeTTS(providerID, cfg, model.ID) {
		client, err := New(providerID, cfg, nil)
		if err == nil && client != nil {
			supportsTTS, probeErr := client.ProbeTTSSupport(context.Background(), model.ID)
			if probeErr == nil && supportsTTS {
				supportsChat := true
				if chatSupported, chatErr := client.ProbeChatSupport(context.Background(), model.ID); chatErr == nil {
					supportsChat = chatSupported
				}
				entry := capabilityEntry{
					ProviderID:        providerID,
					BaseURL:           strings.TrimSpace(cfg.BaseURL),
					ModelID:           model.ID,
					SupportsChat:      supportsChat,
					SupportsTTS:       true,
					SupportsImages:    current.SupportsImages,
					SupportsPDFs:      current.SupportsPDFs,
					CapabilitySource:  "probe",
					CapabilitiesKnown: true,
					DetectedAt:        time.Now().UTC(),
				}
				cache.Entries[key] = entry
				if err := s.save(cache); err != nil {
					return domain.Model{}, err
				}
				return applyEntry(current, entry), nil
			}
		}
	}
	return current, nil
}

func (s *CapabilityStore) SupportsAttachment(providerID string, cfg config.Provider, modelID string, kind attachment.Kind) (bool, error) {
	switch kind {
	case attachment.KindText:
		return true, nil
	case attachment.KindImage:
		return s.supportsImageAttachment(providerID, cfg, modelID)
	case attachment.KindPDF:
		model, err := s.EnrichModel(providerID, cfg, domain.Model{ID: modelID})
		if err != nil {
			return false, err
		}
		return model.SupportsPDFs, nil
	default:
		return false, nil
	}
}

func (s *CapabilityStore) supportsImageAttachment(providerID string, cfg config.Provider, modelID string) (bool, error) {
	if s == nil {
		return true, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	cache, err := s.load()
	if err != nil {
		return false, err
	}
	key := capabilityKey(providerID, cfg.BaseURL, modelID)
	current := inferCapabilities(providerID, cfg, domain.Model{ID: modelID})
	if entry, ok := cache.Entries[key]; ok && time.Since(entry.DetectedAt) < capabilityCacheTTL {
		if strings.TrimSpace(entry.CapabilitySource) == "probe" {
			current = applyEntry(current, entry)
			return current.SupportsImages, nil
		}
	}

	client, err := New(providerID, cfg, nil)
	if err == nil && client != nil {
		supported, probeErr := client.ProbeImageSupport(context.Background(), modelID)
		if probeErr == nil {
			cache.Entries[key] = capabilityEntry{
				ProviderID:        providerID,
				BaseURL:           strings.TrimSpace(cfg.BaseURL),
				ModelID:           modelID,
				SupportsChat:      current.SupportsChat,
				SupportsTTS:       current.SupportsTTS,
				SupportsImages:    supported,
				SupportsPDFs:      current.SupportsPDFs,
				CapabilitySource:  "probe",
				CapabilitiesKnown: true,
				DetectedAt:        time.Now().UTC(),
			}
			if err := s.save(cache); err != nil {
				return false, err
			}
			return supported, nil
		}
	}

	return true, nil
}

func (s *CapabilityStore) Invalidate(providerID string, cfg config.Provider, modelID string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cache, err := s.load()
	if err != nil {
		return err
	}
	delete(cache.Entries, capabilityKey(providerID, cfg.BaseURL, modelID))
	return s.save(cache)
}

func inferCapabilities(providerID string, cfg config.Provider, model domain.Model) domain.Model {
	supportsPDFs := false
	model.SupportsChat = true
	model.SupportsTTS = false
	model.SupportsImages = false
	model.SupportsPDFs = supportsPDFs
	model.CapabilitySource = ""
	model.CapabilitiesKnown = false
	return model
}

func shouldProbeTTS(providerID string, cfg config.Provider, modelID string) bool {
	haystack := strings.ToLower(strings.Join([]string{
		providerID,
		cfg.Name,
		cfg.BaseURL,
		modelID,
	}, " "))
	for _, needle := range []string{"tts", "speech", "voice", "audio", "omnivoice"} {
		if strings.Contains(haystack, needle) {
			return true
		}
	}
	return false
}

func capabilityKey(providerID, baseURL, modelID string) string {
	return strings.TrimSpace(providerID) + "|" + strings.TrimSpace(baseURL) + "|" + strings.TrimSpace(modelID)
}

func applyEntry(model domain.Model, entry capabilityEntry) domain.Model {
	model.SupportsChat = entry.SupportsChat
	if !entry.SupportsChat && !entry.SupportsTTS {
		model.SupportsChat = true
	}
	model.SupportsTTS = entry.SupportsTTS
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

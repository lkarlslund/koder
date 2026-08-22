package service

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/lkarlslund/koder/internal/knowledge/kpackage"
)

var ErrPublisherRegistry = errors.New("knowledge publisher registry is invalid")

type PublisherTrustState string

const (
	PublisherTrustUnknown           PublisherTrustState = "unknown"
	PublisherTrustVerified          PublisherTrustState = "verified"
	PublisherTrustUnsigned          PublisherTrustState = "trusted_publisher_unsigned"
	PublisherTrustUnrecognizedKey   PublisherTrustState = "unrecognized_key"
	PublisherTrustPublisherMismatch PublisherTrustState = "publisher_mismatch"
)

type PublisherTrust struct {
	State       PublisherTrustState `json:"state"`
	PublisherID string              `json:"publisher_id"`
	KeyID       string              `json:"key_id,omitempty"`
	Trusted     bool                `json:"trusted"`
	Reason      string              `json:"reason"`
}

type TrustedPublisher struct {
	ID   string
	Name string
	Keys map[string]ed25519.PublicKey
}

type PublisherRegistry struct {
	publishers map[string]TrustedPublisher
	keyOwners  map[string]string
}

func NewPublisherRegistry(values []TrustedPublisher) (*PublisherRegistry, error) {
	registry := &PublisherRegistry{publishers: make(map[string]TrustedPublisher, len(values)), keyOwners: make(map[string]string)}
	for _, value := range values {
		value.ID = strings.TrimSpace(value.ID)
		value.Name = strings.TrimSpace(value.Name)
		if value.ID == "" {
			return nil, fmt.Errorf("%w: publisher ID is required", ErrPublisherRegistry)
		}
		if _, exists := registry.publishers[value.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate publisher %q", ErrPublisherRegistry, value.ID)
		}
		if len(value.Keys) == 0 {
			return nil, fmt.Errorf("%w: publisher %q has no signing keys", ErrPublisherRegistry, value.ID)
		}
		keys := make(map[string]ed25519.PublicKey, len(value.Keys))
		for rawID, rawKey := range value.Keys {
			keyID := strings.TrimSpace(rawID)
			if keyID == "" || len(rawKey) != ed25519.PublicKeySize {
				return nil, fmt.Errorf("%w: publisher %q has an invalid key", ErrPublisherRegistry, value.ID)
			}
			if owner, exists := registry.keyOwners[keyID]; exists {
				return nil, fmt.Errorf("%w: key %q is already owned by publisher %q", ErrPublisherRegistry, keyID, owner)
			}
			keys[keyID] = slices.Clone(rawKey)
			registry.keyOwners[keyID] = value.ID
		}
		value.Keys = keys
		registry.publishers[value.ID] = value
	}
	return registry, nil
}

func (r *PublisherRegistry) VerificationKeys() map[string]ed25519.PublicKey {
	if r == nil {
		return nil
	}
	result := make(map[string]ed25519.PublicKey, len(r.keyOwners))
	for keyID, owner := range r.keyOwners {
		result[keyID] = slices.Clone(r.publishers[owner].Keys[keyID])
	}
	return result
}

func (r *PublisherRegistry) Assess(manifest kpackage.Manifest, signatureState kpackage.SignatureState) PublisherTrust {
	publisherID := strings.TrimSpace(manifest.Publisher.ID)
	keyID := ""
	if manifest.Signature != nil {
		keyID = strings.TrimSpace(manifest.Signature.KeyID)
	}
	result := PublisherTrust{State: PublisherTrustUnknown, PublisherID: publisherID, KeyID: keyID, Reason: "publisher_not_registered"}
	if r == nil {
		return result
	}
	publisher, known := r.publishers[publisherID]
	owner := r.keyOwners[keyID]
	if !known {
		if owner != "" {
			result.State, result.Reason = PublisherTrustPublisherMismatch, "signing_key_registered_to_different_publisher"
		}
		return result
	}
	if manifest.Signature == nil || signatureState == kpackage.SignatureStateUnsigned {
		result.State, result.Reason = PublisherTrustUnsigned, "registered_publisher_package_is_unsigned"
		return result
	}
	if _, recognized := publisher.Keys[keyID]; !recognized {
		if owner != "" {
			result.State, result.Reason = PublisherTrustPublisherMismatch, "signing_key_registered_to_different_publisher"
		} else {
			result.State, result.Reason = PublisherTrustUnrecognizedKey, "publisher_signing_key_not_registered"
		}
		return result
	}
	if signatureState == kpackage.SignatureStateVerified {
		result.State, result.Trusted, result.Reason = PublisherTrustVerified, true, "registered_publisher_signature_verified"
		return result
	}
	result.State, result.Reason = PublisherTrustUnrecognizedKey, "publisher_signature_not_verified"
	return result
}

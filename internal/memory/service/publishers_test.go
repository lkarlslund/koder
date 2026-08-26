package service

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
	"github.com/lkarlslund/koder/internal/memory/kpackage"
	memoryBackend "github.com/lkarlslund/koder/internal/memory/store/memory"
)

func TestPublisherRegistryValidatesIdentityAndKeyOwnership(t *testing.T) {
	t.Parallel()
	public, _ := publisherTestKey(0x21)
	registry, err := NewPublisherRegistry([]TrustedPublisher{{ID: "publisher:one", Name: "One", Keys: map[string]ed25519.PublicKey{"key:one": public}}})
	if err != nil {
		t.Fatal(err)
	}
	keys := registry.VerificationKeys()
	keys["key:one"][0] ^= 0xff
	if bytes.Equal(keys["key:one"], registry.VerificationKeys()["key:one"]) {
		t.Fatal("VerificationKeys returned mutable registry storage")
	}
	for name, values := range map[string][]TrustedPublisher{
		"missing ID":   {{Keys: map[string]ed25519.PublicKey{"key": public}}},
		"missing keys": {{ID: "publisher:one"}},
		"duplicate publisher": {
			{ID: "publisher:one", Keys: map[string]ed25519.PublicKey{"key:one": public}},
			{ID: "publisher:one", Keys: map[string]ed25519.PublicKey{"key:two": public}},
		},
		"shared key ID": {
			{ID: "publisher:one", Keys: map[string]ed25519.PublicKey{"shared": public}},
			{ID: "publisher:two", Keys: map[string]ed25519.PublicKey{"shared": public}},
		},
	} {
		if _, err := NewPublisherRegistry(values); !errors.Is(err, ErrPublisherRegistry) {
			t.Errorf("NewPublisherRegistry(%s) error = %v", name, err)
		}
	}
}

func TestPublisherRegistryLabelsVerifiedUnsignedUnknownAndMismatchedPackages(t *testing.T) {
	t.Parallel()
	public, private := publisherTestKey(0x31)
	registry, err := NewPublisherRegistry([]TrustedPublisher{{
		ID: "publisher:trusted", Name: "Trusted", Keys: map[string]ed25519.PublicKey{"trusted:key": public},
	}})
	if err != nil {
		t.Fatal(err)
	}
	service := publisherTestService(t, registry, nil)
	for _, test := range []struct {
		name      string
		publisher string
		signing   *kpackage.SigningConfig
		want      PublisherTrustState
		trusted   bool
	}{
		{name: "verified", publisher: "publisher:trusted", signing: &kpackage.SigningConfig{KeyID: "trusted:key", PrivateKey: private}, want: PublisherTrustVerified, trusted: true},
		{name: "unsigned", publisher: "publisher:trusted", want: PublisherTrustUnsigned},
		{name: "unknown publisher", publisher: "publisher:unknown", want: PublisherTrustUnknown},
		{name: "publisher mismatch", publisher: "publisher:other", signing: &kpackage.SigningConfig{KeyID: "trusted:key", PrivateKey: private}, want: PublisherTrustPublisherMismatch},
	} {
		t.Run(test.name, func(t *testing.T) {
			archive := publisherTestArchive(t, test.publisher, test.signing)
			pkg, err := service.ValidateImportArchive(context.Background(), archive)
			if err != nil {
				t.Fatal(err)
			}
			preview, err := service.PreviewImport(context.Background(), pkg)
			if err != nil {
				t.Fatal(err)
			}
			if preview.PublisherTrust.State != test.want || preview.PublisherTrust.Trusted != test.trusted {
				t.Fatalf("PublisherTrust = %#v, want state=%s trusted=%v", preview.PublisherTrust, test.want, test.trusted)
			}
		})
	}
}

func TestTrustedPublisherNeverBypassesChunkPolicy(t *testing.T) {
	t.Parallel()
	public, private := publisherTestKey(0x41)
	registry, err := NewPublisherRegistry([]TrustedPublisher{{ID: "publisher:trusted", Keys: map[string]ed25519.PublicKey{"trusted:key": public}}})
	if err != nil {
		t.Fatal(err)
	}
	denied := errors.New("publisher trust is not authority")
	service := publisherTestService(t, registry, ChunkPolicyFunc(func(context.Context, memory.Actor, ChunkPolicyAction, memory.Chunk) error { return denied }))
	pkg, err := service.ValidateImportArchive(context.Background(), publisherTestArchive(t, "publisher:trusted", &kpackage.SigningConfig{KeyID: "trusted:key", PrivateKey: private}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.PreviewImport(context.Background(), pkg); !errors.Is(err, denied) {
		t.Fatalf("PreviewImport(trusted, denied) error = %v, want policy denial", err)
	}
}

func TestPublisherRegistryRejectsConflictingExplicitVerificationKey(t *testing.T) {
	t.Parallel()
	registered, _ := publisherTestKey(0x51)
	conflicting, _ := publisherTestKey(0x52)
	registry, err := NewPublisherRegistry([]TrustedPublisher{{
		ID: "publisher:trusted", Keys: map[string]ed25519.PublicKey{"trusted:key": registered},
	}})
	if err != nil {
		t.Fatal(err)
	}
	backend := memoryBackend.New()
	t.Cleanup(func() { _ = backend.Close() })
	_, err = New(Config{
		Store: backend, PublisherRegistry: registry,
		Actor: ContextActorSource(memory.Actor{Kind: memory.ActorKindUser, ID: "user:test"}),
		ImportValidation: kpackage.ValidationOptions{VerificationKeys: map[string]ed25519.PublicKey{
			"trusted:key": conflicting,
		}},
	})
	if err == nil {
		t.Fatal("New() accepted conflicting verification key ownership")
	}
}

func publisherTestService(t *testing.T, registry *PublisherRegistry, policy ChunkPolicy) *Service {
	t.Helper()
	backend := memoryBackend.New()
	t.Cleanup(func() { _ = backend.Close() })
	service, err := New(Config{
		Store: backend, PublisherRegistry: registry, ChunkPolicy: policy,
		Actor:            ContextActorSource(memory.Actor{Kind: memory.ActorKindUser, ID: "user:test"}),
		ImportValidation: kpackage.ValidationOptions{CurrentKoderVersion: "r9999"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func publisherTestArchive(t *testing.T, publisherID string, signing *kpackage.SigningConfig) []byte {
	t.Helper()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	request := kpackage.ExportRequest{
		Package:   kpackage.Identity{ID: "01a02b00-0000-7000-8000-000000000500", Version: "1.0.0"},
		Publisher: kpackage.Publisher{ID: publisherID, Name: "Publisher test"}, License: kpackage.License{Name: "MIT"},
		CreatedAt: now, Signing: signing,
		Chunk: memory.Chunk{
			ID: "01a02b00-0000-7000-8000-000000000501", Title: "Publisher trust test", Kind: memory.ChunkKindReference,
			Scope: memory.Scope{Kind: memory.ScopeKindGlobal}, Visibility: memory.VisibilityPublic,
			State: memory.ChunkStateActive, SchemaVersion: 1, Publisher: memory.Publisher{ID: publisherID, Name: "Publisher test"},
			License: "MIT", MinKoderVersion: "r0000", CreatedAt: now, UpdatedAt: now,
			Revision: memory.Revision{
				Number: 1, ID: "01a02b00-0000-7000-8000-000000000502",
				Actor: memory.Actor{Kind: memory.ActorKindSystem, ID: publisherID}, CreatedAt: now,
			},
		},
	}
	var output bytes.Buffer
	if _, err := kpackage.Export(&output, request); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func publisherTestKey(fill byte) (ed25519.PublicKey, ed25519.PrivateKey) {
	seed := bytes.Repeat([]byte{fill}, ed25519.SeedSize)
	private := ed25519.NewKeyFromSeed(seed)
	return private.Public().(ed25519.PublicKey), private
}

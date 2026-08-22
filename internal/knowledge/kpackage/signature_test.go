package kpackage

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestSignAndVerifyManifest(t *testing.T) {
	t.Parallel()
	publicKey, privateKey := deterministicSigningKey(t, 0x42)
	manifest, _, err := buildExport(canonicalExampleRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	unsignedBytes, err := SigningBytes(manifest)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := SignManifest(manifest, "publisher:example:2026", privateKey)
	if err != nil {
		t.Fatalf("SignManifest() error = %v", err)
	}
	if signed.Signature == nil || signed.Signature.Algorithm != SignatureAlgorithmEd25519 || signed.Signature.KeyID != "publisher:example:2026" {
		t.Fatalf("signature envelope = %#v", signed.Signature)
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(signed.Signature.Value)
	if err != nil || len(decoded) != ed25519.SignatureSize {
		t.Fatalf("signature value is not canonical Ed25519 base64: length=%d err=%v", len(decoded), err)
	}
	if err := VerifyManifest(signed, publicKey); err != nil {
		t.Fatalf("VerifyManifest() error = %v", err)
	}
	signedBytes, err := SigningBytes(signed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(unsignedBytes, signedBytes) {
		t.Fatal("signature envelope changed the signed manifest bytes")
	}
	again, err := SignManifest(signed, "publisher:example:2026", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if again.Signature.Value != signed.Signature.Value {
		t.Fatal("repeated Ed25519 signing was not deterministic")
	}
}

func TestVerifyManifestRejectsTamperingAndMalformedEnvelopes(t *testing.T) {
	t.Parallel()
	publicKey, privateKey := deterministicSigningKey(t, 0x24)
	manifest, _, err := buildExport(canonicalExampleRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	signed, err := SignManifest(manifest, "publisher:test", privateKey)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*Manifest)
		key    ed25519.PublicKey
		want   error
	}{
		{name: "unsigned", mutate: func(value *Manifest) { value.Signature = nil }, key: publicKey, want: ErrUnsigned},
		{name: "chunk", mutate: func(value *Manifest) { value.Chunk.Title = "Tampered" }, key: publicKey, want: ErrInvalidSignature},
		{name: "hash", mutate: func(value *Manifest) { value.Files[0].SHA256 = strings.Repeat("0", 64) }, key: publicKey, want: ErrInvalidSignature},
		{name: "algorithm", mutate: func(value *Manifest) { value.Signature.Algorithm = "rsa" }, key: publicKey, want: ErrInvalidSignature},
		{name: "key ID", mutate: func(value *Manifest) { value.Signature.KeyID = "bad key" }, key: publicKey, want: ErrInvalidSignature},
		{name: "base64", mutate: func(value *Manifest) { value.Signature.Value = "%%%" }, key: publicKey, want: ErrInvalidSignature},
		{name: "short signature", mutate: func(value *Manifest) { value.Signature.Value = base64.StdEncoding.EncodeToString([]byte("short")) }, key: publicKey, want: ErrInvalidSignature},
		{name: "wrong key", key: deterministicPublicKey(t, 0x25), want: ErrInvalidSignature},
		{name: "short key", key: ed25519.PublicKey{1, 2}, want: ErrInvalidSignature},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneManifest(t, signed)
			if test.mutate != nil {
				test.mutate(&candidate)
			}
			if err := VerifyManifest(candidate, test.key); !errors.Is(err, test.want) {
				t.Fatalf("VerifyManifest() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestExportOptionallySignsManifest(t *testing.T) {
	t.Parallel()
	publicKey, privateKey := deterministicSigningKey(t, 0x61)
	request := canonicalExampleRequest(t)
	request.Signing = &SigningConfig{KeyID: "publisher:example:development", PrivateKey: privateKey}
	var first bytes.Buffer
	result, err := Export(&first, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyManifest(result.Manifest, publicKey); err != nil {
		t.Fatalf("verify exported manifest: %v", err)
	}
	manifestData, err := canonicalJSON(result.Manifest, true)
	if err != nil {
		t.Fatal(err)
	}
	validateManifestSchema(t, manifestData)
	archive, err := zip.NewReader(bytes.NewReader(first.Bytes()), int64(first.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if got := readZIPFile(t, archive.File[0]); !bytes.Equal(got, manifestData) {
		t.Fatal("signed manifest in ZIP differs from the verified result manifest")
	}
	var second bytes.Buffer
	if _, err := Export(&second, request); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("signed exports are not deterministic")
	}

	request.Signing.KeyID = "bad key"
	invalid := new(bytes.Buffer)
	if _, err := Export(invalid, request); err == nil || invalid.Len() != 0 {
		t.Fatalf("invalid signing configuration error=%v bytes=%d", err, invalid.Len())
	}
	request.Signing.KeyID = "publisher:example"
	request.Signing.PrivateKey = ed25519.PrivateKey{1, 2, 3}
	if _, err := Export(new(bytes.Buffer), request); err == nil {
		t.Fatal("short private key was accepted")
	}
}

func deterministicSigningKey(t *testing.T, value byte) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{value}, ed25519.SeedSize))
	publicKey, ok := privateKey.Public().(ed25519.PublicKey)
	if !ok {
		t.Fatal("Ed25519 key has unexpected public key type")
	}
	return publicKey, privateKey
}

func deterministicPublicKey(t *testing.T, value byte) ed25519.PublicKey {
	t.Helper()
	publicKey, _ := deterministicSigningKey(t, value)
	return publicKey
}

func cloneManifest(t *testing.T, value Manifest) Manifest {
	t.Helper()
	data, err := canonicalJSON(value, false)
	if err != nil {
		t.Fatal(err)
	}
	var clone Manifest
	if err := json.Unmarshal(data, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

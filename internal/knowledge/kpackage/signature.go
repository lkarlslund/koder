package kpackage

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
)

const SignatureAlgorithmEd25519 = "ed25519"

var (
	ErrUnsigned         = errors.New("knowledge package is unsigned")
	ErrInvalidSignature = errors.New("invalid knowledge package signature")
)

// SigningBytes returns the canonical manifest bytes authenticated by a package
// signature. The signature envelope itself is always omitted.
func SigningBytes(manifest Manifest) ([]byte, error) {
	manifest.Signature = nil
	encoded, err := canonicalJSON(manifest, true)
	if err != nil {
		return nil, fmt.Errorf("encode knowledge package signature input: %w", err)
	}
	return encoded, nil
}

// SignManifest returns a copy of manifest with a deterministic Ed25519 signature.
func SignManifest(manifest Manifest, keyID string, privateKey ed25519.PrivateKey) (Manifest, error) {
	if !tokenPattern.MatchString(keyID) {
		return Manifest{}, errors.New("knowledge package signing key ID is invalid")
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return Manifest{}, fmt.Errorf("knowledge package Ed25519 private key has size %d, want %d", len(privateKey), ed25519.PrivateKeySize)
	}
	input, err := SigningBytes(manifest)
	if err != nil {
		return Manifest{}, err
	}
	signature := ed25519.Sign(privateKey, input)
	manifest.Signature = &Signature{
		Algorithm: SignatureAlgorithmEd25519,
		KeyID:     keyID,
		Value:     base64.StdEncoding.EncodeToString(signature),
	}
	return manifest, nil
}

// VerifyManifest verifies authenticity only. Publisher trust and import authority remain
// service policy and are deliberately outside this package.
func VerifyManifest(manifest Manifest, publicKey ed25519.PublicKey) error {
	if manifest.Signature == nil {
		return ErrUnsigned
	}
	if manifest.Signature.Algorithm != SignatureAlgorithmEd25519 || !tokenPattern.MatchString(manifest.Signature.KeyID) {
		return ErrInvalidSignature
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: Ed25519 public key has size %d, want %d", ErrInvalidSignature, len(publicKey), ed25519.PublicKeySize)
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(manifest.Signature.Value)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return ErrInvalidSignature
	}
	input, err := SigningBytes(manifest)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}
	if !ed25519.Verify(publicKey, input, signature) {
		return ErrInvalidSignature
	}
	return nil
}

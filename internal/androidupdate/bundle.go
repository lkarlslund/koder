// Package androidupdate exposes the signed Android client embedded in Koder.
package androidupdate

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"sync"
)

const (
	manifestPath = "bundle/manifest.json"
	apkPath      = "bundle/koder.apk"
)

//go:embed bundle/*
var embedded embed.FS

// Manifest identifies one signed Android application update.
type Manifest struct {
	Channel                  string `json:"channel"`
	ApplicationID            string `json:"application_id"`
	VersionCode              int64  `json:"version_code"`
	VersionName              string `json:"version_name"`
	SourceFingerprint        string `json:"source_fingerprint"`
	SigningCertificateSHA256 string `json:"signing_certificate_sha256"`
	APKSHA256                string `json:"apk_sha256"`
	APKSize                  int64  `json:"apk_size"`
	MinimumVoiceProtocol     string `json:"minimum_voice_protocol"`
	DownloadURI              string `json:"download_uri,omitempty"`
}

// Bundle owns a validated APK and its metadata.
type Bundle struct {
	fs   fs.FS
	once sync.Once
	meta Manifest
	err  error
}

// Embedded returns the process-wide embedded Android bundle.
func Embedded() *Bundle { return embeddedBundle }

var embeddedBundle = &Bundle{fs: embedded}

// Manifest returns the validated update metadata. An APK-less build is not an
// error and reports available=false.
func (b *Bundle) Manifest() (meta Manifest, available bool, err error) {
	b.once.Do(func() { b.meta, b.err = load(b.fs) })
	if b.err != nil {
		if isMissingBundle(b.err) {
			return Manifest{}, false, nil
		}
		return Manifest{}, false, b.err
	}
	return b.meta, true, nil
}

// OpenAPK opens a new reader for the validated APK.
func (b *Bundle) OpenAPK() (fs.File, error) {
	if _, available, err := b.Manifest(); err != nil {
		return nil, err
	} else if !available {
		return nil, fs.ErrNotExist
	}
	return b.fs.Open(apkPath)
}

func load(source fs.FS) (Manifest, error) {
	manifestData, err := fs.ReadFile(source, manifestPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("read Android update manifest: %w", err)
	}
	var meta Manifest
	decoder := json.NewDecoder(strings.NewReader(string(manifestData)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&meta); err != nil {
		return Manifest{}, fmt.Errorf("decode Android update manifest: %w", err)
	}
	if err := meta.validate(); err != nil {
		return Manifest{}, err
	}
	apk, err := source.Open(apkPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("open embedded Android APK: %w", err)
	}
	defer func() { _ = apk.Close() }()
	info, err := fs.Stat(source, apkPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("stat embedded Android APK: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() != meta.APKSize {
		return Manifest{}, fmt.Errorf("embedded Android APK size is %d; manifest declares %d", info.Size(), meta.APKSize)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, apk); err != nil {
		return Manifest{}, fmt.Errorf("hash embedded Android APK: %w", err)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != meta.APKSHA256 {
		return Manifest{}, fmt.Errorf("embedded Android APK SHA-256 is %s; manifest declares %s", got, meta.APKSHA256)
	}
	return meta, nil
}

func (m Manifest) validate() error {
	if m.Channel != "local" && m.Channel != "release" {
		return fmt.Errorf("android update channel %q is invalid", m.Channel)
	}
	if strings.TrimSpace(m.ApplicationID) == "" || m.VersionCode <= 0 || strings.TrimSpace(m.VersionName) == "" {
		return fmt.Errorf("android update application identity is incomplete")
	}
	if !validSHA256(m.SourceFingerprint) || !validSHA256(m.SigningCertificateSHA256) || !validSHA256(m.APKSHA256) {
		return fmt.Errorf("android update digest metadata is invalid")
	}
	if m.APKSize <= 0 {
		return fmt.Errorf("android update APK size must be positive")
	}
	if m.MinimumVoiceProtocol != "voice.v1" {
		return fmt.Errorf("android update requires unsupported protocol %q", m.MinimumVoiceProtocol)
	}
	return nil
}

func validSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}

func isMissingBundle(err error) bool {
	return strings.Contains(err.Error(), manifestPath) && strings.Contains(err.Error(), fs.ErrNotExist.Error())
}

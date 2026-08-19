package androidupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"testing/fstest"
)

func TestBundleManifestAndAPK(t *testing.T) {
	apk := []byte("signed apk fixture")
	meta := validManifest(apk)
	source := mapFS(t, meta, apk)
	bundle := &Bundle{fs: source}

	got, available, err := bundle.Manifest()
	if err != nil || !available {
		t.Fatalf("Manifest() available=%v err=%v", available, err)
	}
	if got != meta {
		t.Fatalf("Manifest()=%#v want %#v", got, meta)
	}
	file, err := bundle.OpenAPK()
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestBundleMissingIsUnavailable(t *testing.T) {
	bundle := &Bundle{fs: fstest.MapFS{"bundle/.keep": {Data: nil}}}
	_, available, err := bundle.Manifest()
	if err != nil || available {
		t.Fatalf("Manifest() available=%v err=%v", available, err)
	}
}

func TestBundleRejectsInvalidContent(t *testing.T) {
	apk := []byte("signed apk fixture")
	tests := map[string]func(*Manifest){
		"channel":  func(m *Manifest) { m.Channel = "nightly" },
		"identity": func(m *Manifest) { m.ApplicationID = "" },
		"digest":   func(m *Manifest) { m.APKSHA256 = "bad" },
		"size":     func(m *Manifest) { m.APKSize++ },
		"protocol": func(m *Manifest) { m.MinimumVoiceProtocol = "voice.v2" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			meta := validManifest(apk)
			mutate(&meta)
			bundle := &Bundle{fs: mapFS(t, meta, apk)}
			if _, _, err := bundle.Manifest(); err == nil {
				t.Fatal("Manifest() unexpectedly succeeded")
			}
		})
	}
}

func validManifest(apk []byte) Manifest {
	digest := sha256.Sum256(apk)
	sha := hex.EncodeToString(digest[:])
	return Manifest{
		Channel:                  "local",
		ApplicationID:            "com.lkarlslund.koder.dev",
		VersionCode:              2,
		VersionName:              "0.1.0-local.test",
		SourceFingerprint:        sha,
		SigningCertificateSHA256: sha,
		APKSHA256:                sha,
		APKSize:                  int64(len(apk)),
		MinimumVoiceProtocol:     "voice.v1",
	}
}

func mapFS(t *testing.T, meta Manifest, apk []byte) fstest.MapFS {
	t.Helper()
	manifest, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	return fstest.MapFS{
		manifestPath: {Data: manifest},
		apkPath:      {Data: apk},
	}
}

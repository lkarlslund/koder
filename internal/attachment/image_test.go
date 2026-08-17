package attachment

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadImageConvertsBinaryPortablePixmapToPNG(t *testing.T) {
	path := filepath.Join(t.TempDir(), "qemu-screen.png")
	ppm := append([]byte("P6\n# qemu screendump\n2 1\n255\n"), 0xff, 0, 0, 0, 0x80, 0xff)
	if err := os.WriteFile(path, ppm, 0o600); err != nil {
		t.Fatal(err)
	}

	data, mimeType, err := LoadImage(path)
	if err != nil {
		t.Fatal(err)
	}
	if mimeType != "image/png" {
		t.Fatalf("mime type = %q, want image/png", mimeType)
	}
	decoded, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode converted PNG: %v", err)
	}
	if got := decoded.Bounds().Size(); got.X != 2 || got.Y != 1 {
		t.Fatalf("converted dimensions = %v, want 2x1", got)
	}
}

func TestLoadImageRejectsExtensionMasqueradingAsImage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-really.png")
	if err := os.WriteFile(path, []byte("plain text"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadImage(path); err == nil {
		t.Fatal("expected non-image content to be rejected")
	}
}

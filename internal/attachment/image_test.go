package attachment

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
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

func TestLoadImageValidatesRasterData(t *testing.T) {
	var encoded bytes.Buffer
	photo := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	photo.Set(0, 0, color.NRGBA{R: 0xff, G: 0x80, A: 0xff})
	if err := jpeg.Encode(&encoded, photo, nil); err != nil {
		t.Fatal(err)
	}
	valid := encoded.Bytes()
	validPath := filepath.Join(t.TempDir(), "valid.jpg")
	if err := os.WriteFile(validPath, valid, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, mimeType, err := LoadImage(validPath); err != nil || mimeType != "image/jpeg" {
		t.Fatalf("LoadImage(valid JPEG) MIME = %q, error = %v", mimeType, err)
	}

	corruptPath := filepath.Join(t.TempDir(), "corrupt.jpg")
	if err := os.WriteFile(corruptPath, valid[:len(valid)-20], 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadImage(corruptPath); err == nil {
		t.Fatal("expected corrupt JPEG to be rejected")
	}
}

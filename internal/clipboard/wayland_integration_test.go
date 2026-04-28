package clipboard

import (
	"os"
	"testing"
)

func TestWaylandClipboardPOC(t *testing.T) {
	if os.Getenv("KODER_WAYLAND_CLIPBOARD_POC") == "" {
		t.Skip("set KODER_WAYLAND_CLIPBOARD_POC=1 to exercise the live Wayland clipboard")
	}

	reader := NewWayland()
	if !reader.Supported() {
		t.Fatal("Wayland reader unsupported in current environment")
	}

	switch os.Getenv("KODER_WAYLAND_CLIPBOARD_MODE") {
	case "files":
		paths, err := reader.ReadFileList()
		if err != nil {
			t.Fatalf("ReadFileList() error = %v", err)
		}
		t.Logf("wayland clipboard files=%v", paths)
	case "text":
		text, err := reader.ReadText()
		if err != nil {
			t.Fatalf("ReadText() error = %v", err)
		}
		t.Logf("wayland clipboard text bytes=%d", len(text))
	default:
		data, err := reader.ReadImage()
		if err != nil {
			t.Fatalf("ReadImage() error = %v", err)
		}
		t.Logf("wayland clipboard image bytes=%d", len(data))
	}
}

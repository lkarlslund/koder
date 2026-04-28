package clipboard

import (
	"errors"
	"sync"
	"testing"

	xclipboard "golang.design/x/clipboard"
)

type fakeWaylandReader struct {
	supported bool
	text      string
	textErr   error
	image     []byte
	imageErr  error
}

func (f fakeWaylandReader) Supported() bool         { return f.supported }
func (f fakeWaylandReader) ReadText() (string, error) { return f.text, f.textErr }
func (f fakeWaylandReader) ReadImage() ([]byte, error) { return f.image, f.imageErr }

func TestReadImagePrefersWaylandOnWayland(t *testing.T) {
	restore := stubClipboardBackends(t)
	defer restore()

	newWaylandReader = func() waylandReader {
		return fakeWaylandReader{supported: true, image: []byte("png")}
	}
	xclipboardInit = func() error {
		t.Fatal("xclipboard init should not be called when Wayland image succeeds")
		return nil
	}

	got, err := ReadImage()
	if err != nil {
		t.Fatalf("ReadImage() error = %v", err)
	}
	if string(got) != "png" {
		t.Fatalf("ReadImage() = %q, want %q", got, "png")
	}
}

func TestReadImageFallsBackWhenWaylandClipboardEmpty(t *testing.T) {
	restore := stubClipboardBackends(t)
	defer restore()

	newWaylandReader = func() waylandReader {
		return fakeWaylandReader{supported: true, imageErr: ErrClipboardEmpty}
	}
	xclipboardInit = func() error { return nil }
	xclipboardRead = func(format xclipboard.Format) []byte {
		if format != xclipboard.FmtImage {
			t.Fatalf("unexpected format %v", format)
		}
		return []byte("fallback")
	}

	got, err := ReadImage()
	if err != nil {
		t.Fatalf("ReadImage() error = %v", err)
	}
	if string(got) != "fallback" {
		t.Fatalf("ReadImage() = %q, want %q", got, "fallback")
	}
}

func TestReadTextFallsBackWhenWaylandUnavailable(t *testing.T) {
	restore := stubClipboardBackends(t)
	defer restore()

	newWaylandReader = func() waylandReader {
		return fakeWaylandReader{supported: true, textErr: ErrWaylandUnsupported}
	}
	xclipboardInit = func() error { return nil }
	xclipboardRead = func(format xclipboard.Format) []byte {
		if format != xclipboard.FmtText {
			t.Fatalf("unexpected format %v", format)
		}
		return []byte("hello")
	}

	got, err := ReadText()
	if err != nil {
		t.Fatalf("ReadText() error = %v", err)
	}
	if got != "hello" {
		t.Fatalf("ReadText() = %q, want %q", got, "hello")
	}
}

func TestReadImageReturnsWaylandErrorWhenFallbackAlsoFails(t *testing.T) {
	restore := stubClipboardBackends(t)
	defer restore()

	wantErr := errors.New("wayland boom")
	newWaylandReader = func() waylandReader {
		return fakeWaylandReader{supported: true, imageErr: wantErr}
	}
	xclipboardInit = func() error { return errors.New("xclipboard unavailable") }

	_, err := ReadImage()
	if !errors.Is(err, wantErr) {
		t.Fatalf("ReadImage() error = %v, want %v", err, wantErr)
	}
}

func TestSupportedUsesWaylandOpportunistically(t *testing.T) {
	restore := stubClipboardBackends(t)
	defer restore()

	newWaylandReader = func() waylandReader {
		return fakeWaylandReader{supported: true}
	}
	xclipboardInit = func() error {
		t.Fatal("xclipboard init should not run when Wayland is supported")
		return nil
	}

	if !Supported() {
		t.Fatal("Supported() = false, want true")
	}
}

func stubClipboardBackends(t *testing.T) func() {
	t.Helper()

	oldNewWaylandReader := newWaylandReader
	oldInit := xclipboardInit
	oldRead := xclipboardRead
	oldWrite := xclipboardWrite
	oldInitErr := initErr
	oldInitOnce := initOnce

	initOnce = sync.Once{}
	initErr = nil

	return func() {
		newWaylandReader = oldNewWaylandReader
		xclipboardInit = oldInit
		xclipboardRead = oldRead
		xclipboardWrite = oldWrite
		initErr = oldInitErr
		initOnce = oldInitOnce
	}
}

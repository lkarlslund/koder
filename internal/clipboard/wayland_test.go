package clipboard

import (
	"errors"
	"reflect"
	"runtime"
	"testing"
)

func TestParseURIList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "multiple file uris",
			in:   "file:///tmp/one.png\nfile:///tmp/two%20words.jpg\n",
			want: []string{"/tmp/one.png", "/tmp/two words.jpg"},
		},
		{
			name: "ignores comments and non-file uris",
			in:   "# metadata\nhttps://example.com/image.png\nfile:///tmp/three.png\n",
			want: []string{"/tmp/three.png"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseURIList([]byte(tc.in))
			if err != nil {
				t.Fatalf("parseURIList() error = %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parseURIList() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestWaylandSupported(t *testing.T) {
	w := NewWayland()
	t.Setenv("WAYLAND_DISPLAY", "")
	if w.Supported() {
		t.Fatal("Supported() = true without WAYLAND_DISPLAY")
	}

	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	got := w.Supported()
	if got != (runtime.GOOS == "linux") {
		t.Fatalf("Supported() = %v, want %v", got, runtime.GOOS == "linux")
	}
}

func TestReadWithoutWayland(t *testing.T) {
	w := NewWayland()
	t.Setenv("WAYLAND_DISPLAY", "")
	_, err := w.ReadImage()
	if !errors.Is(err, ErrWaylandUnsupported) {
		t.Fatalf("ReadImage() error = %v, want %v", err, ErrWaylandUnsupported)
	}
}

package viewpdftool

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/tools"
)

type fakeBackend struct {
	available bool
	pages     int
	err       error
}

func (f fakeBackend) Available() bool                                { return f.available }
func (f fakeBackend) PageCount(context.Context, string) (int, error) { return f.pages, f.err }
func (f fakeBackend) RenderPage(_ context.Context, _ string, _ int, prefix string) error {
	if f.err != nil {
		return f.err
	}
	file, err := os.Create(prefix + ".png")
	if err != nil {
		return err
	}
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.NRGBA{R: 0xff, A: 0xff})
	if err := png.Encode(file, img); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func TestNormalizeArgsDefaultsAndValidatesPage(t *testing.T) {
	args, err := (tool{}).NormalizeArgs(map[string]string{"path": "report.pdf"})
	if err != nil || args["page"] != "1" {
		t.Fatalf("NormalizeArgs() = %#v, %v", args, err)
	}
	for _, page := range []string{"0", "-1", "nope"} {
		if _, err := (tool{}).NormalizeArgs(map[string]string{"path": "report.pdf", "page": page}); err == nil {
			t.Fatalf("expected page %q to fail", page)
		}
	}
}

func TestDefinitionRequiresRenderer(t *testing.T) {
	if _, ok := (tool{backend: fakeBackend{}}).Definition(tools.Runtime{}, tools.ToolSpec{}); ok {
		t.Fatal("expected unavailable renderer to hide tool")
	}
	if _, ok := (tool{backend: fakeBackend{available: true}}).Definition(tools.Runtime{}, tools.ToolSpec{}); !ok {
		t.Fatal("expected available renderer to expose tool")
	}
}

func TestCallRendersSelectedPage(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "report.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := tools.Runtime{Workdir: workspace, SessionID: id.New()}
	t.Cleanup(func() { _ = os.RemoveAll(runtime.SessionTmpDir()) })
	result, err := (tool{backend: fakeBackend{available: true, pages: 4}}).Call(t.Context(), tools.Options{
		Runtime: runtime,
		Request: tools.Request{Args: map[string]string{"path": "report.pdf", "page": "3"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := result.Stored.(tools.ViewImageStoredResult)
	if !ok || stored.Page != 3 || stored.PageCount != 4 || stored.MIMEType != "image/png" {
		t.Fatalf("stored result = %#v", result.Stored)
	}
	if _, err := os.Stat(stored.SourcePath); err != nil {
		t.Fatalf("rendered page missing: %v", err)
	}
	if !strings.Contains(result.Output, "page 3 of 4") {
		t.Fatalf("output = %q", result.Output)
	}
}

func TestCallRejectsOutOfRangePage(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "report.pdf"), []byte("%PDF-1.7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := (tool{backend: fakeBackend{available: true, pages: 2}}).Call(t.Context(), tools.Options{
		Runtime: tools.Runtime{Workdir: workspace},
		Request: tools.Request{Args: map[string]string{"path": "report.pdf", "page": "3"}},
	})
	if err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("expected range error, got %v", err)
	}
}

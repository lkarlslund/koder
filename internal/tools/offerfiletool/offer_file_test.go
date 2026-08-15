package offerfiletool

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lkarlslund/koder/internal/offeredfile"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

func TestOfferFileCreatesLiveCapability(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "report.json")
	if err := os.WriteFile(path, []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	manager := offeredfile.NewManager(st)

	result, err := (tool{}).Call(context.Background(), tools.Options{
		Runtime: tools.Runtime{Workdir: root, SessionID: "session-1", ChatID: "chat-1", OfferedFiles: manager},
		Request: tools.Request{Args: map[string]string{"path": "report.json", "title": "Results"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := result.Stored.(tools.OfferFileStoredResult)
	if !ok {
		t.Fatalf("stored result type = %T", result.Stored)
	}
	if stored.Name != "report.json" || stored.MIMEType != "application/json" || stored.Size == 0 || stored.Token == "" {
		t.Fatalf("stored result = %#v", stored)
	}
	record, err := manager.Resolve(context.Background(), stored.Token)
	if err != nil {
		t.Fatal(err)
	}
	if record.Path != path || record.Title != "Results" {
		t.Fatalf("record = %#v", record)
	}
}

func TestOfferFileIsExposed(t *testing.T) {
	if !tools.Info(tools.OfferFile).ExposeToLLM {
		t.Fatal("offer_file should be exposed to the model")
	}
}

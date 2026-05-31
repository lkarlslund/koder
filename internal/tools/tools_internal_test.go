package tools

import (
	"context"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools/tooltest"
)

type badStoredResult struct {
	Bad func()
}

func (badStoredResult) storedResultPayload() {}

func openToolsStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestPersistStandardResultReturnsStoredMarshalError(t *testing.T) {
	st := openToolsStore(t)
	session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = PersistStandardResult(context.Background(), Runtime{Store: st, SessionID: session.ID, SessionControl: tooltest.NewSessionControl(st)}, Request{
		Tool: domain.ToolKindQuestion,
		Args: map[string]string{"question": "What next?"},
	}, Result{
		Output: "What next?",
		Stored: badStoredResult{Bad: func() {}},
	})
	if err == nil {
		t.Fatal("expected stored result marshal error")
	}
}

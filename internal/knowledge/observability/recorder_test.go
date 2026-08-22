package observability

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRecorderAggregatesBoundedPrivacySafeOperations(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 22, 20, 0, 0, 0, time.UTC)
	ids := []string{"operation-1", "operation-2", "operation-3"}
	recorder := NewRecorder(Config{
		Now: func() time.Time {
			current := now
			now = now.Add(7 * time.Millisecond)
			return current
		},
		NewID: func() string {
			id := ids[0]
			ids = ids[1:]
			return id
		},
		MaxRecent: 2,
	})

	first := recorder.Start(OperationSearch, "request-1")
	first.Finish(OutcomeSucceeded, 1, 3)
	first.Finish(OutcomeFailed, 99, 99)
	second := recorder.Start(OperationSearch, "unsafe audit contains a query")
	second.Finish(OutcomeEmpty, 1, 0)
	third := recorder.Start(OperationImportPreview, "request-3")
	third.Finish(OutcomeFailed, 5, 0)

	snapshot := recorder.Snapshot()
	if len(snapshot.Operations) != 2 || len(snapshot.Recent) != 2 {
		t.Fatalf("Snapshot() = %#v", snapshot)
	}
	search := snapshot.Operations[1]
	if search.Operation != OperationSearch || search.Started != 2 || search.Completed != 2 ||
		search.Succeeded != 1 || search.Empty != 1 || search.Failed != 0 || search.TotalDurationMS != 14 {
		t.Fatalf("search metric = %#v", search)
	}
	if snapshot.Recent[0].OperationID != "operation-2" || snapshot.Recent[0].AuditID != "" ||
		snapshot.Recent[1].OperationID != "operation-3" {
		t.Fatalf("recent observations = %#v", snapshot.Recent)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "unsafe audit") || strings.Contains(string(encoded), "query") {
		t.Fatalf("snapshot disclosed rejected audit content: %s", encoded)
	}
}

func TestRecorderRejectsUnregisteredOperationClasses(t *testing.T) {
	t.Parallel()
	recorder := NewRecorder(Config{NewID: func() string { return "unused" }})
	span := recorder.Start(Operation("search:private query"), "request-1")
	span.Finish(OutcomeSucceeded, 1, 1)
	if span.ID() != "" || len(recorder.Snapshot().Operations) != 0 {
		t.Fatalf("unregistered operation was recorded: %#v", recorder.Snapshot())
	}
}

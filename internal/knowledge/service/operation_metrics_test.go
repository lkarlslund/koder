package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge/observability"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestServiceRecordsCorrelatedSearchAndImportOperations(t *testing.T) {
	t.Parallel()
	backend := memory.New()
	t.Cleanup(func() { _ = backend.Close() })
	service := newImportTestService(t, backend, func() time.Time { return serviceTime }, 0x900)
	operationTime := serviceTime
	nextID := 0
	recorder := observability.NewRecorder(observability.Config{
		Now: func() time.Time {
			current := operationTime
			operationTime = operationTime.Add(time.Millisecond)
			return current
		},
		NewID: func() string {
			nextID++
			return "operation-" + string(rune('0'+nextID))
		},
	})
	service.operationRecorder = recorder
	ctx, err := WithAuditID(context.Background(), "request-safe-1")
	if err != nil {
		t.Fatal(err)
	}

	search, err := service.SearchLexical(ctx, LexicalSearchRequest{Query: "nothing private should enter metrics"})
	if err != nil || search.OperationID != "operation-1" {
		t.Fatalf("SearchLexical() = %#v, %v", search, err)
	}
	if _, err := service.ValidateImportArchive(ctx, []byte("invalid private archive bytes")); err == nil {
		t.Fatal("ValidateImportArchive() unexpectedly succeeded")
	}
	pkg := importTestPackage(t)
	preview, err := service.PreviewImport(ctx, pkg)
	if err != nil || preview.OperationID != "operation-3" {
		t.Fatalf("PreviewImport() = %#v, %v", preview, err)
	}
	stage, err := service.StageImport(ctx, StageImportRequest{Package: pkg})
	if err != nil || stage.OperationID != "operation-4" || stage.Preview.OperationID == "" {
		t.Fatalf("StageImport() = %#v, %v", stage, err)
	}
	activated, err := service.ActivateImport(ctx, stage.ID)
	if err != nil || activated.OperationID == "" {
		t.Fatalf("ActivateImport() = %#v, %v", activated, err)
	}

	snapshot := service.OperationMetrics()
	if len(snapshot.Operations) != 5 || len(snapshot.Recent) != 6 {
		t.Fatalf("OperationMetrics() = %#v", snapshot)
	}
	for _, observation := range snapshot.Recent {
		if observation.AuditID != "request-safe-1" {
			t.Fatalf("observation audit ID = %#v", observation)
		}
	}
	if snapshot.Operations[0].Operation != observability.OperationImportActivate || snapshot.Operations[0].Succeeded != 1 ||
		snapshot.Operations[1].Operation != observability.OperationImportPreview || snapshot.Operations[1].Succeeded != 2 ||
		snapshot.Operations[2].Operation != observability.OperationImportStage || snapshot.Operations[2].Succeeded != 1 ||
		snapshot.Operations[3].Operation != observability.OperationImportValidate || snapshot.Operations[3].Failed != 1 ||
		snapshot.Operations[4].Operation != observability.OperationSearch || snapshot.Operations[4].Empty != 1 {
		t.Fatalf("operation aggregates = %#v", snapshot.Operations)
	}
	status, err := service.OperationalStatus(context.Background())
	if err != nil || len(status.Operations.Operations) != 5 {
		t.Fatalf("OperationalStatus() metrics = %#v, %v", status.Operations, err)
	}
}

func TestOperationOutcomeClassifiesCancellation(t *testing.T) {
	t.Parallel()
	if got := operationOutcome(errors.Join(errors.New("wrapped"), context.Canceled), false); got != observability.OutcomeCanceled {
		t.Fatalf("operationOutcome() = %q", got)
	}
}

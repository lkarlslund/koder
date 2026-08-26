package curation

import (
	"context"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
)

type blockingExtractor struct {
	started chan struct{}
	release chan struct{}
}

func (e blockingExtractor) Extract(ctx context.Context, _ memory.CurationRecord) (ExtractionResult, error) {
	close(e.started)
	select {
	case <-ctx.Done():
		return ExtractionResult{}, ctx.Err()
	case <-e.release:
		return ExtractionResult{}, nil
	}
}

func TestCoordinatorDoesNotDelayCompletedTurnWhileExtractionBlocks(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	extractor := blockingExtractor{started: make(chan struct{}), release: make(chan struct{})}
	queue, err := New(Config{Store: NewMemoryStore(), Extractor: extractor, NewID: func() string { return "00000000-0000-7000-8000-000000000099" }})
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := NewCoordinator(ctx, queue, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(coordinator.Close)
	request := queueTestRequest()
	returned := make(chan bool, 1)
	go func() { returned <- coordinator.Observe(request) }()
	select {
	case accepted := <-returned:
		if !accepted {
			t.Fatal("first observation was not accepted")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Observe blocked on extraction")
	}
	select {
	case <-extractor.started:
	case <-time.After(time.Second):
		t.Fatal("background extraction did not start")
	}
	close(extractor.release)
}

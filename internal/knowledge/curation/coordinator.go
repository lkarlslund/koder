package curation

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

const defaultObservationBuffer = 128

// Coordinator keeps completed-turn curation entirely off the user-facing turn
// path. Observe is a bounded non-blocking notification; one worker serializes
// queue transitions and extraction so provider calls cannot fan out without limit.
type Coordinator struct {
	queue        *Queue
	automatic    interface{ ApplyAutomatic(context.Context) error }
	observations chan SubmitRequest
	cancel       context.CancelFunc
	done         chan struct{}
	closeOnce    sync.Once
}

func NewCoordinator(ctx context.Context, queue *Queue, buffer int, automatic ...interface{ ApplyAutomatic(context.Context) error }) (*Coordinator, error) {
	if ctx == nil || queue == nil {
		return nil, fmt.Errorf("%w: coordinator requires context and queue", ErrUnavailable)
	}
	if buffer <= 0 {
		buffer = defaultObservationBuffer
	}
	workerCtx, cancel := context.WithCancel(ctx)
	coordinator := &Coordinator{
		queue: queue, observations: make(chan SubmitRequest, buffer), cancel: cancel, done: make(chan struct{}),
	}
	if len(automatic) > 0 {
		coordinator.automatic = automatic[0]
	}
	go coordinator.run(workerCtx)
	return coordinator, nil
}

// Observe schedules a completed turn without waiting for store or model work.
// false means the bounded queue was full or the coordinator has stopped; chat
// completion itself remains successful either way.
func (c *Coordinator) Observe(request SubmitRequest) bool {
	if c == nil {
		return false
	}
	select {
	case <-c.done:
		return false
	default:
	}
	select {
	case c.observations <- request:
		return true
	default:
		return false
	}
}

func (c *Coordinator) Close() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		c.cancel()
		<-c.done
	})
}

func (c *Coordinator) run(ctx context.Context) {
	defer close(c.done)
	for {
		select {
		case <-ctx.Done():
			return
		case request := <-c.observations:
			if _, err := c.queue.Submit(ctx, request); err != nil {
				slog.Warn("schedule completed turn for knowledge curation", "error", safeCoordinatorError(err))
				continue
			}
			for {
				processed, err := c.queue.ProcessNext(ctx)
				if err != nil {
					slog.Warn("process completed turn for knowledge curation", "error", safeCoordinatorError(err))
					break
				}
				if !processed {
					break
				}
			}
			if c.automatic != nil {
				if err := c.automatic.ApplyAutomatic(ctx); err != nil {
					slog.Warn("apply automatic knowledge curation", "error", safeCoordinatorError(err))
				}
			}
		}
	}
}

func safeCoordinatorError(err error) string {
	if err == nil {
		return ""
	}
	// Queue errors are already wrapped around privacy-safe machine state. Avoid
	// logging model responses or completed-turn material from deeper causes.
	return normalizeErrorCode(err.Error())
}

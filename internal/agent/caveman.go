package agent

import (
	"context"
	"fmt"
	"sync"
)

type cavemanService struct {
	slots chan struct{}
}

type cavemanJob struct {
	state *cavemanJobState
}

type cavemanResult struct {
	text string
	err  error
}

type cavemanJobState struct {
	done   chan struct{}
	once   sync.Once
	mu     sync.Mutex
	result cavemanResult
}

func newCavemanService(parallelism int) *cavemanService {
	if parallelism <= 0 {
		parallelism = 1
	}
	return &cavemanService{slots: make(chan struct{}, parallelism)}
}

func (s *cavemanService) Submit(ctx context.Context, work func(context.Context) (string, error)) cavemanJob {
	state := &cavemanJobState{done: make(chan struct{})}
	complete := func(result cavemanResult) {
		state.once.Do(func() {
			state.mu.Lock()
			state.result = result
			state.mu.Unlock()
			close(state.done)
		})
	}
	go func() {
		if s == nil {
			complete(cavemanResult{err: fmt.Errorf("caveman service is unavailable")})
			return
		}
		select {
		case s.slots <- struct{}{}:
			defer func() { <-s.slots }()
		case <-ctx.Done():
			complete(cavemanResult{err: ctx.Err()})
			return
		}
		text, err := work(ctx)
		complete(cavemanResult{text: text, err: err})
	}()
	return cavemanJob{state: state}
}

func (j cavemanJob) Valid() bool {
	return j.state != nil && j.state.done != nil
}

func (j cavemanJob) Await(ctx context.Context) (string, error) {
	if !j.Valid() {
		return "", nil
	}
	select {
	case <-j.state.done:
		j.state.mu.Lock()
		result := j.state.result
		j.state.mu.Unlock()
		return result.text, result.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

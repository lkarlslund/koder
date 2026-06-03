package agent

import (
	"context"
	"fmt"
)

type cavemanService struct {
	slots chan struct{}
}

type cavemanJob struct {
	done <-chan cavemanResult
}

type cavemanResult struct {
	text string
	err  error
}

func newCavemanService(parallelism int) *cavemanService {
	if parallelism <= 0 {
		parallelism = 1
	}
	return &cavemanService{slots: make(chan struct{}, parallelism)}
}

func (s *cavemanService) Submit(ctx context.Context, work func(context.Context) (string, error)) cavemanJob {
	done := make(chan cavemanResult, 1)
	go func() {
		if s == nil {
			done <- cavemanResult{err: fmt.Errorf("caveman service is unavailable")}
			return
		}
		select {
		case s.slots <- struct{}{}:
			defer func() { <-s.slots }()
		case <-ctx.Done():
			done <- cavemanResult{err: ctx.Err()}
			return
		}
		text, err := work(ctx)
		done <- cavemanResult{text: text, err: err}
	}()
	return cavemanJob{done: done}
}

func (j cavemanJob) Valid() bool {
	return j.done != nil
}

func (j cavemanJob) Await(ctx context.Context) (string, error) {
	if !j.Valid() {
		return "", nil
	}
	select {
	case result := <-j.done:
		return result.text, result.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

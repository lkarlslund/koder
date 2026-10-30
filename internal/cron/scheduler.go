package cron

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/store"
)

const (
	// MaxJobsPerChat bounds how many jobs one chat can keep.
	MaxJobsPerChat = 20
	// MaxPromptBytes bounds a job's prompt.
	MaxPromptBytes = 8 << 10
	// RecurringMaxAge is how long a recurring job lives: it runs once more
	// after this age and is then deleted.
	RecurringMaxAge = 7 * 24 * time.Hour
	// missedAfter is how late a job may run. A job found later than this,
	// because Koder was not running, is missed: a recurring job skips to its
	// next run, and a one-shot job is reported to its chat and dropped.
	missedAfter = 5 * time.Minute
	// recurringJitterFrac and recurringJitterCap delay each recurring job
	// by a fixed share of its period, so jobs set for the same minute do not
	// all reach the model provider at once.
	recurringJitterFrac = 0.1
	recurringJitterCap  = 15 * time.Minute
	// oneShotJitterMax moves a one-shot job set for :00 or :30 up to this
	// much earlier, for the same reason.
	oneShotJitterMax = 90 * time.Second
	idleWait         = time.Minute
)

// Job is a prompt scheduled in a chat.
type Job struct {
	ID        string `json:"id"`
	SessionID id.ID  `json:"session_id"`
	ChatID    id.ID  `json:"chat_id"`
	// Cron is the 5-field schedule in the server's local time.
	Cron      string    `json:"cron"`
	Prompt    string    `json:"prompt"`
	Recurring bool      `json:"recurring"`
	CreatedAt time.Time `json:"created_at"`
	// NextRunAt is when the job runs next, its fixed offset included.
	NextRunAt   time.Time `json:"next_run_at"`
	LastFiredAt time.Time `json:"last_fired_at,omitzero"`
}

// Delivery says why a job reached its chat.
type Delivery int

const (
	// Due means the job came due and its prompt should run.
	Due Delivery = iota
	// Missed means a one-shot job came due while Koder was not running; it
	// has been dropped and must not run unless the user asks.
	Missed
)

// ErrChatGone, returned by a Deliver function, deletes the job: its chat no
// longer exists or is archived.
var ErrChatGone = errors.New("chat is gone")

// Deliver hands a job to its chat.
type Deliver func(ctx context.Context, job Job, delivery Delivery) error

// Scheduler keeps jobs in the store and delivers them when they come due.
type Scheduler struct {
	jobs    store.Collection[Job]
	deliver Deliver
	now     func() time.Time
	wake    chan struct{}
	// onChange, when set, is called after a job is added or removed.
	onChange func()
	// mu serializes changes to jobs between the tool and the run loop.
	mu sync.Mutex
}

// OnChange sets a function called, outside the scheduler's lock, after a
// job is added or removed.
func (s *Scheduler) OnChange(fn func()) {
	s.mu.Lock()
	s.onChange = fn
	s.mu.Unlock()
}

func (s *Scheduler) changed() {
	s.mu.Lock()
	fn := s.onChange
	s.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// Counts is how many jobs each session and each chat has.
type Counts struct {
	Sessions map[id.ID]int `json:"sessions"`
	Chats    map[id.ID]int `json:"chats"`
}

// Counts returns how many jobs each session and chat has.
func (s *Scheduler) Counts(ctx context.Context) (Counts, error) {
	s.mu.Lock()
	jobs, err := s.jobs.List(ctx, store.All[Job]())
	s.mu.Unlock()
	counts := Counts{Sessions: map[id.ID]int{}, Chats: map[id.ID]int{}}
	if err != nil {
		return counts, err
	}
	for _, job := range jobs {
		counts.Sessions[job.SessionID]++
		counts.Chats[job.ChatID]++
	}
	return counts, nil
}

// NewScheduler returns a scheduler over st. It delivers nothing until Run.
func NewScheduler(st *store.Store, deliver Deliver) *Scheduler {
	return &Scheduler{
		jobs: store.NewCollection(st, store.CollectionSpec[Job]{
			Namespace: "cron-jobs",
			GetID:     func(job Job) string { return job.ID },
			SetID:     func(job *Job, key string) { job.ID = key },
		}),
		deliver: deliver,
		now:     time.Now,
		wake:    make(chan struct{}, 1),
	}
}

// Create schedules prompt in a chat. spec is a cron expression or an
// interval such as 5m.
func (s *Scheduler) Create(ctx context.Context, sessionID, chatID id.ID, spec, prompt string, recurring bool) (Job, error) {
	if sessionID == "" || chatID == "" {
		return Job{}, errors.New("cron jobs need a persisted chat")
	}
	prompt = strings.TrimSpace(prompt)
	switch {
	case prompt == "":
		return Job{}, errors.New("a prompt is required")
	case len(prompt) > MaxPromptBytes:
		return Job{}, fmt.Errorf("the prompt is %d bytes; the limit is %d", len(prompt), MaxPromptBytes)
	}
	expr, err := Normalize(spec)
	if err != nil {
		return Job{}, err
	}
	job, err := s.create(ctx, sessionID, chatID, expr, prompt, recurring)
	if err == nil {
		s.changed()
	}
	return job, err
}

func (s *Scheduler) create(ctx context.Context, sessionID, chatID id.ID, expr, prompt string, recurring bool) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, err := s.listLocked(ctx, chatID)
	if err != nil {
		return Job{}, err
	}
	if len(existing) >= MaxJobsPerChat {
		return Job{}, fmt.Errorf("this chat already has %d cron jobs; delete one first", len(existing))
	}
	now := s.now()
	job := Job{ID: newJobID(), SessionID: sessionID, ChatID: chatID, Cron: expr, Prompt: prompt, Recurring: recurring, CreatedAt: now.UTC()}
	next, ok := s.nextRun(job, now)
	if !ok {
		return Job{}, fmt.Errorf("cron expression %q matches no time in the next %d years", expr, searchYears)
	}
	job.NextRunAt = next.UTC()
	if err := s.jobs.Put(ctx, job); err != nil {
		return Job{}, err
	}
	s.poke()
	return job, nil
}

// List returns a chat's jobs, the next to run first.
func (s *Scheduler) List(ctx context.Context, chatID id.ID) ([]Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listLocked(ctx, chatID)
}

func (s *Scheduler) listLocked(ctx context.Context, chatID id.ID) ([]Job, error) {
	jobs, err := s.jobs.List(ctx, store.Query[Job]{Filter: func(job Job) bool { return job.ChatID == chatID }})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(jobs, func(a, b Job) int { return a.NextRunAt.Compare(b.NextRunAt) })
	return jobs, nil
}

// Delete removes one of a chat's jobs.
func (s *Scheduler) Delete(ctx context.Context, chatID id.ID, jobID string) (Job, error) {
	job, err := s.delete(ctx, chatID, jobID)
	if err == nil {
		s.changed()
	}
	return job, err
}

func (s *Scheduler) delete(ctx context.Context, chatID id.ID, jobID string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, err := s.jobs.Get(ctx, strings.TrimSpace(jobID))
	if store.IsNotFound(err) || (err == nil && job.ChatID != chatID) {
		return Job{}, fmt.Errorf("this chat has no cron job %q", jobID)
	}
	if err != nil {
		return Job{}, err
	}
	if err := s.jobs.Delete(ctx, job.ID); err != nil {
		return Job{}, err
	}
	s.poke()
	return job, nil
}

// Run delivers jobs as they come due until ctx ends.
func (s *Scheduler) Run(ctx context.Context) {
	for {
		wait := s.tick(ctx)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.wake:
		case <-timer.C:
		}
		timer.Stop()
	}
}

func (s *Scheduler) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// tick delivers every job that is due and returns how long to wait for the
// next one.
func (s *Scheduler) tick(ctx context.Context) time.Duration {
	s.mu.Lock()
	jobs, err := s.jobs.List(ctx, store.All[Job]())
	s.mu.Unlock()
	if err != nil {
		slog.Warn("cron: list jobs", "error", err)
		return idleWait
	}
	wait := idleWait
	for _, job := range jobs {
		now := s.now()
		if until := job.NextRunAt.Sub(now); until > 0 {
			wait = min(wait, until)
			continue
		}
		s.run(ctx, job, now)
	}
	return max(wait, time.Second)
}

func (s *Scheduler) run(ctx context.Context, job Job, now time.Time) {
	late := now.Sub(job.NextRunAt)
	if late > missedAfter && job.Recurring {
		slog.Info("cron: skipped missed run", "job_id", job.ID, "chat_id", job.ChatID, "due", job.NextRunAt)
		s.reschedule(ctx, job, now)
		return
	}
	delivery := Due
	if late > missedAfter {
		delivery = Missed
	}
	err := s.deliver(ctx, job, delivery)
	switch {
	case errors.Is(err, ErrChatGone):
		s.remove(ctx, job, "chat gone")
		return
	case err != nil && delivery == Due && !job.Recurring:
		// A one-shot job tries again on the next tick, until it is missed.
		slog.Warn("cron: deliver job", "job_id", job.ID, "chat_id", job.ChatID, "error", err)
		return
	case err != nil:
		slog.Warn("cron: deliver job", "job_id", job.ID, "chat_id", job.ChatID, "error", err)
	}
	if !job.Recurring {
		s.remove(ctx, job, "ran once")
		return
	}
	if now.Sub(job.CreatedAt) >= RecurringMaxAge {
		s.remove(ctx, job, "expired")
		return
	}
	job.LastFiredAt = now.UTC()
	s.reschedule(ctx, job, now)
}

func (s *Scheduler) reschedule(ctx context.Context, job Job, now time.Time) {
	next, ok := s.nextRun(job, now)
	if !ok {
		s.remove(ctx, job, "no further runs")
		return
	}
	job.NextRunAt = next.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	// The job may have been deleted while it was being delivered.
	if _, err := s.jobs.Get(ctx, job.ID); err != nil {
		return
	}
	if err := s.jobs.Put(ctx, job); err != nil {
		slog.Warn("cron: save job", "job_id", job.ID, "error", err)
	}
}

func (s *Scheduler) remove(ctx context.Context, job Job, reason string) {
	s.mu.Lock()
	err := s.jobs.Delete(ctx, job.ID)
	s.mu.Unlock()
	if err != nil && !store.IsNotFound(err) {
		slog.Warn("cron: delete job", "job_id", job.ID, "error", err)
		return
	}
	slog.Info("cron: removed job", "job_id", job.ID, "chat_id", job.ChatID, "reason", reason)
	s.changed()
}

// nextRun is the job's first run after now in local time, with its fixed
// offset.
func (s *Scheduler) nextRun(job Job, now time.Time) (time.Time, bool) {
	schedule, err := Parse(job.Cron)
	if err != nil {
		return time.Time{}, false
	}
	now = now.Local()
	next := schedule.Next(now)
	if next.IsZero() {
		return time.Time{}, false
	}
	share := jobShare(job.ID)
	if job.Recurring {
		if after := schedule.Next(next); !after.IsZero() {
			offset := time.Duration(share * recurringJitterFrac * float64(after.Sub(next)))
			next = next.Add(min(offset, recurringJitterCap))
		}
		return next, true
	}
	if next.Minute()%30 == 0 {
		if early := next.Add(-time.Duration(share * float64(oneShotJitterMax))); early.After(now) {
			next = early
		}
	}
	return next, true
}

// jobShare maps a job ID to a fixed number in [0, 1).
func jobShare(jobID string) float64 {
	raw, err := hex.DecodeString(jobID)
	if err != nil || len(raw) < 4 {
		return 0
	}
	return float64(binary.BigEndian.Uint32(raw)) / (1 << 32)
}

func newJobID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

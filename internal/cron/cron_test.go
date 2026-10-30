package cron

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/store"
)

func at(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.ParseInLocation("2006-01-02 15:04", value, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestScheduleNext(t *testing.T) {
	for _, tc := range []struct{ expr, from, want string }{
		{"*/5 * * * *", "2026-10-30 10:03", "2026-10-30 10:05"},
		{"0 9 * * 1-5", "2026-10-30 10:00", "2026-11-02 09:00"}, // Friday to Monday
		{"30 14 31 10 *", "2026-10-30 10:00", "2026-10-31 14:30"},
		{"0 0 1 * *", "2026-12-15 08:00", "2027-01-01 00:00"},
		{"0 12 13 * 5", "2026-10-30 13:00", "2026-11-06 12:00"}, // the 13th or a Friday
		{"0 8 * * 7", "2026-10-30 10:00", "2026-11-01 08:00"},   // 7 is Sunday
		{"15,45 */6 * * *", "2026-10-30 06:20", "2026-10-30 06:45"},
	} {
		schedule, err := Parse(tc.expr)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.expr, err)
		}
		if got := schedule.Next(at(t, tc.from)); !got.Equal(at(t, tc.want)) {
			t.Errorf("%q after %s = %s, want %s", tc.expr, tc.from, got.Format("2006-01-02 15:04"), tc.want)
		}
	}
	never, _ := Parse("0 0 30 2 *")
	if got := never.Next(at(t, "2026-10-30 10:00")); !got.IsZero() {
		t.Fatalf("February 30 runs at %s", got)
	}
}

func TestNormalize(t *testing.T) {
	for spec, want := range map[string]string{
		"5m": "*/5 * * * *", "1m": "* * * * *", "2h": "0 */2 * * *", "1d": "0 0 * * *",
		" 7  *  * * * ": "7 * * * *",
	} {
		if got, err := Normalize(spec); err != nil || got != want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", spec, got, err, want)
		}
	}
	for _, spec := range []string{"", "30s", "60m", "* * *", "61 * * * *", "5-1 * * * *", "*/0 * * * *"} {
		if _, err := Normalize(spec); err == nil {
			t.Errorf("Normalize(%q) succeeded", spec)
		}
	}
}

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

type delivered struct {
	job      Job
	delivery Delivery
}

func newTestScheduler(t *testing.T, now time.Time, deliver func(Job, Delivery) error) (*Scheduler, *clock) {
	t.Helper()
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	c := &clock{now: now}
	s := NewScheduler(st, func(_ context.Context, job Job, delivery Delivery) error { return deliver(job, delivery) })
	s.now = c.Now
	return s, c
}

func TestSchedulerRunsOneShotOnceAndRecurringAgain(t *testing.T) {
	ctx := context.Background()
	var got []delivered
	s, c := newTestScheduler(t, at(t, "2026-10-30 10:00"), func(job Job, d Delivery) error {
		got = append(got, delivered{job, d})
		return nil
	})
	once, err := s.Create(ctx, "s1", "c1", "17 10 30 10 *", "check the deploy", false)
	if err != nil {
		t.Fatal(err)
	}
	every, err := s.Create(ctx, "s1", "c1", "1h", "look at the queue", true)
	if err != nil {
		t.Fatal(err)
	}
	if !once.NextRunAt.Equal(at(t, "2026-10-30 10:17")) {
		t.Fatalf("one-shot next run = %s", once.NextRunAt.Local())
	}
	offset := every.NextRunAt.Sub(at(t, "2026-10-30 11:00"))
	if offset < 0 || offset > 6*time.Minute {
		t.Fatalf("hourly job offset = %s, want within 10%% of an hour", offset)
	}

	c.now = at(t, "2026-10-30 10:20")
	s.tick(ctx)
	if len(got) != 1 || got[0].job.ID != once.ID || got[0].delivery != Due {
		t.Fatalf("deliveries = %+v", got)
	}
	c.now = every.NextRunAt.Add(time.Second)
	s.tick(ctx)
	s.tick(ctx)
	if len(got) != 2 || got[1].job.ID != every.ID {
		t.Fatalf("deliveries = %+v", got)
	}
	jobs, err := s.List(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].ID != every.ID || !jobs[0].NextRunAt.After(c.now) {
		t.Fatalf("jobs after firing = %+v", jobs)
	}
}

func TestSchedulerReportsMissedOneShotAndSkipsMissedRecurring(t *testing.T) {
	ctx := context.Background()
	var got []delivered
	s, c := newTestScheduler(t, at(t, "2026-10-30 10:00"), func(job Job, d Delivery) error {
		got = append(got, delivered{job, d})
		return nil
	})
	once, _ := s.Create(ctx, "s1", "c1", "17 10 30 10 *", "check the deploy", false)
	every, _ := s.Create(ctx, "s1", "c1", "17 * * * *", "look at the queue", true)
	c.now = at(t, "2026-10-30 13:00") // Koder was down for hours
	s.tick(ctx)
	if len(got) != 1 || got[0].job.ID != once.ID || got[0].delivery != Missed {
		t.Fatalf("deliveries = %+v", got)
	}
	jobs, _ := s.List(ctx, "c1")
	if len(jobs) != 1 || jobs[0].ID != every.ID || jobs[0].NextRunAt.Before(at(t, "2026-10-30 13:17")) {
		t.Fatalf("jobs = %+v", jobs)
	}
}

func TestSchedulerDropsJobsOfGoneChatsAndExpiresRecurring(t *testing.T) {
	ctx := context.Background()
	gone := false
	s, c := newTestScheduler(t, at(t, "2026-10-30 10:00"), func(Job, Delivery) error {
		if gone {
			return ErrChatGone
		}
		return nil
	})
	job, _ := s.Create(ctx, "s1", "c1", "1d", "summarize the day", true)
	c.now = job.NextRunAt.Add(RecurringMaxAge)
	// Line the clock up with a run, a week on.
	next, _ := s.nextRun(job, c.now.Add(-24*time.Hour))
	job.NextRunAt = next
	if err := s.jobs.Put(ctx, job); err != nil {
		t.Fatal(err)
	}
	c.now = next
	s.tick(ctx)
	if jobs, _ := s.List(ctx, "c1"); len(jobs) != 0 {
		t.Fatalf("expired job kept: %+v", jobs)
	}

	gone = true
	if _, err := s.Create(ctx, "s1", "c1", "* * * * *", "ping", true); err != nil {
		t.Fatal(err)
	}
	c.now = c.now.Add(2 * time.Minute)
	s.tick(ctx)
	s.tick(ctx)
	if jobs, _ := s.List(ctx, "c1"); len(jobs) != 0 {
		t.Fatalf("job of a gone chat kept: %+v", jobs)
	}
}

func TestSchedulerKeepsChatsApart(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestScheduler(t, at(t, "2026-10-30 10:00"), func(Job, Delivery) error { return nil })
	job, _ := s.Create(ctx, "s1", "c1", "5m", "ping", true)
	if _, err := s.Delete(ctx, "c2", job.ID); err == nil {
		t.Fatal("another chat deleted the job")
	}
	if jobs, _ := s.List(ctx, "c2"); len(jobs) != 0 {
		t.Fatalf("another chat lists %+v", jobs)
	}
	if _, err := s.Delete(ctx, "c1", job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "s1", "c1", "5m", strings.Repeat("x", MaxPromptBytes+1), true); err == nil {
		t.Fatal("oversized prompt accepted")
	}
	for range MaxJobsPerChat {
		if _, err := s.Create(ctx, "s1", "c1", "5m", "ping", true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Create(ctx, "s1", "c1", "5m", "ping", true); err == nil || !strings.Contains(err.Error(), "delete one") {
		t.Fatalf("over the limit: %v", err)
	}
}

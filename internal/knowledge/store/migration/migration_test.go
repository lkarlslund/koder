package migration

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

var migrationTime = time.Date(2026, 8, 22, 16, 0, 0, 0, time.UTC)

type fakeJournal struct {
	version uint32
	state   State
	active  bool
	saves   int
	clears  int
}

func (j *fakeJournal) SchemaVersion(ctx context.Context) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return j.version, nil
}

func (j *fakeJournal) SetSchemaVersion(ctx context.Context, expected, next uint32) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if j.version != expected {
		return fmt.Errorf("version conflict: have %d, expected %d", j.version, expected)
	}
	j.version = next
	return nil
}

func (j *fakeJournal) Load(ctx context.Context) (State, bool, error) {
	if err := ctx.Err(); err != nil {
		return State{}, false, err
	}
	return j.state, j.active, nil
}

func (j *fakeJournal) Save(ctx context.Context, state State) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	j.state = state
	j.active = true
	j.saves++
	return nil
}

func (j *fakeJournal) Clear(ctx context.Context, stepID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !j.active || j.state.StepID != stepID {
		return fmt.Errorf("clear mismatch")
	}
	j.state = State{}
	j.active = false
	j.clears++
	return nil
}

func TestRegistryBuildsOnlyCompleteForwardPaths(t *testing.T) {
	t.Parallel()
	noop := func(context.Context, Progress) error { return nil }
	registry, err := NewRegistry(
		Step{ID: "schema-1-2", FromVersion: 1, ToVersion: 2, Apply: noop},
		Step{ID: "schema-2-3", FromVersion: 2, ToVersion: 3, Apply: noop},
	)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	plan, err := registry.Plan(1, 3)
	if err != nil || len(plan.Steps) != 2 || plan.Steps[0].ID != "schema-1-2" || plan.Steps[1].ID != "schema-2-3" {
		t.Fatalf("Plan() = %#v, %v", plan, err)
	}
	if _, err := registry.Plan(3, 1); !errors.Is(err, ErrNoPath) {
		t.Fatalf("Plan(backward) error = %v, want ErrNoPath", err)
	}

	invalid := [][]Step{
		{{ID: "bad step", FromVersion: 1, ToVersion: 2, Apply: noop}},
		{{ID: "skip", FromVersion: 1, ToVersion: 3, Apply: noop}},
		{{ID: "missing-handler", FromVersion: 1, ToVersion: 2}},
		{{ID: "one", FromVersion: 1, ToVersion: 2, Apply: noop}, {ID: "two", FromVersion: 1, ToVersion: 2, Apply: noop}},
	}
	for _, steps := range invalid {
		if _, err := NewRegistry(steps...); err == nil {
			t.Errorf("NewRegistry(%#v) unexpectedly succeeded", steps)
		}
	}
}

func TestDryRunDoesNotRunHandlersOrWriteJournal(t *testing.T) {
	t.Parallel()
	calls := 0
	registry, _ := NewRegistry(Step{
		ID: "schema-1-2", FromVersion: 1, ToVersion: 2,
		Apply: func(context.Context, Progress) error { calls++; return nil },
	})
	journal := &fakeJournal{version: 1}
	engine, _ := New(registry, journal, func() time.Time { return migrationTime })
	plan, err := engine.DryRun(context.Background(), 2)
	if err != nil || len(plan.Steps) != 1 {
		t.Fatalf("DryRun() = %#v, %v", plan, err)
	}
	if calls != 0 || journal.saves != 0 || journal.version != 1 {
		t.Fatalf("DryRun mutated state: calls=%d journal=%#v", calls, journal)
	}
}

func TestApplyRunsRegisteredStepsAndDurablyAdvancesVersion(t *testing.T) {
	t.Parallel()
	var calls []string
	step := func(name string) Handler {
		return func(_ context.Context, progress Progress) error {
			calls = append(calls, name+":"+progress.Cursor())
			return progress.Checkpoint(context.Background(), "done")
		}
	}
	registry, _ := NewRegistry(
		Step{ID: "schema-1-2", FromVersion: 1, ToVersion: 2, Apply: step("one")},
		Step{ID: "schema-2-3", FromVersion: 2, ToVersion: 3, Apply: step("two")},
	)
	journal := &fakeJournal{version: 1}
	engine, _ := New(registry, journal, func() time.Time { return migrationTime })
	result, err := engine.Apply(context.Background(), 3)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.FromVersion != 1 || result.ToVersion != 3 || len(result.Applied) != 2 || journal.version != 3 || journal.active {
		t.Fatalf("Apply() = %#v, journal=%#v", result, journal)
	}
	if fmt.Sprint(calls) != "[one: two:]" || journal.saves != 4 || journal.clears != 2 {
		t.Fatalf("handler/journal calls = %v, saves=%d clears=%d", calls, journal.saves, journal.clears)
	}
}

func TestFailedApplyResumesFromDurableCursor(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("interrupted")
	calls := 0
	registry, _ := NewRegistry(Step{
		ID: "schema-1-2", FromVersion: 1, ToVersion: 2,
		Apply: func(ctx context.Context, progress Progress) error {
			calls++
			if progress.Cursor() == "" {
				if err := progress.Checkpoint(ctx, "halfway"); err != nil {
					return err
				}
				return wantErr
			}
			if progress.Cursor() != "halfway" {
				return fmt.Errorf("unexpected cursor %q", progress.Cursor())
			}
			return nil
		},
	})
	journal := &fakeJournal{version: 1}
	engine, _ := New(registry, journal, func() time.Time { return migrationTime })
	if _, err := engine.Apply(context.Background(), 2); !errors.Is(err, wantErr) {
		t.Fatalf("Apply() error = %v, want interrupted", err)
	}
	if !journal.active || journal.state.Cursor != "halfway" || journal.version != 1 {
		t.Fatalf("failed apply journal = %#v", journal)
	}
	if _, err := engine.Apply(context.Background(), 2); !errors.Is(err, ErrMigrationActive) {
		t.Fatalf("second Apply() error = %v, want ErrMigrationActive", err)
	}
	result, err := engine.Resume(context.Background())
	if err != nil || !result.Resumed || result.ToVersion != 2 || calls != 2 || journal.active {
		t.Fatalf("Resume() = %#v, %v; calls=%d journal=%#v", result, err, calls, journal)
	}
}

func TestRollbackIsResumableAndLeavesOriginalVersion(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("apply failed")
	rollbackCursor := ""
	registry, _ := NewRegistry(Step{
		ID: "schema-1-2", FromVersion: 1, ToVersion: 2,
		Apply: func(ctx context.Context, progress Progress) error {
			if err := progress.Checkpoint(ctx, "changed-one-batch"); err != nil {
				return err
			}
			return wantErr
		},
		Rollback: func(ctx context.Context, progress Progress) error {
			rollbackCursor = progress.Cursor()
			return progress.Checkpoint(ctx, "rolled-back")
		},
	})
	journal := &fakeJournal{version: 1}
	engine, _ := New(registry, journal, func() time.Time { return migrationTime })
	_, _ = engine.Apply(context.Background(), 2)
	result, err := engine.Rollback(context.Background())
	if err != nil || !result.RolledBack || result.ToVersion != 1 || journal.version != 1 || journal.active {
		t.Fatalf("Rollback() = %#v, %v; journal=%#v", result, err, journal)
	}
	if rollbackCursor != "changed-one-batch" {
		t.Fatalf("rollback cursor = %q", rollbackCursor)
	}
}

func TestResumeFinishesCrashAfterVersionSwitchWithoutRerunningHandler(t *testing.T) {
	t.Parallel()
	calls := 0
	registry, _ := NewRegistry(Step{
		ID: "schema-1-2", FromVersion: 1, ToVersion: 2,
		Apply: func(context.Context, Progress) error { calls++; return nil },
	})
	journal := &fakeJournal{
		version: 2,
		active:  true,
		state: State{
			StepID: "schema-1-2", FromVersion: 1, ToVersion: 2, Phase: PhaseApplying,
			StartedAt: migrationTime, UpdatedAt: migrationTime,
		},
	}
	engine, _ := New(registry, journal, func() time.Time { return migrationTime })
	result, err := engine.Resume(context.Background())
	if err != nil || result.ToVersion != 2 || calls != 0 || journal.active {
		t.Fatalf("Resume() = %#v, %v; calls=%d journal=%#v", result, err, calls, journal)
	}
}

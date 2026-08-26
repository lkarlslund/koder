// Package migration coordinates resumable, rollback-capable store schema migrations.
// Backend adapters own the durable journal and the migration step implementations.
package migration

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"
)

var (
	ErrNoPath          = errors.New("no memory schema migration path")
	ErrMigrationActive = errors.New("memory schema migration already active")
	ErrNoActive        = errors.New("no active memory schema migration")
)

type Phase string

const (
	PhaseApplying    Phase = "applying"
	PhaseRollingBack Phase = "rolling_back"
)

// State is the minimum durable journal needed to resume after process failure. Cursor is
// opaque to the coordinator and interpreted only by the owning step.
type State struct {
	StepID      string    `json:"step_id"`
	FromVersion uint32    `json:"from_version"`
	ToVersion   uint32    `json:"to_version"`
	Phase       Phase     `json:"phase"`
	Cursor      string    `json:"cursor,omitempty"`
	StartedAt   time.Time `json:"started_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Journal atomically owns the current schema version and active migration state. Save,
// SetSchemaVersion, and Clear must be durable before returning.
type Journal interface {
	SchemaVersion(context.Context) (uint32, error)
	SetSchemaVersion(context.Context, uint32, uint32) error
	Load(context.Context) (State, bool, error)
	Save(context.Context, State) error
	Clear(context.Context, string) error
}

// Progress lets an idempotent step durably record a backend-private resume cursor.
type Progress interface {
	Cursor() string
	Checkpoint(context.Context, string) error
}

type Handler func(context.Context, Progress) error

// Step migrates exactly one schema version. Apply must be idempotent from its last saved
// cursor. Rollback is optional only when Apply cannot mutate before its final commit.
type Step struct {
	ID          string
	FromVersion uint32
	ToVersion   uint32
	Apply       Handler
	Rollback    Handler
}

type Plan struct {
	FromVersion uint32 `json:"from_version"`
	ToVersion   uint32 `json:"to_version"`
	Steps       []Step `json:"-"`
}

type Result struct {
	FromVersion uint32   `json:"from_version"`
	ToVersion   uint32   `json:"to_version"`
	Applied     []string `json:"applied,omitempty"`
	Resumed     bool     `json:"resumed,omitempty"`
	RolledBack  bool     `json:"rolled_back,omitempty"`
}

type Registry struct {
	byFrom map[uint32]Step
	byID   map[string]Step
}

var validStepID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

func NewRegistry(steps ...Step) (*Registry, error) {
	registry := &Registry{byFrom: make(map[uint32]Step, len(steps)), byID: make(map[string]Step, len(steps))}
	for _, step := range steps {
		if !validStepID.MatchString(step.ID) || step.FromVersion == 0 || step.ToVersion != step.FromVersion+1 || step.Apply == nil {
			return nil, fmt.Errorf("invalid memory schema migration step %q (%d -> %d)", step.ID, step.FromVersion, step.ToVersion)
		}
		if _, exists := registry.byFrom[step.FromVersion]; exists {
			return nil, fmt.Errorf("duplicate memory schema migration from version %d", step.FromVersion)
		}
		if _, exists := registry.byID[step.ID]; exists {
			return nil, fmt.Errorf("duplicate memory schema migration ID %q", step.ID)
		}
		registry.byFrom[step.FromVersion] = step
		registry.byID[step.ID] = step
	}
	return registry, nil
}

func (r *Registry) Plan(fromVersion, toVersion uint32) (Plan, error) {
	plan := Plan{FromVersion: fromVersion, ToVersion: toVersion}
	if fromVersion == 0 || toVersion < fromVersion {
		return plan, fmt.Errorf("%w: %d -> %d", ErrNoPath, fromVersion, toVersion)
	}
	for version := fromVersion; version < toVersion; version++ {
		step, ok := r.byFrom[version]
		if !ok {
			return plan, fmt.Errorf("%w: missing step from version %d", ErrNoPath, version)
		}
		plan.Steps = append(plan.Steps, step)
	}
	return plan, nil
}

type Engine struct {
	registry *Registry
	journal  Journal
	now      func() time.Time
}

func New(registry *Registry, journal Journal, now func() time.Time) (*Engine, error) {
	if registry == nil || journal == nil {
		return nil, fmt.Errorf("memory migration registry and journal are required")
	}
	if now == nil {
		now = time.Now
	}
	return &Engine{registry: registry, journal: journal, now: now}, nil
}

// DryRun resolves the exact ordered path without executing handlers or writing a journal.
func (e *Engine) DryRun(ctx context.Context, targetVersion uint32) (Plan, error) {
	if err := ctx.Err(); err != nil {
		return Plan{}, err
	}
	current, err := e.journal.SchemaVersion(ctx)
	if err != nil {
		return Plan{}, err
	}
	return e.registry.Plan(current, targetVersion)
}

// Apply runs every required step. A handler failure leaves its journal intact for Resume
// or Rollback; completed step IDs are returned only after the version switch is durable.
func (e *Engine) Apply(ctx context.Context, targetVersion uint32) (Result, error) {
	current, err := e.journal.SchemaVersion(ctx)
	if err != nil {
		return Result{}, err
	}
	result := Result{FromVersion: current, ToVersion: current}
	if _, active, err := e.journal.Load(ctx); err != nil {
		return result, err
	} else if active {
		return result, ErrMigrationActive
	}
	plan, err := e.registry.Plan(current, targetVersion)
	if err != nil {
		return result, err
	}
	for _, step := range plan.Steps {
		if err := e.applyStep(ctx, step, State{}); err != nil {
			return result, err
		}
		result.Applied = append(result.Applied, step.ID)
		result.ToVersion = step.ToVersion
	}
	return result, nil
}

// Resume continues the one durably active apply/rollback operation.
func (e *Engine) Resume(ctx context.Context) (Result, error) {
	state, active, err := e.journal.Load(ctx)
	if err != nil {
		return Result{}, err
	}
	if !active {
		return Result{}, ErrNoActive
	}
	step, err := e.stepForState(state)
	if err != nil {
		return Result{}, err
	}
	current, err := e.journal.SchemaVersion(ctx)
	if err != nil {
		return Result{}, err
	}
	result := Result{FromVersion: state.FromVersion, ToVersion: current, Resumed: true}
	if current == state.ToVersion && state.Phase == PhaseApplying {
		if err := e.journal.Clear(ctx, state.StepID); err != nil {
			return result, err
		}
		result.ToVersion = current
		result.Applied = []string{state.StepID}
		return result, nil
	}
	if current != state.FromVersion {
		return result, fmt.Errorf("migration %s journal/schema mismatch: journal %d -> %d, current %d", state.StepID, state.FromVersion, state.ToVersion, current)
	}
	if state.Phase == PhaseRollingBack {
		if step.Rollback == nil {
			return result, fmt.Errorf("migration %s does not support rollback", step.ID)
		}
		if err := e.runHandler(ctx, step, state, step.Rollback); err != nil {
			return result, err
		}
		if err := e.journal.Clear(ctx, step.ID); err != nil {
			return result, err
		}
		result.RolledBack = true
		return result, nil
	}
	if state.Phase != PhaseApplying {
		return result, fmt.Errorf("migration %s has invalid phase %q", state.StepID, state.Phase)
	}
	if err := e.applyStep(ctx, step, state); err != nil {
		return result, err
	}
	result.ToVersion = step.ToVersion
	result.Applied = []string{step.ID}
	return result, nil
}

// Rollback reverses the active, not-yet-version-switched step. A failed rollback remains
// journaled in rolling_back phase and is itself resumable.
func (e *Engine) Rollback(ctx context.Context) (Result, error) {
	state, active, err := e.journal.Load(ctx)
	if err != nil {
		return Result{}, err
	}
	if !active {
		return Result{}, ErrNoActive
	}
	step, err := e.stepForState(state)
	if err != nil {
		return Result{}, err
	}
	if step.Rollback == nil {
		return Result{}, fmt.Errorf("migration %s does not support rollback", step.ID)
	}
	current, err := e.journal.SchemaVersion(ctx)
	if err != nil {
		return Result{}, err
	}
	if current != step.FromVersion {
		return Result{}, fmt.Errorf("migration %s can no longer roll back at schema version %d", step.ID, current)
	}
	state.Phase = PhaseRollingBack
	state.UpdatedAt = e.timestamp()
	if err := e.journal.Save(ctx, state); err != nil {
		return Result{}, err
	}
	if err := e.runHandler(ctx, step, state, step.Rollback); err != nil {
		return Result{}, err
	}
	if err := e.journal.Clear(ctx, step.ID); err != nil {
		return Result{}, err
	}
	return Result{FromVersion: step.FromVersion, ToVersion: step.FromVersion, RolledBack: true}, nil
}

func (e *Engine) applyStep(ctx context.Context, step Step, resume State) error {
	state := resume
	if state.StepID == "" {
		now := e.timestamp()
		state = State{StepID: step.ID, FromVersion: step.FromVersion, ToVersion: step.ToVersion, Phase: PhaseApplying, StartedAt: now, UpdatedAt: now}
		if err := e.journal.Save(ctx, state); err != nil {
			return err
		}
	}
	if err := e.runHandler(ctx, step, state, step.Apply); err != nil {
		return err
	}
	if err := e.journal.SetSchemaVersion(ctx, step.FromVersion, step.ToVersion); err != nil {
		return err
	}
	return e.journal.Clear(ctx, step.ID)
}

func (e *Engine) runHandler(ctx context.Context, step Step, state State, handler Handler) error {
	progress := &progress{engine: e, state: state}
	if err := handler(ctx, progress); err != nil {
		return fmt.Errorf("run memory migration %s in phase %s: %w", step.ID, state.Phase, err)
	}
	return ctx.Err()
}

func (e *Engine) stepForState(state State) (Step, error) {
	step, ok := e.registry.byID[state.StepID]
	if !ok || step.FromVersion != state.FromVersion || step.ToVersion != state.ToVersion {
		return Step{}, fmt.Errorf("active memory migration %q is not registered with matching versions", state.StepID)
	}
	return step, nil
}

func (e *Engine) timestamp() time.Time { return e.now().UTC().Round(0) }

type progress struct {
	engine *Engine
	state  State
}

func (p *progress) Cursor() string { return p.state.Cursor }

func (p *progress) Checkpoint(ctx context.Context, cursor string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.state.Cursor = cursor
	p.state.UpdatedAt = p.engine.timestamp()
	return p.engine.journal.Save(ctx, p.state)
}

// Package observability records privacy-safe Knowledge operation metrics and correlation
// identifiers. It deliberately has no fields for queries, record titles, bodies, paths,
// model output, or raw errors.
package observability

import (
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/id"
)

type Operation string

const (
	OperationSearch          Operation = "search"
	OperationImportValidate  Operation = "import_validate"
	OperationImportPreview   Operation = "import_preview"
	OperationImportStage     Operation = "import_stage"
	OperationImportActivate  Operation = "import_activate"
	OperationCurationSubmit  Operation = "curation_submit"
	OperationCurationProcess Operation = "curation_process"
)

type Outcome string

const (
	OutcomeSucceeded Outcome = "succeeded"
	OutcomeFailed    Outcome = "failed"
	OutcomeCanceled  Outcome = "canceled"
	OutcomeEmpty     Outcome = "empty"
)

type Observation struct {
	OperationID string    `json:"operation_id"`
	AuditID     string    `json:"audit_id,omitempty"`
	Operation   Operation `json:"operation"`
	Outcome     Outcome   `json:"outcome"`
	StartedAt   time.Time `json:"started_at"`
	DurationMS  int64     `json:"duration_ms"`
	InputCount  uint64    `json:"input_count"`
	OutputCount uint64    `json:"output_count"`
}

type Metric struct {
	Operation       Operation `json:"operation"`
	Started         uint64    `json:"started"`
	Completed       uint64    `json:"completed"`
	Succeeded       uint64    `json:"succeeded"`
	Failed          uint64    `json:"failed"`
	Canceled        uint64    `json:"canceled"`
	Empty           uint64    `json:"empty"`
	TotalDurationMS uint64    `json:"total_duration_ms"`
	LastDurationMS  int64     `json:"last_duration_ms"`
	LastOperationID string    `json:"last_operation_id,omitempty"`
	LastCompletedAt time.Time `json:"last_completed_at,omitzero"`
}

type Snapshot struct {
	Operations []Metric      `json:"operations"`
	Recent     []Observation `json:"recent"`
}

type Config struct {
	Now       func() time.Time
	NewID     func() string
	MaxRecent int
}

type Recorder struct {
	mu        sync.Mutex
	now       func() time.Time
	newID     func() string
	maxRecent int
	metrics   map[Operation]Metric
	recent    []Observation
}

type Span struct {
	recorder    *Recorder
	observation Observation
	once        sync.Once
}

func NewRecorder(config Config) *Recorder {
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.NewID == nil {
		config.NewID = func() string { return string(id.New()) }
	}
	if config.MaxRecent <= 0 {
		config.MaxRecent = 64
	}
	return &Recorder{
		now: config.Now, newID: config.NewID, maxRecent: config.MaxRecent,
		metrics: make(map[Operation]Metric),
	}
}

func (r *Recorder) Start(operation Operation, auditID string) *Span {
	if r == nil || !validOperation(operation) {
		return &Span{}
	}
	operationID := strings.TrimSpace(r.newID())
	if !safeIdentifier(operationID) {
		operationID = string(id.New())
	}
	if !safeIdentifier(auditID) {
		auditID = ""
	}
	startedAt := r.now().UTC().Round(0)
	r.mu.Lock()
	metric := r.metrics[operation]
	metric.Operation = operation
	metric.Started++
	r.metrics[operation] = metric
	r.mu.Unlock()
	return &Span{recorder: r, observation: Observation{
		OperationID: operationID, AuditID: auditID, Operation: operation, StartedAt: startedAt,
	}}
}

func (s *Span) ID() string {
	if s == nil {
		return ""
	}
	return s.observation.OperationID
}

func (s *Span) Finish(outcome Outcome, inputCount, outputCount uint64) {
	if s == nil || s.recorder == nil {
		return
	}
	s.once.Do(func() {
		if !validOutcome(outcome) {
			outcome = OutcomeFailed
		}
		completedAt := s.recorder.now().UTC().Round(0)
		duration := completedAt.Sub(s.observation.StartedAt)
		if duration < 0 {
			duration = 0
		}
		s.observation.Outcome = outcome
		s.observation.DurationMS = duration.Milliseconds()
		s.observation.InputCount = inputCount
		s.observation.OutputCount = outputCount
		r := s.recorder
		r.mu.Lock()
		metric := r.metrics[s.observation.Operation]
		metric.Operation = s.observation.Operation
		metric.Completed++
		switch outcome {
		case OutcomeSucceeded:
			metric.Succeeded++
		case OutcomeFailed:
			metric.Failed++
		case OutcomeCanceled:
			metric.Canceled++
		case OutcomeEmpty:
			metric.Empty++
		}
		metric.TotalDurationMS += uint64(s.observation.DurationMS)
		metric.LastDurationMS = s.observation.DurationMS
		metric.LastOperationID = s.observation.OperationID
		metric.LastCompletedAt = completedAt
		r.metrics[s.observation.Operation] = metric
		r.recent = append(r.recent, s.observation)
		if len(r.recent) > r.maxRecent {
			r.recent = slices.Clone(r.recent[len(r.recent)-r.maxRecent:])
		}
		r.mu.Unlock()
		slog.Debug("knowledge operation", "operation_id", s.observation.OperationID, "audit_id", s.observation.AuditID,
			"operation", s.observation.Operation, "outcome", outcome, "duration_ms", s.observation.DurationMS,
			"input_count", inputCount, "output_count", outputCount)
	})
}

func validOperation(value Operation) bool {
	switch value {
	case OperationSearch, OperationImportValidate, OperationImportPreview, OperationImportStage,
		OperationImportActivate, OperationCurationSubmit, OperationCurationProcess:
		return true
	default:
		return false
	}
}

func (r *Recorder) Snapshot() Snapshot {
	if r == nil {
		return Snapshot{Operations: []Metric{}, Recent: []Observation{}}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	result := Snapshot{Operations: make([]Metric, 0, len(r.metrics)), Recent: slices.Clone(r.recent)}
	for _, metric := range r.metrics {
		result.Operations = append(result.Operations, metric)
	}
	slices.SortFunc(result.Operations, func(left, right Metric) int {
		return strings.Compare(string(left.Operation), string(right.Operation))
	})
	return result
}

func validOutcome(value Outcome) bool {
	switch value {
	case OutcomeSucceeded, OutcomeFailed, OutcomeCanceled, OutcomeEmpty:
		return true
	default:
		return false
	}
}

func safeIdentifier(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' ||
			char == '-' || char == '_' || char == '.' || char == ':' {
			continue
		}
		return false
	}
	return true
}

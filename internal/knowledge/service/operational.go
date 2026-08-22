package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

type OperationalAction string

const (
	OperationalStatusRead    OperationalAction = "status_read"
	OperationalIntegrityScan OperationalAction = "integrity_scan"
	OperationalIndexRebuild  OperationalAction = "index_rebuild"
)

var ErrOperationalPolicyDenied = errors.New("knowledge operational policy denied operation")

type OperationalPolicy interface {
	AuthorizeKnowledgeOperation(context.Context, knowledge.Actor, OperationalAction) error
}

type OperationalPolicyFunc func(context.Context, knowledge.Actor, OperationalAction) error

func (fn OperationalPolicyFunc) AuthorizeKnowledgeOperation(ctx context.Context, actor knowledge.Actor, action OperationalAction) error {
	return fn(ctx, actor, action)
}

type AllowAllOperationalPolicy struct{}

func (AllowAllOperationalPolicy) AuthorizeKnowledgeOperation(context.Context, knowledge.Actor, OperationalAction) error {
	return nil
}

type OperationalStoreStatus struct {
	Backend         string `json:"backend"`
	Open            bool   `json:"open"`
	ReadOnly        bool   `json:"read_only"`
	SchemaVersion   uint32 `json:"schema_version"`
	IndexGeneration uint64 `json:"index_generation"`
	LastError       string `json:"last_error,omitempty"`
}

type OperationalStatus struct {
	Store                OperationalStoreStatus             `json:"store"`
	MaintenanceAvailable bool                               `json:"maintenance_available"`
	LexicalIndex         *knowledgeStore.IndexRebuildStatus `json:"lexical_index,omitempty"`
	SemanticIndex        *SemanticIndexStatus               `json:"semantic_index,omitempty"`
	MutationCheckpoint   MutationCheckpoint                 `json:"mutation_checkpoint"`
}

type StartIndexRebuildResult struct {
	Accepted bool                              `json:"accepted"`
	Status   knowledgeStore.IndexRebuildStatus `json:"status"`
}

func (s *Service) OperationalStatus(ctx context.Context) (OperationalStatus, error) {
	if err := ctx.Err(); err != nil {
		return OperationalStatus{}, err
	}
	if err := s.authorizeOperational(ctx, OperationalStatusRead); err != nil {
		return OperationalStatus{}, err
	}
	health, err := s.store.Health(ctx)
	if err != nil {
		return OperationalStatus{}, fmt.Errorf("read knowledge store health: %w", err)
	}
	result := OperationalStatus{
		Store: OperationalStoreStatus{
			Backend: health.Backend, Open: health.Open, ReadOnly: health.ReadOnly,
			SchemaVersion: health.SchemaVersion, IndexGeneration: health.IndexGeneration,
			LastError: health.LastError,
		},
		MutationCheckpoint: s.MutationCheckpoint(),
	}
	if maintenance, ok := s.store.(knowledgeStore.MaintenanceStore); ok {
		status, err := maintenance.IndexRebuildStatus(ctx)
		if err != nil {
			return OperationalStatus{}, fmt.Errorf("read knowledge index status: %w", err)
		}
		s.operationalMu.Lock()
		if s.rebuildRunning && !status.Running {
			status.Running = true
			status.ActiveGeneration = health.IndexGeneration
			if health.IndexGeneration < math.MaxUint64 {
				status.TargetGeneration = health.IndexGeneration + 1
			}
			status.StartedAt = s.rebuildStartedAt
		}
		s.operationalMu.Unlock()
		result.MaintenanceAvailable = true
		result.LexicalIndex = &status
	}
	if s.semantic != nil {
		status, err := s.semantic.Status(ctx)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return OperationalStatus{}, ctxErr
			}
			status.Available = false
			status.LastError = "semantic index status unavailable"
		}
		result.SemanticIndex = &status
	}
	return result, nil
}

func (s *Service) StartIndexRebuild(ctx context.Context) (StartIndexRebuildResult, error) {
	if err := ctx.Err(); err != nil {
		return StartIndexRebuildResult{}, err
	}
	if err := s.authorizeOperational(ctx, OperationalIndexRebuild); err != nil {
		return StartIndexRebuildResult{}, err
	}
	maintenance, ok := s.store.(knowledgeStore.MaintenanceStore)
	if !ok {
		return StartIndexRebuildResult{}, knowledgeStore.ErrUnsupported
	}
	health, err := s.store.Health(ctx)
	if err != nil {
		return StartIndexRebuildResult{}, fmt.Errorf("read knowledge store health: %w", err)
	}
	if !health.Open {
		return StartIndexRebuildResult{}, knowledgeStore.ErrClosed
	}
	if health.ReadOnly {
		return StartIndexRebuildResult{}, knowledgeStore.ErrReadOnly
	}
	if health.IndexGeneration == math.MaxUint64 {
		return StartIndexRebuildResult{}, knowledgeStore.ErrIncompatible
	}
	status, err := maintenance.IndexRebuildStatus(ctx)
	if err != nil {
		return StartIndexRebuildResult{}, fmt.Errorf("read knowledge index status: %w", err)
	}
	s.operationalMu.Lock()
	if s.operationsClosed {
		s.operationalMu.Unlock()
		return StartIndexRebuildResult{}, knowledgeStore.ErrClosed
	}
	if s.rebuildRunning || status.Running {
		s.operationalMu.Unlock()
		return StartIndexRebuildResult{}, fmt.Errorf("%w: knowledge index rebuild already running", knowledgeStore.ErrConflict)
	}
	s.rebuildRunning = true
	s.rebuildStartedAt = s.now().UTC().Round(0)
	startedAt := s.rebuildStartedAt
	s.operationsWG.Add(1)
	s.operationalMu.Unlock()

	status.Running = true
	status.ActiveGeneration = health.IndexGeneration
	status.StartedAt = startedAt
	status.TargetGeneration = health.IndexGeneration + 1
	go s.runIndexRebuild(maintenance)
	return StartIndexRebuildResult{Accepted: true, Status: status}, nil
}

func (s *Service) runIndexRebuild(maintenance knowledgeStore.MaintenanceStore) {
	defer s.operationsWG.Done()
	_ = maintenance.RebuildIndexes(s.operationsCtx)
	s.operationalMu.Lock()
	s.rebuildRunning = false
	s.rebuildStartedAt = time.Time{}
	s.operationalMu.Unlock()
}

// ShutdownOperations cancels and joins background maintenance without closing the
// caller-owned store. It is safe to call more than once.
func (s *Service) ShutdownOperations(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.operationalMu.Lock()
	if !s.operationsClosed {
		s.operationsClosed = true
		s.operationsCancel()
	}
	s.operationalMu.Unlock()
	done := make(chan struct{})
	go func() {
		s.operationsWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) authorizeOperational(ctx context.Context, action OperationalAction) error {
	actor, err := s.actor(ctx)
	if err != nil {
		return fmt.Errorf("resolve knowledge actor: %w", err)
	}
	if err := actor.Validate(); err != nil {
		return err
	}
	if err := s.operational.AuthorizeKnowledgeOperation(ctx, actor, action); err != nil {
		return fmt.Errorf("%w: action %s: %w", ErrOperationalPolicyDenied, action, err)
	}
	return nil
}

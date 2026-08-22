package service

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/lkarlslund/koder/internal/knowledge"
)

type ChunkPolicyAction string

const (
	ChunkPolicyLinkCreate  ChunkPolicyAction = "link_create"
	ChunkPolicyLinkUnlink  ChunkPolicyAction = "link_unlink"
	ChunkPolicyLinkRestore ChunkPolicyAction = "link_restore"
	ChunkPolicyTraverse    ChunkPolicyAction = "traverse"
)

var (
	ErrChunkPolicyDenied       = errors.New("knowledge chunk policy denied operation")
	ErrLinkEndpointUnavailable = errors.New("knowledge link endpoint chunk is unavailable")
)

// ChunkPolicy is the authorization boundary for operations whose effects span chunks.
// Koder's local single-user mode permits them; multi-user/profile adapters can apply
// visibility, sharing, principal, and driver policy without moving it into persistence.
type ChunkPolicy interface {
	AuthorizeChunk(context.Context, knowledge.Actor, ChunkPolicyAction, knowledge.Chunk) error
}

type ChunkPolicyFunc func(context.Context, knowledge.Actor, ChunkPolicyAction, knowledge.Chunk) error

func (fn ChunkPolicyFunc) AuthorizeChunk(ctx context.Context, actor knowledge.Actor, action ChunkPolicyAction, chunk knowledge.Chunk) error {
	return fn(ctx, actor, action, chunk)
}

type AllowAllChunkPolicy struct{}

func (AllowAllChunkPolicy) AuthorizeChunk(context.Context, knowledge.Actor, ChunkPolicyAction, knowledge.Chunk) error {
	return nil
}

func (s *Service) authorizeLinkChunks(ctx context.Context, actor knowledge.Actor, action ChunkPolicyAction, requireActive bool, chunks ...knowledge.Chunk) error {
	unique := make(map[knowledge.ChunkID]knowledge.Chunk, len(chunks))
	for _, chunk := range chunks {
		unique[chunk.ID] = chunk
	}
	ids := make([]knowledge.ChunkID, 0, len(unique))
	for id := range unique {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	var failures []error
	for _, id := range ids {
		chunk := unique[id]
		if requireActive && chunk.State != knowledge.ChunkStateActive {
			failures = append(failures, fmt.Errorf("%w: chunk %s is %q", ErrLinkEndpointUnavailable, chunk.ID, chunk.State))
		}
		if err := s.chunkPolicy.AuthorizeChunk(ctx, actor, action, chunk); err != nil {
			failures = append(failures, fmt.Errorf("%w: action %s on chunk %s: %w", ErrChunkPolicyDenied, action, chunk.ID, err))
		}
	}
	return errors.Join(failures...)
}

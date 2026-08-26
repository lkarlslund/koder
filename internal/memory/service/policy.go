package service

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

type ChunkPolicyAction string

const (
	ChunkPolicyCreate         ChunkPolicyAction = "create"
	ChunkPolicyUpdate         ChunkPolicyAction = "update"
	ChunkPolicyArchive        ChunkPolicyAction = "archive"
	ChunkPolicyRestore        ChunkPolicyAction = "restore"
	ChunkPolicyDelete         ChunkPolicyAction = "delete"
	ChunkPolicyCascadeDelete  ChunkPolicyAction = "cascade_delete"
	ChunkPolicyLinkCreate     ChunkPolicyAction = "link_create"
	ChunkPolicyLinkUnlink     ChunkPolicyAction = "link_unlink"
	ChunkPolicyLinkRestore    ChunkPolicyAction = "link_restore"
	ChunkPolicyEntryCreate    ChunkPolicyAction = "entry_create"
	ChunkPolicyEntryUpdate    ChunkPolicyAction = "entry_update"
	ChunkPolicyEntryArchive   ChunkPolicyAction = "entry_archive"
	ChunkPolicyEntryRestore   ChunkPolicyAction = "entry_restore"
	ChunkPolicyEntryDelete    ChunkPolicyAction = "entry_delete"
	ChunkPolicyEntryVerify    ChunkPolicyAction = "entry_verify"
	ChunkPolicyEntrySupersede ChunkPolicyAction = "entry_supersede"
	ChunkPolicyRead           ChunkPolicyAction = "read"
	ChunkPolicyTraverse       ChunkPolicyAction = "traverse"
	ChunkPolicySearch         ChunkPolicyAction = "search"
)

var (
	ErrChunkPolicyDenied       = errors.New("memory chunk policy denied operation")
	ErrLinkEndpointUnavailable = errors.New("memory link endpoint chunk is unavailable")
)

// ChunkPolicy is the authorization boundary for operations whose effects span chunks.
// Koder's local single-user mode permits them; multi-user/profile adapters can apply
// visibility, sharing, principal, and driver policy without moving it into persistence.
type ChunkPolicy interface {
	AuthorizeChunk(context.Context, memory.Actor, ChunkPolicyAction, memory.Chunk) error
}

type ChunkPolicyFunc func(context.Context, memory.Actor, ChunkPolicyAction, memory.Chunk) error

func (fn ChunkPolicyFunc) AuthorizeChunk(ctx context.Context, actor memory.Actor, action ChunkPolicyAction, chunk memory.Chunk) error {
	return fn(ctx, actor, action, chunk)
}

type AllowAllChunkPolicy struct{}

func (AllowAllChunkPolicy) AuthorizeChunk(context.Context, memory.Actor, ChunkPolicyAction, memory.Chunk) error {
	return nil
}

func (s *Service) authorizeChunk(ctx context.Context, actor memory.Actor, action ChunkPolicyAction, chunk memory.Chunk) error {
	if err := s.chunkPolicy.AuthorizeChunk(ctx, actor, action, chunk); err != nil {
		return fmt.Errorf("%w: action %s on chunk %s: %w", ErrChunkPolicyDenied, action, chunk.ID, err)
	}
	return nil
}

func (s *Service) authorizeEntryChunk(ctx context.Context, tx memoryStoreAPI.ReadTx, actor memory.Actor, action ChunkPolicyAction, chunkID memory.ChunkID) (memory.Chunk, error) {
	chunk, err := tx.Chunk(ctx, chunkID)
	if err != nil {
		return memory.Chunk{}, err
	}
	if err := s.authorizeChunk(ctx, actor, action, chunk); err != nil {
		return memory.Chunk{}, err
	}
	return chunk, nil
}

func (s *Service) authorizeLinkChunks(ctx context.Context, actor memory.Actor, action ChunkPolicyAction, requireActive bool, chunks ...memory.Chunk) error {
	unique := make(map[memory.ChunkID]memory.Chunk, len(chunks))
	for _, chunk := range chunks {
		unique[chunk.ID] = chunk
	}
	ids := make([]memory.ChunkID, 0, len(unique))
	for id := range unique {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	var failures []error
	for _, id := range ids {
		chunk := unique[id]
		if requireActive && chunk.State != memory.ChunkStateActive {
			failures = append(failures, fmt.Errorf("%w: chunk %s is %q", ErrLinkEndpointUnavailable, chunk.ID, chunk.State))
		}
		if err := s.authorizeChunk(ctx, actor, action, chunk); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

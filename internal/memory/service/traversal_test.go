package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
	memoryBackend "github.com/lkarlslund/koder/internal/memory/store/memory"
)

func TestTraverseBreadthFirstDetectsCycles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	chunks := make([]memory.Chunk, 0, 3)
	for _, title := range []string{"A", "B", "C"} {
		candidate := testChunkCandidate()
		candidate.Title = title
		created, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: candidate})
		if err != nil {
			t.Fatalf("CreateChunk(%s): %v", title, err)
		}
		chunks = append(chunks, created.Chunk)
	}
	refs := make([]memory.ObjectRef, len(chunks))
	for index, chunk := range chunks {
		refs[index] = memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(chunk.ID)}
	}
	for index := range refs {
		if _, err := service.CreateLink(ctx, CreateLinkRequest{Link: memory.Link{
			Source: refs[index], Target: refs[(index+1)%len(refs)], Kind: memory.LinkKindRequires,
		}}); err != nil {
			t.Fatalf("CreateLink(%d): %v", index, err)
		}
	}
	result, err := service.Traverse(ctx, TraversalRequest{
		Root: refs[0], Direction: memoryStoreAPI.LinkDirectionOutgoing, MaxDepth: 4,
	})
	if err != nil {
		t.Fatalf("Traverse(): %v", err)
	}
	if len(result.Nodes) != 3 || len(result.Edges) != 3 || result.Truncated {
		t.Fatalf("cycle traversal = %#v", result)
	}
	if result.Nodes[0].Depth != 0 || result.Nodes[1].Depth != 1 || result.Nodes[2].Depth != 2 {
		t.Fatalf("breadth-first depths = %#v", result.Nodes)
	}
}

func FuzzTraverseCyclesStayFinite(f *testing.F) {
	f.Add([]byte{1, 2, 0})
	f.Add([]byte{0})
	f.Fuzz(func(t *testing.T, edges []byte) {
		if len(edges) == 0 {
			return
		}
		if len(edges) > 8 {
			edges = edges[:8]
		}
		ctx := context.Background()
		store := memoryBackend.New()
		defer func() { _ = store.Close() }()
		nextID := uint64(1)
		service, err := New(Config{
			Store: store,
			Actor: func(context.Context) (memory.Actor, error) {
				return memory.Actor{Kind: memory.ActorKindUser, ID: "user:fuzz"}, nil
			},
			Now: func() time.Time { return serviceTime },
			NewID: func() string {
				value := fmt.Sprintf("01a02b00-0000-7000-8000-%012x", nextID)
				nextID++
				return value
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		refs := make([]memory.ObjectRef, len(edges))
		for index := range refs {
			candidate := testChunkCandidate()
			candidate.Title = string(rune('A' + index))
			created, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: candidate})
			if err != nil {
				t.Fatal(err)
			}
			refs[index] = memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(created.Chunk.ID)}
		}
		for source, destination := range edges {
			_, _ = service.CreateLink(ctx, CreateLinkRequest{Link: memory.Link{
				Source: refs[source], Target: refs[int(destination)%len(refs)], Kind: memory.LinkKindRequires,
			}})
		}
		result, err := service.Traverse(ctx, TraversalRequest{
			Root: refs[0], Direction: memoryStoreAPI.LinkDirectionBoth, MaxDepth: 8, MaxNodes: len(refs), MaxEdges: len(edges),
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Nodes) > len(refs) || len(result.Edges) > len(edges) {
			t.Fatalf("cyclic traversal exceeded bounds: %d nodes, %d edges", len(result.Nodes), len(result.Edges))
		}
		seenNodes := make(map[memory.ObjectRef]struct{}, len(result.Nodes))
		for _, node := range result.Nodes {
			ref := node.Object.ObjectRef()
			if _, duplicate := seenNodes[ref]; duplicate {
				t.Fatalf("cyclic traversal returned duplicate node %v", ref)
			}
			seenNodes[ref] = struct{}{}
		}
		seenEdges := make(map[memory.LinkID]struct{}, len(result.Edges))
		for _, edge := range result.Edges {
			if _, duplicate := seenEdges[edge.ID]; duplicate {
				t.Fatalf("cyclic traversal returned duplicate edge %s", edge.ID)
			}
			seenEdges[edge.ID] = struct{}{}
		}
	})
}

func TestTraverseEnforcesNodeAndDepthLimits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	chunks := make([]memory.Chunk, 0, 3)
	for _, title := range []string{"Root", "One", "Two"} {
		candidate := testChunkCandidate()
		candidate.Title = title
		created, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: candidate})
		chunks = append(chunks, created.Chunk)
	}
	root := memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(chunks[0].ID)}
	for _, chunk := range chunks[1:] {
		_, _ = service.CreateLink(ctx, CreateLinkRequest{Link: memory.Link{
			Source: root, Target: memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(chunk.ID)},
			Kind: memory.LinkKindRelatedTo,
		}})
	}
	result, err := service.Traverse(ctx, TraversalRequest{Root: root, MaxDepth: 1, MaxNodes: 2, MaxEdges: 10})
	if err != nil {
		t.Fatalf("Traverse(): %v", err)
	}
	if len(result.Nodes) != 2 || !result.Truncated || !containsReason(result.TruncationReasons, "node_limit") {
		t.Fatalf("node-limited traversal = %#v", result)
	}
}

func TestTraverseReportsOnlyActualDepthTruncation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	refs := make([]memory.ObjectRef, 0, 3)
	for _, title := range []string{"Root", "Middle", "Leaf"} {
		candidate := testChunkCandidate()
		candidate.Title = title
		created, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: candidate})
		refs = append(refs, memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(created.Chunk.ID)})
	}
	for index := 0; index < 2; index++ {
		_, _ = service.CreateLink(ctx, CreateLinkRequest{Link: memory.Link{
			Source: refs[index], Target: refs[index+1], Kind: memory.LinkKindRelatedTo,
		}})
	}
	limited, err := service.Traverse(ctx, TraversalRequest{
		Root: refs[0], Direction: memoryStoreAPI.LinkDirectionOutgoing, MaxDepth: 1,
	})
	if err != nil || !limited.Truncated || !containsReason(limited.TruncationReasons, "depth_limit") || len(limited.Nodes) != 2 {
		t.Fatalf("depth-limited traversal = %#v, %v", limited, err)
	}
	complete, err := service.Traverse(ctx, TraversalRequest{
		Root: refs[0], Direction: memoryStoreAPI.LinkDirectionOutgoing, MaxDepth: 2,
	})
	if err != nil || complete.Truncated || len(complete.Nodes) != 3 {
		t.Fatalf("complete traversal = %#v, %v", complete, err)
	}
}

func TestTraverseReturnsPartialResultAtInternalTimeLimit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	chunk, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	root := memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(chunk.Chunk.ID)}
	calls := 0
	service.chunkPolicy = ChunkPolicyFunc(func(ctx context.Context, _ memory.Actor, action ChunkPolicyAction, _ memory.Chunk) error {
		if action != ChunkPolicyTraverse {
			return nil
		}
		calls++
		if calls == 1 {
			return nil
		}
		<-ctx.Done()
		return ctx.Err()
	})
	result, err := service.Traverse(ctx, TraversalRequest{Root: root, TimeLimit: time.Millisecond})
	if err != nil || !result.Truncated || !containsReason(result.TruncationReasons, "time_limit") || len(result.Nodes) != 1 {
		t.Fatalf("time-limited Traverse() = %#v, %v", result, err)
	}
}

func containsReason(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

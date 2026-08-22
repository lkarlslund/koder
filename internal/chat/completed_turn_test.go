package chat

import (
	"sync"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
)

type completedTurnRecorder struct {
	mu    sync.Mutex
	turns []CompletedTurn
	done  chan struct{}
}

func (r *completedTurnRecorder) ObserveCompletedTurn(turn CompletedTurn) {
	r.mu.Lock()
	r.turns = append(r.turns, turn)
	r.mu.Unlock()
	select {
	case r.done <- struct{}{}:
	default:
	}
}

func TestLatestCompletedTurnUsesFinalSealedAssistantBoundary(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	timeline := []domain.TimelineItem{
		{ID: "user-1", Content: domain.UserMessage{Text: "fix it"}, SealedAt: now},
		{ID: "assistant-tool", Content: domain.AssistantMessage{Tools: []domain.ToolCall{{Tool: domain.ToolKindExecCommand}}}, SealedAt: now},
		{ID: "assistant-final", Content: domain.AssistantMessage{Text: "Done"}, SealedAt: now},
	}
	turn, ok := latestCompletedTurn(domain.Session{ID: "session-1"}, domain.Chat{ID: "chat-1"}, timeline)
	if !ok || turn.User.ID != "user-1" || turn.Assistant.ID != "assistant-final" || len(turn.Items) != 3 {
		t.Fatalf("latestCompletedTurn() = %#v, %v", turn, ok)
	}
	timeline[2].SealedAt = time.Time{}
	turn, ok = latestCompletedTurn(domain.Session{}, domain.Chat{}, timeline)
	if !ok || turn.Assistant.ID != "assistant-tool" {
		t.Fatalf("unsealed final boundary = %#v, %v", turn, ok)
	}
}

func TestCompletedTurnObserverRunsAfterSuccessfulTurnIsSealed(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	runner := &runtimeFakeRunner{response: &ModelResponse{Text: "durable result"}}
	runtime := newTestChat(t, st, session, chatRecord, runner)
	recorder := &completedTurnRecorder{done: make(chan struct{}, 1)}
	runtime.deps.Turns = recorder

	runtime.Enqueue(QueueItem{Kind: QueueKindUser, Source: domain.UserMessageSourceUser, Text: "remember this"})
	select {
	case <-recorder.done:
	case <-time.After(2 * time.Second):
		t.Fatal("completed-turn observer was not called")
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.turns) != 1 {
		t.Fatalf("observed turns = %d", len(recorder.turns))
	}
	turn := recorder.turns[0]
	if !turn.User.Sealed() || !turn.Assistant.Sealed() {
		t.Fatalf("observer received unsealed boundary: %#v", turn)
	}
	if message, ok := turn.Assistant.Content.(domain.AssistantMessage); !ok || message.Text != "durable result" {
		t.Fatalf("assistant boundary = %#v", turn.Assistant)
	}
}

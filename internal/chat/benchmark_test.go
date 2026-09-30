package chat

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
)

// newBenchmarkChat returns a chat whose history holds turns user/assistant
// pairs, each assistant message with one completed tool call, and a live
// subscriber draining updates like a connected web client.
func newBenchmarkChat(b *testing.B, turns int) (*Chat, domain.TimelineItem) {
	b.Helper()
	ctx := context.Background()
	st := openTestStore(b)
	session, chatRecord, _ := createSessionWithPlan(b, st)
	rt := newTestChat(b, st, session, chatRecord, &runtimeFakeRunner{})
	var last domain.TimelineItem
	for turn := range turns {
		if _, err := rt.AppendUserMessageForInput(ctx, domain.QueuedInput{}, domain.UserMessage{Text: fmt.Sprintf("request %d: %s", turn, strings.Repeat("context ", 40))}); err != nil {
			b.Fatal(err)
		}
		callID := fmt.Sprintf("call-%d", turn)
		item, err := rt.AppendAssistantToolCalls(ctx, domain.TimelineItem{}, []domain.ToolCall{
			{ToolCallID: domain.ToolCallID(callID), Tool: domain.ToolKindFileRead, Args: map[string]string{"path": "README.md"}, Status: domain.ToolStatusPending},
		}, strings.Repeat("reasoning about the change ", 20), domain.ReasoningContent{}, domain.Usage{}, domain.ModelPerformance{})
		if err != nil {
			b.Fatal(err)
		}
		if _, err := rt.MarkToolRunning(ctx, callID); err != nil {
			b.Fatal(err)
		}
		if last, err = rt.AttachToolResult(ctx, callID, domain.ToolResult{Status: domain.ToolResultStatusOK, Text: strings.Repeat("file line\n", 60)}); err != nil {
			b.Fatal(err)
		}
		_ = item
	}
	updates, unsubscribe := rt.Subscribe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range updates {
		}
	}()
	b.Cleanup(func() {
		unsubscribe()
		<-done
	})
	return rt, last
}

// BenchmarkStreamDelta measures handling one streamed text token, which
// runs for every token of every model response.
func BenchmarkStreamDelta(b *testing.B) {
	for _, turns := range []int{10, 100, 400} {
		b.Run(fmt.Sprintf("history=%d", turns), func(b *testing.B) {
			rt, item := newBenchmarkChat(b, turns)
			event := domain.Event{Kind: domain.EventKindMessageDelta, Text: "token ", Item: item}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				rt.handleStreamEventForTurn(0, event)
			}
		})
	}
}

func TestStreamedTextAppendsAndReseeds(t *testing.T) {
	var text streamedText
	got := text.append("", "hel")
	got = text.append(got, "lo")
	if got != "hello" {
		t.Fatalf("append = %q", got)
	}
	// Another path replaced the text; the builder must not keep stale bytes.
	if got = text.append("replaced", "!"); got != "replaced!" {
		t.Fatalf("append after replacement = %q", got)
	}
	earlier := got
	for range 100 {
		got = text.append(got, "x")
	}
	if earlier != "replaced!" {
		t.Fatalf("earlier result changed to %q", earlier)
	}
}

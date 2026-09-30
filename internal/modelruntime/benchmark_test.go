package modelruntime

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
)

// benchmarkTimeline returns turns user/assistant pairs; every assistant
// message carries one completed tool call with a sizeable result.
func benchmarkTimeline(turns int) []domain.TimelineItem {
	now := time.Now().UTC()
	items := make([]domain.TimelineItem, 0, turns*2)
	for turn := range turns {
		items = append(items, domain.TimelineItem{
			ID: id.ID(fmt.Sprintf("user-%d", turn)), ChatID: "chat-1", Seq: int64(len(items) + 1), CreatedAt: now,
			Content: domain.UserMessage{Text: fmt.Sprintf("request %d: %s", turn, strings.Repeat("context ", 40))},
		})
		item := assistantToolItem(fmt.Sprintf("assistant-%d", turn), fmt.Sprintf("call-%d", turn), "cat README.md", strings.Repeat("file line\n", 60))
		item.Seq = int64(len(items) + 1)
		items = append(items, item)
	}
	return items
}

// BenchmarkBuildPromptEnvelope measures rendering a chat history into a
// provider prompt, which runs on every model step.
func BenchmarkBuildPromptEnvelope(b *testing.B) {
	runtime := New(Config{Config: testConfig(b)})
	session := domain.Session{ID: "session-1", ProjectRoot: b.TempDir()}
	chat := domain.Chat{ID: "chat-1", SessionID: "session-1"}
	for _, turns := range []int{10, 100, 400} {
		b.Run(fmt.Sprintf("history=%d", turns), func(b *testing.B) {
			timeline := benchmarkTimeline(turns)
			b.ReportAllocs()
			for b.Loop() {
				if _, err := runtime.BuildPromptEnvelopeForTimeline(session, chat, timeline, "", nil, nil, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

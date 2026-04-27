package tui

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/ui"
	"github.com/lkarlslund/koder/internal/workspace"
)

func benchmarkConfig(b *testing.B) config.Config {
	b.Helper()
	return config.Default().WithStateDir(b.TempDir())
}

func benchmarkModel(b *testing.B, messageCount int) Model {
	b.Helper()
	cfg := benchmarkConfig(b)
	cfg.DefaultProvider = "benchmark"
	cfg.DefaultModel = "model"
	cfg.Providers = map[string]config.Provider{
		"benchmark": {
			Kind:         "openai-compatible",
			AuthMethod:   "api_key",
			BaseURL:      "https://example.invalid/v1",
			APIKey:       "secret",
			DefaultModel: "model",
		},
	}
	m, err := NewWithWorkdir(cfg, nil, nil, StartupModeNew, nil, b.TempDir(), StartupOptions{})
	if err != nil {
		b.Fatalf("new model: %v", err)
	}
	m.width = 120
	m.height = 42
	m.currentSession = domain.Session{
		ID:         42,
		Title:      "Benchmark Session",
		ProviderID: "benchmark",
		ModelID:    "model",
	}
	m.workspace = workspaceStub()
	m.messages = make([]domain.Message, 0, messageCount)
	m.parts = make(map[int64][]domain.Part, messageCount)
	now := time.Now()
	for i := 0; i < messageCount; i++ {
		role := domain.MessageRoleAssistant
		if i%2 == 0 {
			role = domain.MessageRoleUser
		}
		msg := domain.Message{
			ID:        int64(i + 1),
			SessionID: m.currentSession.ID,
			Role:      role,
			CreatedAt: now.Add(time.Duration(i) * time.Second),
		}
		m.messages = append(m.messages, msg)
		body := fmt.Sprintf("message %03d %s", i, strings.Repeat("content ", 10))
		m.parts[msg.ID] = []domain.Part{{
			ID:        int64(i + 1),
			MessageID: msg.ID,
			Kind:      domain.PartKindText,
			Body:      body,
			CreatedAt: msg.CreatedAt,
		}}
		if role == domain.MessageRoleAssistant && i%5 == 1 {
			m.parts[msg.ID] = append(m.parts[msg.ID], domain.Part{
				ID:        int64(1000 + i),
				MessageID: msg.ID,
				Kind:      domain.PartKindReasoning,
				Body:      strings.Repeat("reasoning ", 12),
				CreatedAt: msg.CreatedAt,
			})
		}
	}
	m.resize()
	m.refreshViewport()
	return m
}

func workspaceStub() workspace.Status {
	return workspace.Status{
		Available:   true,
		ProjectRoot: "/tmp/project",
		Branch:      "main",
		Added:       3,
		Modified:    7,
		Deleted:     1,
		Untracked:   4,
	}
}

func BenchmarkModelViewLargeTranscript(b *testing.B) {
	m := benchmarkModel(b, 140)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = m.View()
	}
}

func BenchmarkRefreshViewportLargeTranscript(b *testing.B) {
	m := benchmarkModel(b, 140)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m.refreshViewportPreserve()
	}
}

func BenchmarkApplyEventStreamLargeTranscript(b *testing.B) {
	textChunks := benchmarkNonsenseChunks(32, 24, 1)
	reasoningChunks := benchmarkNonsenseChunks(24, 20, 2)

	b.Run("message_delta", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			m := benchmarkModel(b, 220)
			b.StartTimer()
			for _, chunk := range textChunks {
				m.applyEvent(domain.Event{Kind: domain.EventKindMessageDelta, Text: chunk})
			}
		}
	})

	b.Run("reasoning_then_text", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			m := benchmarkModel(b, 220)
			m.showReasoning = true
			b.StartTimer()
			for _, chunk := range reasoningChunks {
				m.applyEvent(domain.Event{Kind: domain.EventKindReasoning, Text: chunk})
			}
			for _, chunk := range textChunks {
				m.applyEvent(domain.Event{Kind: domain.EventKindMessageDelta, Text: chunk})
			}
		}
	})
}

func BenchmarkHandleKeyTyping(b *testing.B) {
	m := benchmarkModel(b, 40)
	msg := ui.KeyMsg{Type: ui.KeyRunes, Runes: []rune("x")}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		next, _ := m.handleKey(msg)
		m = *next.(*Model)
	}
}

func BenchmarkHandleKeyTypingSlashMode(b *testing.B) {
	m := benchmarkModel(b, 40)
	m.composer.SetValue("/")
	msg := ui.KeyMsg{Type: ui.KeyRunes, Runes: []rune("n")}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		next, _ := m.handleKey(msg)
		m = *next.(*Model)
	}
}

func BenchmarkHandleKeyTypingMentionMode(b *testing.B) {
	m := benchmarkModel(b, 40)
	m.composer.SetValue("inspect @./cmd/")
	msg := ui.KeyMsg{Type: ui.KeyRunes, Runes: []rune("k")}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		next, _ := m.handleKey(msg)
		m = *next.(*Model)
	}
}

func BenchmarkHandleKeyTypingLargeDraft(b *testing.B) {
	m := benchmarkModel(b, 40)
	m.composer.SetValue(strings.Repeat("draft text ", 200))
	msg := ui.KeyMsg{Type: ui.KeyRunes, Runes: []rune("x")}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		next, _ := m.handleKey(msg)
		m = *next.(*Model)
	}
}

func BenchmarkUpdateComposerMenusSlash(b *testing.B) {
	m := benchmarkModel(b, 10)
	m.composer.SetValue("/per")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m.composerQueries.revision = 0
		m.updateComposerMenus()
	}
}

func BenchmarkUpdateComposerMenusSkill(b *testing.B) {
	m := benchmarkModel(b, 10)
	m.workdir = newSkillRepoTB(b)
	m.composer.SetValue("Investigate $rev")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m.composerQueries.revision = 0
		m.updateComposerMenus()
	}
}

func BenchmarkUpdateComposerMenusMention(b *testing.B) {
	m := benchmarkModel(b, 10)
	m.workdir = b.TempDir()
	if err := os.MkdirAll(filepath.Join(m.workdir, "cmd"), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.workdir, "cmd", "koder.go"), []byte("package main\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	m.composer.SetValue("inspect @./cmd/ko")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m.composerQueries.revision = 0
		m.updateComposerMenus()
	}
}

func BenchmarkViewWithModelDialog(b *testing.B) {
	m := benchmarkModel(b, 80)
	models := make([]domain.Model, 0, 120)
	for i := 0; i < 120; i++ {
		models = append(models, domain.Model{
			ID:      fmt.Sprintf("benchmark-model-%03d-long-name", i),
			OwnedBy: "provider",
		})
	}
	m.openModelDialog("benchmark", models)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = m.View()
	}
}

func BenchmarkViewLinesFooterOnlyInvalidation(b *testing.B) {
	m := benchmarkModel(b, 140)
	m.composer.SetValue("draft")
	_ = m.ViewLines()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m.invalidateFooterCache()
		_ = m.ViewLines()
	}
}

func benchmarkNonsenseChunks(chunkCount, wordsPerChunk int, seed int64) []string {
	rng := rand.New(rand.NewSource(seed))
	syllables := []string{
		"zor", "vek", "tal", "mur", "qen", "lix", "dra", "som", "pel", "nuv",
		"kir", "bex", "wom", "caz", "tir", "yul", "fex", "rav", "min", "glo",
	}
	chunks := make([]string, 0, chunkCount)
	for i := 0; i < chunkCount; i++ {
		var line strings.Builder
		for j := 0; j < wordsPerChunk; j++ {
			if j > 0 {
				line.WriteByte(' ')
			}
			parts := 2 + rng.Intn(3)
			for k := 0; k < parts; k++ {
				line.WriteString(syllables[rng.Intn(len(syllables))])
			}
		}
		line.WriteString(". ")
		chunks = append(chunks, line.String())
	}
	return chunks
}

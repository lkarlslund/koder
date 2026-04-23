package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/lkarlslund/koder/internal/ui/tea"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
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

func BenchmarkHandleKeyTyping(b *testing.B) {
	m := benchmarkModel(b, 40)
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		next, _ := m.handleKey(msg)
		m = *next.(*Model)
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

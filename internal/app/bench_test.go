package app

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
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/ui"
	"github.com/lkarlslund/koder/internal/workspace"
)

func benchmarkConfig(b *testing.B) config.Config {
	b.Helper()
	return config.Default().WithStateDir(b.TempDir())
}

func benchmarkModel(b *testing.B, messageCount int) App {
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
	m.currentSnapshot.Messages = make([]domain.Message, 0, messageCount)
	m.currentSnapshot.Parts = make(map[int64][]domain.Part, messageCount)
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
		m.currentSnapshot.Messages = append(m.currentSnapshot.Messages, msg)
		body := fmt.Sprintf("message %03d %s", i, strings.Repeat("content ", 10))
		m.currentSnapshot.Parts[msg.ID] = []domain.Part{{
			ID:        int64(i + 1),
			MessageID: msg.ID,
			Kind:      domain.PartKindText,
			Body:      body,
			CreatedAt: msg.CreatedAt,
		}}
		if role == domain.MessageRoleAssistant && i%5 == 1 {
			m.currentSnapshot.Parts[msg.ID] = append(m.currentSnapshot.Parts[msg.ID], domain.Part{
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

	b.Run("message_delta_then_frame", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			m := benchmarkModel(b, 220)
			b.StartTimer()
			for _, chunk := range textChunks {
				m.applyEvent(domain.Event{Kind: domain.EventKindMessageDelta, Text: chunk})
			}
			m.prepareFrame()
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
		m = *next.(*App)
	}
}

func BenchmarkHandleKeyTypingSlashMode(b *testing.B) {
	m := benchmarkModel(b, 40)
	m.composer.SetValue("/")
	msg := ui.KeyMsg{Type: ui.KeyRunes, Runes: []rune("n")}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		next, _ := m.handleKey(msg)
		m = *next.(*App)
	}
}

func BenchmarkHandleKeyTypingMentionMode(b *testing.B) {
	m := benchmarkModel(b, 40)
	m.composer.SetValue("inspect @./cmd/")
	msg := ui.KeyMsg{Type: ui.KeyRunes, Runes: []rune("k")}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		next, _ := m.handleKey(msg)
		m = *next.(*App)
	}
}

func BenchmarkHandleKeyTypingLargeDraft(b *testing.B) {
	m := benchmarkModel(b, 40)
	m.composer.SetValue(strings.Repeat("draft text ", 200))
	msg := ui.KeyMsg{Type: ui.KeyRunes, Runes: []rune("x")}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		next, _ := m.handleKey(msg)
		m = *next.(*App)
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
	m.openModelDialog(singleProviderModelEntries("benchmark", "benchmark", models))
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

func BenchmarkViewSurfaceComposerCursorDamage(b *testing.B) {
	m := benchmarkModel(b, 140)
	m.composer.SetValue("draft text")
	_ = m.viewSurface()
	msg := ui.KeyMsg{Type: ui.KeyLeft}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		next, _ := m.handleKey(msg)
		m = *next.(*App)
		_ = m.viewSurface()
	}
}

func BenchmarkViewSurfaceStatusDamage(b *testing.B) {
	m := benchmarkModel(b, 140)
	m.busy.start(busyScopeTranscript, "Working ...")
	_ = m.viewSurface()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if i%2 == 0 {
			m.busy.updateStatus("Working ...")
		} else {
			m.busy.updateStatus("Still working ...")
		}
		_ = m.viewSurface()
	}
}

func BenchmarkViewSurfaceSidebarToggle(b *testing.B) {
	m := benchmarkModel(b, 140)
	_ = m.viewSurface()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m.showSidebar = i%2 == 0
		m.resize()
		m.refreshViewportPreserve()
		_ = m.viewSurface()
	}
}

func BenchmarkViewSurfaceComposerMultilineGrowth(b *testing.B) {
	m := benchmarkModel(b, 140)
	_ = m.viewSurface()
	values := []string{
		"one line",
		"one line\ntwo line",
		"one line\ntwo line\nthree line",
		"one line\ntwo line\nthree line\nfour line",
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m.composer.SetValue(values[i%len(values)])
		m.updateComposerMenus()
		m.invalidateFooterCache()
		_ = m.viewSurface()
	}
}

func BenchmarkViewSurfaceTranscriptAppendSidebarVisible(b *testing.B) {
	m := benchmarkModel(b, 140)
	m.showSidebar = true
	m.resize()
	m.refreshViewport()
	_ = m.viewSurface()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m.appendLocalUserPrompt(fmt.Sprintf("benchmark append %d", i), nil, nil)
		m.refreshViewport()
		_ = m.viewSurface()
	}
}

func BenchmarkViewSurfaceMainResize(b *testing.B) {
	m := benchmarkModel(b, 140)
	_ = m.viewSurface()
	sizes := [][2]int{
		{120, 42},
		{132, 46},
		{96, 36},
		{140, 50},
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		size := sizes[i%len(sizes)]
		m.width = size[0]
		m.height = size[1]
		m.resize()
		m.refreshViewportPreserve()
		_ = m.viewSurface()
	}
}

func BenchmarkMainScreenRetainedPrepareIdle(b *testing.B) {
	m := benchmarkModel(b, 140)
	screen := m.ensureMainScreenView()
	ctx := &ui.Context{Palette: m.palette}
	bounds := ui.Rect{W: m.width, H: m.height}
	_ = screen.Surface(ctx, bounds)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = screen.Surface(ctx, bounds)
	}
}

func BenchmarkCenteredModalOpen(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m := benchmarkModel(b, 80)
		_ = m.viewSurface()
		m.openHelpModal()
		_ = m.viewSurface()
	}
}

func BenchmarkCenteredModalClose(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m := benchmarkModel(b, 80)
		m.openHelpModal()
		_ = m.viewSurface()
		m.closeHelpModal()
		_ = m.viewSurface()
	}
}

func BenchmarkMainWindowRetainedFullPaint(b *testing.B) {
	m := benchmarkModel(b, 140)
	_ = m.viewSurface()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m.invalidateBodyCache()
		_ = m.viewSurface()
	}
}

func BenchmarkOverlayStackRepaint(b *testing.B) {
	m := benchmarkModel(b, 80)
	m.openHelpModal()
	m.openThemePicker()
	_ = m.viewSurface()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m.themeDialog.Update(ui.KeyMsg{Type: ui.KeyRight})
		m.previewSelectedTheme()
		_ = m.viewSurface()
	}
}

func BenchmarkThemeSwitchRepaint(b *testing.B) {
	m := benchmarkModel(b, 80)
	_ = m.viewSurface()
	themes := []string{"tokyonight", "gruvbox"}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := m.setTheme(themes[i%len(themes)], false); err != nil {
			b.Fatalf("set theme: %v", err)
		}
		_ = m.viewSurface()
	}
}

func BenchmarkDialogPaint(b *testing.B) {
	benchmarkDialogPaint(b, "connect", benchmarkConnectDialogModel, func(m *App) {
		m.connectDialog.Update(ui.KeyMsg{Type: ui.KeyDown})
	})
	benchmarkDialogPaint(b, "model", benchmarkModelDialogModel, func(m *App) {
		m.modelDialog.Update(ui.KeyMsg{Type: ui.KeyDown})
	})
	benchmarkDialogPaint(b, "preferences", benchmarkPreferencesDialogModel, func(m *App) {
		m.preferences.Update(ui.KeyMsg{Type: ui.KeyRight})
	})
	benchmarkDialogPaint(b, "tools", benchmarkToolsDialogModel, func(m *App) {
		m.toolsDialog.Update(ui.KeyMsg{Type: ui.KeyDown})
	})
	benchmarkDialogPaint(b, "theme", benchmarkThemeDialogModel, func(m *App) {
		m.themeDialog.Update(ui.KeyMsg{Type: ui.KeyRight})
		m.previewSelectedTheme()
	})
	benchmarkDialogPaint(b, "picker", benchmarkPickerDialogModel, func(m *App) {
		m.picker.dialog.Update(ui.KeyMsg{Type: ui.KeyDown})
	})
	benchmarkDialogPaint(b, "approval", benchmarkApprovalDialogModel, func(m *App) {
		m.ensureApprovalDialog()
		m.approvalDialog.Update(ui.KeyMsg{Type: ui.KeyRight})
	})
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

func benchmarkDialogPaint(b *testing.B, name string, setup func(*testing.B) App, interact func(*App)) {
	b.Helper()
	b.Run(name+"/initial_full_paint", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			m := setup(b)
			_ = m.viewSurface()
		}
	})
	b.Run(name+"/steady_state", func(b *testing.B) {
		m := setup(b)
		_ = m.viewSurface()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = m.viewSurface()
		}
	})
	b.Run(name+"/interaction_repaint", func(b *testing.B) {
		m := setup(b)
		_ = m.viewSurface()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			interact(&m)
			_ = m.viewSurface()
		}
	})
}

func benchmarkConnectDialogModel(b *testing.B) App {
	b.Helper()
	m := benchmarkModel(b, 40)
	m.openConnectDialog()
	return m
}

func benchmarkModelDialogModel(b *testing.B) App {
	b.Helper()
	m := benchmarkModel(b, 40)
	models := make([]domain.Model, 0, 120)
	for i := 0; i < 120; i++ {
		models = append(models, domain.Model{
			ID:      fmt.Sprintf("benchmark-model-%03d-long-name", i),
			OwnedBy: "provider",
		})
	}
	m.openModelDialog(singleProviderModelEntries("benchmark", "benchmark", models))
	return m
}

func benchmarkPreferencesDialogModel(b *testing.B) App {
	b.Helper()
	m := benchmarkModel(b, 40)
	m.openPreferencesDialog()
	return m
}

func benchmarkToolsDialogModel(b *testing.B) App {
	b.Helper()
	m := benchmarkModel(b, 40)
	m.openToolsDialog()
	return m
}

func benchmarkThemeDialogModel(b *testing.B) App {
	b.Helper()
	m := benchmarkModel(b, 40)
	m.openThemePicker()
	return m
}

func benchmarkPickerDialogModel(b *testing.B) App {
	b.Helper()
	m := benchmarkModel(b, 40)
	m.openPermissionsPicker()
	return m
}

func benchmarkApprovalDialogModel(b *testing.B) App {
	b.Helper()
	m := benchmarkModel(b, 40)
	m.currentSnapshot.Approvals = []store.Approval{{
		ID:      7,
		Tool:    domain.ToolKindBash,
		Command: `{"command":"git status","tool_call_id":"call_1"}`,
	}}
	return m
}

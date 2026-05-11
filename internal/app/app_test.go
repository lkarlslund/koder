package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/lkarlslund/koder/internal/agent"
	"github.com/lkarlslund/koder/internal/attachment"
	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/debugsrv"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/execruntime"
	"github.com/lkarlslund/koder/internal/permission"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/reference"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/tools"
	"github.com/lkarlslund/koder/internal/tui/dialogs"
	"github.com/lkarlslund/koder/internal/ui"
	"github.com/lkarlslund/koder/internal/ui/textarea"
	"github.com/lkarlslund/koder/internal/workspace"
)

func TestMatchingSlashCommands(t *testing.T) {
	all := matchingSlashCommands("")
	if len(all) == 0 {
		t.Fatal("expected slash commands")
	}

	matches := matchingSlashCommands("n")
	if len(matches) != 1 {
		t.Fatalf("expected one match, got %d", len(matches))
	}
	if matches[0].Name != "/new" {
		t.Fatalf("expected /new, got %s", matches[0].Name)
	}

	matches = matchingSlashCommands("per")
	if len(matches) != 1 || matches[0].Name != "/permissions" {
		t.Fatalf("expected /permissions, got %#v", matches)
	}

	matches = matchingSlashCommands("comp")
	if len(matches) != 1 || matches[0].Name != "/compact" {
		t.Fatalf("expected /compact, got %#v", matches)
	}

	matches = matchingSlashCommands("pro")
	if len(matches) != 1 || matches[0].Name != "/provider" {
		t.Fatalf("expected /provider, got %#v", matches)
	}

	matches = matchingSlashCommands("fork")
	if len(matches) != 1 || matches[0].Name != "/fork" {
		t.Fatalf("expected /fork, got %#v", matches)
	}

	matches = matchingSlashCommands("mod")
	if len(matches) != 1 || matches[0].Name != "/model" {
		t.Fatalf("expected /model, got %#v", matches)
	}

	matches = matchingSlashCommands("res")
	if len(matches) != 1 || matches[0].Name != "/resume" {
		t.Fatalf("expected /resume, got %#v", matches)
	}

	matches = matchingSlashCommands("the")
	if len(matches) != 1 || matches[0].Name != "/theme" {
		t.Fatalf("expected /theme, got %#v", matches)
	}

	matches = matchingSlashCommands("ski")
	if len(matches) != 1 || matches[0].Name != "/skills" {
		t.Fatalf("expected /skills, got %#v", matches)
	}

	matches = matchingSlashCommands("rea")
	if len(matches) != 0 {
		t.Fatalf("expected tool slash commands to stay hidden, got %#v", matches)
	}
}

func TestComposerUpdatesKeepMainScreenCacheAndInvalidateComposerArea(t *testing.T) {
	m := App{
		cfg:         config.Default().WithStateDir(t.TempDir()),
		palette:     theme.Default().Palette,
		viewport:    newTranscriptViewport(80, 20),
		renderCache: &modelRenderCache{},
		composer:    textarea.New(),
		width:       80,
		height:      24,
	}
	m.composer.SetValue("draft text")

	_ = m.ViewLines()
	if !m.ensureRenderCache().bodyValid {
		t.Fatal("expected body cache to be primed")
	}
	if !m.ensureRenderCache().composerAreaValid {
		t.Fatal("expected composer area cache to be primed")
	}

	nextModel, _ := m.handleKey(ui.KeyMsg{Type: ui.KeyLeft})
	next := nextModel.(*App)

	if !next.ensureRenderCache().bodyValid {
		t.Fatal("expected composer update to keep the cached main screen surface for patching")
	}
	if next.ensureRenderCache().composerAreaValid {
		t.Fatal("expected composer-only update to invalidate composer area cache")
	}
}

func TestHandleKeyInsertsPlainRunesIntoComposer(t *testing.T) {
	m := App{
		cfg:      config.Default().WithStateDir(t.TempDir()),
		palette:  theme.Default().Palette,
		viewport: newTranscriptViewport(80, 20),
		composer: textarea.New(),
		width:    80,
		height:   24,
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Runes: []rune("a")})
	if cmd == nil {
		t.Fatal("expected composer input to schedule blink command")
	}
	next := *(updated.(*App))
	if got := next.composer.Value(); got != "a" {
		t.Fatalf("expected plain rune input to reach composer, got %q", got)
	}
}

func TestTabFocusesComposerThroughMainScreenFocusScope(t *testing.T) {
	composer := textarea.New()
	composer.Blur()
	m := App{
		cfg:      config.Default().WithStateDir(t.TempDir()),
		palette:  theme.Default().Palette,
		viewport: newTranscriptViewport(80, 20),
		composer: composer,
		width:    80,
		height:   24,
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyTab})
	next := updated.(*App)

	if cmd != nil {
		t.Fatal("expected no command from focus traversal")
	}
	if !next.composer.Focused() {
		t.Fatal("expected tab traversal to focus composer")
	}
}

func TestTypingIntoPrimedComposerUpdatesRenderedView(t *testing.T) {
	m := App{
		cfg:      config.Default().WithStateDir(t.TempDir()),
		palette:  theme.Default().Palette,
		viewport: newTranscriptViewport(80, 20),
		composer: textarea.New(),
		width:    80,
		height:   24,
	}

	_ = m.ViewLines()
	updated, _ := m.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Runes: []rune("hello")})
	next := *(updated.(*App))
	got := strings.Join(next.ViewLines(), "\n")
	if !strings.Contains(got, "hello") {
		t.Fatalf("expected typed composer text in rendered view, got %q", got)
	}
}

func TestComposerRevisionChangeMarksRetainedFooterDirty(t *testing.T) {
	m := App{
		cfg:      config.Default().WithStateDir(t.TempDir()),
		palette:  theme.Default().Palette,
		viewport: newTranscriptViewport(80, 20),
		composer: textarea.New(),
		width:    80,
		height:   24,
	}

	ctx := &ui.Context{Palette: m.palette}
	main := m.ensureMainScreenView()
	_ = main.Surface(ctx, ui.Rect{W: m.width, H: m.height})

	m.composer.InsertString("hello")
	m.syncMainScreenViewState()
	if !main.ComposerDirty() {
		t.Fatal("expected composer widget to become dirty from textarea revision change")
	}

	got := strings.Join(main.Surface(ctx, ui.Rect{W: m.width, H: m.height}).Lines(), "\n")
	if !strings.Contains(got, "hello") {
		t.Fatalf("expected retained surface to repaint composer text, got %q", got)
	}
}

func TestComposerCursorMoveProducesBottomOnlyDamage(t *testing.T) {
	m := App{
		cfg:         config.Default().WithStateDir(t.TempDir()),
		palette:     theme.Default().Palette,
		viewport:    newTranscriptViewport(80, 20),
		renderCache: &modelRenderCache{},
		composer:    textarea.New(),
		width:       80,
		height:      24,
	}
	m.composer.SetValue("draft text")

	_ = m.viewSurface()
	nextModel, _ := m.handleKey(ui.KeyMsg{Type: ui.KeyLeft})
	next := nextModel.(*App)

	surface := next.viewSurface()
	rects, ok := surface.DirtyRects()
	if !ok || len(rects) == 0 {
		t.Fatal("expected dirty rects after composer cursor move")
	}
	damageStart := surface.SurfaceHeight() - next.statusPaneHeight() - next.composerAreaHeight()
	totalArea := 0
	for _, rect := range rects {
		if rect.Y < damageStart {
			t.Fatalf("expected composer damage to stay near footer, got rect %#v with damage start %d", rect, damageStart)
		}
		totalArea += rect.W * rect.H
	}
	if totalArea > 4 {
		t.Fatalf("expected cursor move to stay tightly localized, got rects %#v", rects)
	}
}

func TestCtrlBTogglesBouncyBallsAndSchedulesAnimation(t *testing.T) {
	m := App{
		cfg:         config.Default().WithStateDir(t.TempDir()),
		palette:     theme.Default().Palette,
		viewport:    newTranscriptViewport(80, 20),
		renderCache: &modelRenderCache{},
		composer:    textarea.New(),
		width:       80,
		height:      24,
	}

	updated, cmd := m.Update(ui.KeyMsg{Type: ui.KeyCtrlB})
	next := asModelPtr(t, updated)
	if !next.bouncyBalls.Enabled {
		t.Fatal("expected ctrl+b to enable bouncy balls")
	}
	if got := next.status; got != "Bouncy balls enabled" {
		t.Fatalf("expected enable status, got %q", got)
	}
	if cmd == nil {
		t.Fatal("expected bouncy balls to schedule an animation tick")
	}
	if len(next.bouncyBalls.balls) != 4 {
		t.Fatalf("expected 4 balls, got %d", len(next.bouncyBalls.balls))
	}
	for _, ball := range next.bouncyBalls.balls {
		if ball.x <= 0 || ball.x >= float64(next.width) || ball.y <= 0 || ball.y >= float64(next.height) {
			t.Fatalf("expected randomized ball position within viewport, got (%f,%f) in %dx%d", ball.x, ball.y, next.width, next.height)
		}
	}
	for idx, ball := range next.bouncyBalls.balls {
		if ball.radius <= 0 {
			t.Fatalf("expected positive radius for ball %d, got %f", idx, ball.radius)
		}
	}
	if !(next.bouncyBalls.balls[0].radius < next.bouncyBalls.balls[1].radius &&
		next.bouncyBalls.balls[1].radius < next.bouncyBalls.balls[2].radius &&
		next.bouncyBalls.balls[2].radius < next.bouncyBalls.balls[3].radius) {
		t.Fatalf("expected scaled radii to remain ordered, got %#v", next.bouncyBalls.balls)
	}
	uniqueSpeeds := map[string]struct{}{}
	for _, ball := range next.bouncyBalls.balls {
		key := fmt.Sprintf("%.3f:%.3f", ball.vx, ball.vy)
		uniqueSpeeds[key] = struct{}{}
	}
	if len(uniqueSpeeds) < 4 {
		t.Fatalf("expected individual randomized velocities, got %#v", next.bouncyBalls.balls)
	}

	beforeX, beforeY := next.bouncyBalls.balls[0].x, next.bouncyBalls.balls[0].y
	seq := next.bouncyBalls.tickSeq
	updated, cmd = next.Update(bouncyBallsTickMsg{Seq: next.bouncyBalls.tickSeq, At: time.Now()})
	next = asModelPtr(t, updated)
	if next.bouncyBalls.balls[0].x == beforeX && next.bouncyBalls.balls[0].y == beforeY {
		t.Fatal("expected bouncy balls tick to move the overlay")
	}
	if cmd == nil {
		t.Fatal("expected each bouncy balls tick to schedule the next tick")
	}

	updated, _ = next.Update(ui.KeyMsg{Type: ui.KeyCtrlB})
	next = asModelPtr(t, updated)
	if next.bouncyBalls.Enabled {
		t.Fatal("expected second ctrl+b to disable bouncy balls")
	}
	if got := next.status; got != "Bouncy balls disabled" {
		t.Fatalf("expected disable status, got %q", got)
	}
	updated, _ = next.Update(bouncyBallsTickMsg{Seq: seq, At: time.Now()})
	next = asModelPtr(t, updated)
	if next.bouncyBalls.tickPending {
		t.Fatal("expected stale bouncy balls tick to be ignored after disable")
	}
}

func TestStatusUpdateProducesBottomOnlyDamage(t *testing.T) {
	m := App{
		cfg:         config.Default().WithStateDir(t.TempDir()),
		palette:     theme.Default().Palette,
		viewport:    newTranscriptViewport(80, 20),
		renderCache: &modelRenderCache{},
		composer:    textarea.New(),
		width:       80,
		height:      24,
	}
	m.busy.start(busyScopeTranscript, "Working ...")

	_ = m.viewSurface()
	m.busy.updateStatus("Still working ...")

	surface := m.viewSurface()
	rects, ok := surface.DirtyRects()
	if !ok || len(rects) == 0 {
		t.Fatal("expected dirty rects after status update")
	}
	damageStart := surface.SurfaceHeight() - m.statusPaneHeight()
	for _, rect := range rects {
		if rect.Y < damageStart {
			t.Fatalf("expected status damage to stay on status rows, got rect %#v with damage start %d", rect, damageStart)
		}
	}
}

func TestShouldRefreshDetailsAfterEvent(t *testing.T) {
	cases := []struct {
		name string
		evt  domain.Event
		want bool
	}{
		{name: "message delta", evt: domain.Event{Kind: domain.EventKindMessageDelta}, want: false},
		{name: "reasoning", evt: domain.Event{Kind: domain.EventKindReasoning}, want: false},
		{name: "usage", evt: domain.Event{Kind: domain.EventKindUsage}, want: false},
		{name: "status", evt: domain.Event{Kind: domain.EventKindStatus}, want: false},
		{name: "session title", evt: domain.Event{Kind: domain.EventKindSessionTitle}, want: false},
		{name: "tool call delta", evt: domain.Event{Kind: domain.EventKindToolCallDelta}, want: false},
		{name: "tool result", evt: domain.Event{Kind: domain.EventKindToolResult}, want: false},
		{name: "approval ask", evt: domain.Event{Kind: domain.EventKindApprovalAsk}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldRefreshDetailsAfterEvent(tc.evt); got != tc.want {
				t.Fatalf("shouldRefreshDetailsAfterEvent(%q) = %v, want %v", tc.evt.Kind, got, tc.want)
			}
		})
	}
}

func TestToolCallDeltaAppendsCurrentChatImmediately(t *testing.T) {
	now := time.Now().UTC()
	msg := domain.Message{
		ID:        10,
		SessionID: 1,
		ChatID:    2,
		Role:      domain.MessageRoleAssistant,
		Summary:   "tool:bash",
		CreatedAt: now,
	}
	parts := []domain.Part{{
		ID:        11,
		MessageID: msg.ID,
		Kind:      domain.PartKindToolCall,
		Payload: domain.ToolCallPayload{
			Tool:       domain.ToolKindBash,
			ToolCallID: "call_1",
			Args:       map[string]string{"command": "pwd"},
		},
		CreatedAt: now,
	}}
	m := App{
		cfg:             testConfig(t),
		currentSession:  domain.Session{ID: 1},
		currentChat:     domain.Chat{ID: 2},
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
		composer:        textarea.New(),
	}
	events := make(chan domain.Event)
	defer close(events)

	updated, cmd := m.Update(eventMsg{
		chatID: 2,
		event: domain.Event{
			Kind:    domain.EventKindToolCallDelta,
			Text:    "tool calls persisted",
			Message: msg,
			Parts:   parts,
		},
		events: events,
	})
	next := updated.(App)
	if cmd == nil {
		t.Fatal("expected follow-up command for remaining event stream")
	}
	if len(next.currentSnapshot.Messages) != 1 {
		t.Fatalf("expected current chat to append immediately, got %d messages", len(next.currentSnapshot.Messages))
	}
	blocks := next.transcriptBlocks()
	if len(blocks) != 1 || blocks[0].Kind != transcriptBlockToolRun {
		t.Fatalf("expected requested tool run block, got %#v", blocks)
	}
	if blocks[0].ToolRun.Status != ui.ToolRunStatusRequested {
		t.Fatalf("expected requested status, got %#v", blocks[0].ToolRun)
	}
	if blocks[0].ToolRun.ToolCallID != "call_1" {
		t.Fatalf("unexpected tool call id: %#v", blocks[0].ToolRun)
	}
}

func TestProviderToolCallDeltaRendersLiveToolRun(t *testing.T) {
	m := App{
		cfg:             testConfig(t),
		currentSession:  domain.Session{ID: 1},
		currentChat:     domain.Chat{ID: 2},
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
		composer:        textarea.New(),
	}

	m.applyEvent(domain.Event{
		Kind:       domain.EventKindToolCallDelta,
		Tool:       domain.ToolKindRead,
		ToolCallID: "call_1",
		Meta:       map[string]string{"arguments": `{"path":"main.go","limit":"20"}`},
	})

	if len(m.transcriptItems) != 1 {
		t.Fatalf("expected live tool run item, got %d", len(m.transcriptItems))
	}
	item, ok := m.transcriptItems[0].(toolRunTranscriptItem)
	if !ok {
		t.Fatalf("expected tool run transcript item, got %#v", m.transcriptItems[0])
	}
	run := m.transcriptToolRunValue(item)
	if run.Tool != domain.ToolKindRead || run.ToolCallID != "call_1" {
		t.Fatalf("unexpected live tool run: %#v", run)
	}
	if !strings.Contains(run.Preview, "main.go") {
		t.Fatalf("expected live tool preview to include path, got %#v", run)
	}
	if m.busy.transcriptPhase != transcriptBusyPhaseTools {
		t.Fatalf("expected tools busy phase, got %#v", m.busy.transcriptPhase)
	}
}

func TestToolResultEventUpdatesRequestedRunInMemory(t *testing.T) {
	now := time.Now().UTC()
	callMsg := domain.Message{
		ID:        10,
		SessionID: 1,
		ChatID:    2,
		Role:      domain.MessageRoleAssistant,
		Summary:   "tool:bash",
		CreatedAt: now,
	}
	callParts := []domain.Part{{
		ID:        11,
		MessageID: callMsg.ID,
		Kind:      domain.PartKindToolCall,
		Payload: domain.ToolCallPayload{
			Tool:       domain.ToolKindBash,
			ToolCallID: "call_1",
			Args:       map[string]string{"command": "pwd"},
		},
		CreatedAt: now,
	}}
	resultMsg := domain.Message{
		ID:        12,
		SessionID: 1,
		ChatID:    2,
		Role:      domain.MessageRoleTool,
		Summary:   "bash",
		CreatedAt: now.Add(time.Second),
	}
	resultParts := []domain.Part{{
		ID:        13,
		MessageID: resultMsg.ID,
		Kind:      domain.PartKindToolOutput,
		Payload: domain.ToolOutputPayload{
			Tool:       domain.ToolKindBash,
			ToolCallID: "call_1",
			Status:     domain.ToolResultStatusOK,
			Text:       "/tmp/project",
			Result:     tools.BashStoredResult{Command: "pwd", Output: "/tmp/project"},
		},
		CreatedAt: now.Add(time.Second),
	}}
	m := App{
		cfg:             testConfig(t),
		currentSession:  domain.Session{ID: 1},
		currentChat:     domain.Chat{ID: 2},
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
		composer:        textarea.New(),
	}
	updated, _ := m.Update(eventMsg{chatID: 2, event: domain.Event{Kind: domain.EventKindToolCallDelta, Message: callMsg, Parts: callParts}, events: make(chan domain.Event)})
	m = updated.(App)
	updated, _ = m.Update(eventMsg{chatID: 2, event: domain.Event{Kind: domain.EventKindToolResult, Tool: domain.ToolKindBash, ToolCallID: "call_1", Message: resultMsg, Parts: resultParts}, events: make(chan domain.Event)})
	m = updated.(App)

	blocks := m.transcriptBlocks()
	if len(blocks) != 1 || blocks[0].Kind != transcriptBlockToolRun {
		t.Fatalf("expected one merged tool run block, got %#v", blocks)
	}
	run := blocks[0].ToolRun
	if run.Status != ui.ToolRunStatusCompleted {
		t.Fatalf("expected completed status, got %#v", run)
	}
	if run.Output != "/tmp/project" {
		t.Fatalf("expected output to update in place, got %#v", run)
	}
}

func TestMessageDonePersistsAssistantWithoutReload(t *testing.T) {
	now := time.Now().UTC()
	msg := domain.Message{
		ID:        21,
		SessionID: 1,
		ChatID:    2,
		Role:      domain.MessageRoleAssistant,
		Summary:   "done",
		CreatedAt: now,
	}
	parts := []domain.Part{{
		ID:        22,
		MessageID: msg.ID,
		Kind:      domain.PartKindText,
		Payload:   domain.TextPayload{Text: "done"},
		CreatedAt: now,
	}}
	m := App{
		cfg:               testConfig(t),
		currentSession:    domain.Session{ID: 1},
		currentChat:       domain.Chat{ID: 2},
		currentSnapshot:   chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
		composer:          textarea.New(),
		activeEventStream: true,
	}
	m.applyEvent(domain.Event{Kind: domain.EventKindMessageDelta, Text: "done"})

	updated, _ := m.Update(eventMsg{
		chatID: 2,
		event:  domain.Event{Kind: domain.EventKindMessageDone, Message: msg, Parts: parts},
		events: make(chan domain.Event),
	})
	m = updated.(App)

	if strings.TrimSpace(m.currentSnapshot.PendingAssistant.Text) != "" || strings.TrimSpace(m.currentSnapshot.PendingAssistant.Reasoning) != "" {
		t.Fatalf("expected pending assistant cleared, got %#v", m.currentSnapshot.PendingAssistant)
	}
	blocks := m.transcriptBlocks()
	if len(blocks) != 1 || blocks[0].Kind != transcriptBlockMessage {
		t.Fatalf("expected one persisted assistant block, got %#v", blocks)
	}
	if blocks[0].Message.ID != msg.ID || firstPartBody(blocks[0].Parts, domain.PartKindText) != "done" {
		t.Fatalf("unexpected persisted assistant block %#v", blocks[0])
	}
}

func TestApprovalAskEventAppendsPendingApprovalToolRun(t *testing.T) {
	now := time.Now().UTC()
	msg := domain.Message{
		ID:        30,
		SessionID: 1,
		ChatID:    2,
		Role:      domain.MessageRoleTool,
		Summary:   "approval:bash",
		CreatedAt: now,
	}
	parts := []domain.Part{{
		ID:        31,
		MessageID: msg.ID,
		Kind:      domain.PartKindApprovalRequest,
		Payload: domain.ApprovalRequestPayload{
			ApprovalID: 44,
			Tool:       domain.ToolKindBash,
			ToolCallID: "call_1",
			Command:    "pwd",
			Status:     domain.ApprovalStatusPending,
			Body:       "Approval required for bash: pwd",
		},
		CreatedAt: now,
	}}
	m := App{
		cfg:             testConfig(t),
		currentSession:  domain.Session{ID: 1},
		currentChat:     domain.Chat{ID: 2},
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
		composer:        textarea.New(),
	}

	updated, _ := m.Update(eventMsg{
		chatID: 2,
		event: domain.Event{
			Kind:    domain.EventKindApprovalAsk,
			Text:    "bash requires approval",
			Tool:    domain.ToolKindBash,
			Message: msg,
			Parts:   parts,
			Meta: map[string]string{
				"approval_id":  "44",
				"command":      "pwd",
				"tool_call_id": "call_1",
			},
		},
		events: make(chan domain.Event),
	})
	m = updated.(App)

	if len(m.currentSnapshot.Approvals) != 1 || m.currentSnapshot.Approvals[0].ID != 44 {
		t.Fatalf("expected pending approval snapshot, got %#v", m.currentSnapshot.Approvals)
	}
	blocks := m.transcriptBlocks()
	if len(blocks) != 1 || blocks[0].Kind != transcriptBlockToolRun {
		t.Fatalf("expected approval tool run block, got %#v", blocks)
	}
	if blocks[0].ToolRun.Status != ui.ToolRunStatusPendingApproval {
		t.Fatalf("expected pending approval status, got %#v", blocks[0].ToolRun)
	}
}

func TestApprovalReplyEventRemovesPendingApproval(t *testing.T) {
	now := time.Now().UTC()
	askMsg := domain.Message{
		ID:        30,
		SessionID: 1,
		ChatID:    2,
		Role:      domain.MessageRoleTool,
		Summary:   "approval:bash",
		CreatedAt: now,
	}
	askParts := []domain.Part{{
		ID:        31,
		MessageID: askMsg.ID,
		Kind:      domain.PartKindApprovalRequest,
		Payload: domain.ApprovalRequestPayload{
			ApprovalID: 44,
			Tool:       domain.ToolKindBash,
			ToolCallID: "call_1",
			Command:    "pwd",
			Status:     domain.ApprovalStatusPending,
			Body:       "Approval required for bash: pwd",
		},
		CreatedAt: now,
	}}
	replyMsg := domain.Message{
		ID:        32,
		SessionID: 1,
		ChatID:    2,
		Role:      domain.MessageRoleTool,
		Summary:   "approval:bash:approved",
		CreatedAt: now.Add(time.Second),
	}
	replyParts := []domain.Part{{
		ID:        33,
		MessageID: replyMsg.ID,
		Kind:      domain.PartKindToolOutput,
		Payload: domain.ToolOutputPayload{
			Tool:       domain.ToolKindBash,
			ToolCallID: "call_1",
			Args:       map[string]string{"approval_id": "44", "tool": "bash", "status": "approved", "command": "pwd", "tool_call_id": "call_1"},
			Status:     domain.ToolResultStatusOK,
			Text:       "Approval 44 approved for bash: pwd",
		},
		CreatedAt: now.Add(time.Second),
	}}
	m := App{
		cfg:             testConfig(t),
		currentSession:  domain.Session{ID: 1},
		currentChat:     domain.Chat{ID: 2},
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
		composer:        textarea.New(),
	}
	updated, _ := m.Update(eventMsg{chatID: 2, event: domain.Event{
		Kind:    domain.EventKindApprovalAsk,
		Text:    "bash requires approval",
		Tool:    domain.ToolKindBash,
		Message: askMsg,
		Parts:   askParts,
		Meta:    map[string]string{"approval_id": "44", "command": "pwd", "tool_call_id": "call_1"},
	}, events: make(chan domain.Event)})
	m = updated.(App)
	updated, _ = m.Update(eventMsg{chatID: 2, event: domain.Event{
		Kind:    domain.EventKindApprovalReply,
		Text:    "approval 44 approved",
		Tool:    domain.ToolKindBash,
		Message: replyMsg,
		Parts:   replyParts,
		Meta:    map[string]string{"approval_id": "44", "tool_call_id": "call_1"},
	}, events: make(chan domain.Event)})
	m = updated.(App)

	if len(m.currentSnapshot.Approvals) != 0 {
		t.Fatalf("expected pending approval removed, got %#v", m.currentSnapshot.Approvals)
	}
	blocks := m.transcriptBlocks()
	if len(blocks) != 1 || blocks[0].Kind != transcriptBlockToolRun {
		t.Fatalf("expected single approval tool run block, got %#v", blocks)
	}
	if blocks[0].ToolRun.Status != ui.ToolRunStatusApproved {
		t.Fatalf("expected approved tool run status, got %#v", blocks[0].ToolRun)
	}
}

func TestSkillQuery(t *testing.T) {
	query, start, ok := skillQuery("Investigate $rev")
	if !ok || query != "rev" {
		t.Fatalf("unexpected skill query: ok=%v query=%q start=%d", ok, query, start)
	}
	if start != len("Investigate ") {
		t.Fatalf("unexpected start index %d", start)
	}
	if _, _, ok := skillQuery("Investigate$rev"); ok {
		t.Fatal("expected no skill query when $ is embedded in a word")
	}
	if _, _, ok := skillQuery("Investigate $rev more"); ok {
		t.Fatal("expected no skill query when token is not at end")
	}
}

func TestMentionQuery(t *testing.T) {
	query, start, pathMode, ok := mentionQuery("inspect @rea", len("inspect @rea"))
	if !ok || query != "rea" || pathMode {
		t.Fatalf("unexpected mention query: ok=%v query=%q pathMode=%v start=%d", ok, query, pathMode, start)
	}
	if start != len("inspect ") {
		t.Fatalf("unexpected mention start %d", start)
	}
	query, _, pathMode, ok = mentionQuery("inspect @./cmd/ko", len("inspect @./cmd/ko"))
	if !ok || query != "./cmd/ko" || !pathMode {
		t.Fatalf("unexpected path mention query: ok=%v query=%q pathMode=%v", ok, query, pathMode)
	}
}

func TestAcceptMentionSelectionInsertsStructuredReference(t *testing.T) {
	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, "README.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	composer := textarea.New()
	composer.SetValue("inspect @rea")
	composer.SetCursor(len("inspect @rea"))
	m := App{
		cfg:            testConfig(t),
		composer:       composer,
		workdir:        workdir,
		mentionMatches: []reference.Entry{{Kind: reference.KindFile, Path: "README.md", Display: "README.md", Description: "file"}},
	}
	m.acceptMentionSelection()
	if got := m.composer.Value(); got != "inspect @README.md" {
		t.Fatalf("unexpected composer value %q", got)
	}
	if len(m.draftReferences) != 1 {
		t.Fatalf("expected one structured reference, got %#v", m.draftReferences)
	}
	ref := m.draftReferences[0]
	if ref.Path != "README.md" || ref.Display != "@README.md" {
		t.Fatalf("unexpected reference %#v", ref)
	}
	if ref.Start != len("inspect ") || ref.End != len("inspect @README.md") {
		t.Fatalf("unexpected reference offsets %#v", ref)
	}
}

func TestMentionTokenBackspaceRemovesWholeReference(t *testing.T) {
	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, "README.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	composer := textarea.New()
	composer.SetValue("inspect @rea")
	composer.SetCursor(len("inspect @rea"))
	m := App{
		cfg:            testConfig(t),
		composer:       composer,
		workdir:        workdir,
		mentionMatches: []reference.Entry{{Kind: reference.KindFile, Path: "README.md", Display: "README.md", Description: "file"}},
	}
	m.acceptMentionSelection()
	m.composer.SetCursor(len("inspect @READ"))

	updated, _ := m.handleKey(ui.KeyMsg{Type: ui.KeyBackspace})
	next := updated.(*App)
	if got := next.composer.Value(); got != "inspect " {
		t.Fatalf("expected reference token to be removed atomically, got %q", got)
	}
	if len(next.draftReferences) != 0 {
		t.Fatalf("expected structured reference removed with token, got %#v", next.draftReferences)
	}
}

func TestHandleLocalCommandOpensPermissionsPicker(t *testing.T) {
	m := App{
		cfg:      testConfig(t),
		composer: textarea.New(),
	}
	model, cmd, ok := m.handleLocalCommand("/permissions")
	if !ok {
		t.Fatal("expected local command to be handled")
	}
	if cmd == nil {
		t.Fatal("expected sync title command")
	}
	next := model.(*App)
	if !next.hasPicker() {
		t.Fatal("expected permissions picker to open")
	}
	if next.picker.mode != pickerModePermissions {
		t.Fatalf("expected permissions picker mode, got %v", next.picker.mode)
	}
}

func TestHandleLocalCommandOpensSkillsPicker(t *testing.T) {
	workdir := newSkillRepo(t)
	m := App{
		cfg:      testConfig(t),
		composer: textarea.New(),
		workdir:  workdir,
	}
	model, cmd, ok := m.handleLocalCommand("/skills")
	if !ok {
		t.Fatal("expected local command to be handled")
	}
	if cmd == nil {
		t.Fatal("expected sync title command")
	}
	next := model.(*App)
	if !next.hasPicker() {
		t.Fatal("expected skills picker to open")
	}
	if next.picker.mode != pickerModeSkills {
		t.Fatalf("expected skills picker mode, got %v", next.picker.mode)
	}
	if len(next.picker.dialog.Items) != 1 || next.picker.dialog.Items[0].Value != "review" {
		t.Fatalf("unexpected picker items: %#v", next.picker.dialog.Items)
	}
}

func TestSkillAutocompleteAcceptsSelection(t *testing.T) {
	workdir := newSkillRepo(t)
	m := App{
		cfg:      testConfig(t),
		composer: textarea.New(),
		workdir:  workdir,
	}
	m.composer.SetValue("Use $rev")
	m.updateComposerMenus()
	if !m.hasSkillMenu() {
		t.Fatal("expected skill menu")
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyTab})
	next := updated.(*App)
	if cmd != nil {
		t.Fatal("expected no async command")
	}
	if got := next.composer.Value(); got != "Use $review" {
		t.Fatalf("expected completed skill mention, got %q", got)
	}
	if next.hasSkillMenu() {
		t.Fatal("expected skill menu to close after selection")
	}
}

func TestSkillTokenDeleteRemovesWholeToken(t *testing.T) {
	workdir := newSkillRepo(t)
	m := App{
		cfg:      testConfig(t),
		composer: textarea.New(),
		workdir:  workdir,
	}
	m.composer.SetValue("Use $rev")
	m.updateComposerMenus()
	updated, _ := m.handleKey(ui.KeyMsg{Type: ui.KeyTab})
	next := updated.(*App)
	next.composer.SetCursor(len("Use $rev"))

	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyDelete})
	final := updated.(*App)
	if got := final.composer.Value(); got != "Use " {
		t.Fatalf("expected skill token to be removed atomically, got %q", got)
	}
}

func TestSkillsPickerSelectionInsertsToken(t *testing.T) {
	workdir := newSkillRepo(t)
	m := App{
		cfg:      testConfig(t),
		composer: textarea.New(),
		workdir:  workdir,
	}
	m.openSkillsPicker()
	model, cmd := m.submitPickerSelection("review")
	next := model.(*App)
	if cmd == nil {
		t.Fatal("expected sync title command")
	}
	if got := next.composer.Value(); got != "$review" {
		t.Fatalf("expected inserted skill token, got %q", got)
	}
	if next.hasPicker() {
		t.Fatal("expected picker to close after selection")
	}
}

func TestPermissionsCommandOpensWhileBusy(t *testing.T) {
	m := App{
		cfg:      testConfig(t),
		composer: textarea.New(),
		loading:  true,
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
		},
	}
	m.composer.SetValue("/permissions")
	m.updateComposerMenus()

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected permissions command to execute while busy")
	}
	if !next.hasPicker() {
		t.Fatal("expected permissions picker to open while busy")
	}
	if len(next.currentChat.QueuedInputs) != 0 {
		t.Fatalf("expected no queued prompt, got %#v", next.currentChat.QueuedInputs)
	}
}

func TestPermissionsPickerSelectionUpdatesDraftSession(t *testing.T) {
	m := App{
		cfg:      testConfig(t),
		composer: textarea.New(),
	}
	m.openPermissionsPicker()
	for idx, item := range m.picker.dialog.Items {
		if item.Value == permission.ProfileWriteAsk {
			m.picker.dialog.Index = idx
			break
		}
	}
	model, _ := m.submitPickerSelection(permission.ProfileWriteAsk)
	next := model.(*App)
	if next.currentSession.PermissionProfile != permission.ProfileWriteAsk {
		t.Fatalf("expected draft session permission profile updated, got %q", next.currentSession.PermissionProfile)
	}
	if !strings.Contains(next.pendingModelNote, "Permission mode changed to write / ask.") {
		t.Fatalf("expected pending model note, got %q", next.pendingModelNote)
	}
	if next.hasPicker() {
		t.Fatal("expected picker to close after selection")
	}
}

func TestEnterShowsOptimisticUserPromptBeforePromptStarts(t *testing.T) {
	cfg := testConfig(t)
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "model"
	cfg.Providers = map[string]config.Provider{
		"test": {BaseURL: "https://example.invalid/v1", APIKey: "secret", DefaultModel: "model"},
	}
	m := App{
		cfg:             cfg,
		composer:        textarea.New(),
		palette:         theme.Resolve("tokyonight").Palette,
		viewport:        newTranscriptViewport(60, 10),
		currentSession:  domain.Session{ID: 1, ProviderID: "test", ModelID: "model"},
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
	}
	m.composer.SetValue("hello there")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected prompt kickoff command")
	}
	if len(next.currentSnapshot.Messages) != 1 {
		t.Fatalf("expected optimistic user message, got %#v", next.currentSnapshot.Messages)
	}
	if next.currentSnapshot.Messages[0].Role != domain.MessageRoleUser || next.currentSnapshot.Messages[0].Summary != "hello there" {
		t.Fatalf("unexpected optimistic message: %#v", next.currentSnapshot.Messages[0])
	}
	if got := next.viewport.View(); !strings.Contains(got, "hello there") {
		t.Fatalf("expected viewport to show optimistic user prompt, got %q", got)
	}

	msg := cmd()
	if _, ok := msg.(kickoffPromptMsg); !ok {
		t.Fatalf("expected kickoffPromptMsg before async prompt starts, got %#v", msg)
	}
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	return config.Default().WithStateDir(t.TempDir())
}

func asModelPtr(t *testing.T, model ui.Model) *App {
	t.Helper()
	switch next := model.(type) {
	case *App:
		return next
	case App:
		return &next
	default:
		t.Fatalf("unexpected model type %T", model)
		return nil
	}
}

func mustMarshalMeta(t *testing.T, meta map[string]string) string {
	t.Helper()
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	return string(raw)
}

func newSkillRepo(t *testing.T) string {
	t.Helper()
	return newSkillRepoTB(t)
}

func newSkillRepoTB(tb testing.TB) string {
	tb.Helper()
	home := tb.TempDir()
	if setter, ok := any(tb).(interface{ Setenv(string, string) }); ok {
		setter.Setenv("HOME", home)
	}

	repo := filepath.Join(tb.TempDir(), "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		tb.Fatal(err)
	}
	path := filepath.Join(repo, ".agents", "skills", "review", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\nname: review\ndescription: Review code carefully\n---\n"), 0o644); err != nil {
		tb.Fatal(err)
	}
	return repo
}

func TestSlashQuery(t *testing.T) {
	query, ok := slashQuery("/")
	if !ok || query != "" {
		t.Fatalf("unexpected slash query for /: ok=%v query=%q", ok, query)
	}

	query, ok = slashQuery("/new")
	if !ok || query != "new" {
		t.Fatalf("unexpected slash query for /new: ok=%v query=%q", ok, query)
	}

	if _, ok := slashQuery("/mouse on"); ok {
		t.Fatal("expected no autocomplete query after slash command arguments start")
	}
}

func TestComposerQueryHelpers(t *testing.T) {
	composer := textarea.New()
	composer.SetValue("inspect @./cmd/ko")
	composer.SetCursor(len("inspect @./cmd/ko"))

	slash, ok := slashQueryFromComposer(composer)
	if ok || slash != "" {
		t.Fatalf("unexpected slash query: ok=%v query=%q", ok, slash)
	}

	query, start, pathMode, ok := mentionQuery(composer.Value(), len(composer.Value()))
	if !ok {
		t.Fatal("expected string mention query")
	}
	composerQuery, composerStart, composerEnd, composerPathMode, composerOK := mentionQueryFromComposer(composer)
	if !composerOK {
		t.Fatal("expected composer mention query")
	}
	if composerQuery != query || composerStart != start || !composerPathMode || composerPathMode != pathMode {
		t.Fatalf("unexpected composer mention query: got %q %d %v want %q %d %v", composerQuery, composerStart, composerPathMode, query, start, pathMode)
	}
	if composerEnd != len(composer.Value()) {
		t.Fatalf("unexpected mention end: got %d want %d", composerEnd, len(composer.Value()))
	}

	composer.SetValue("Investigate $rev")
	composer.SetCursor(len("Investigate $rev"))
	query, start, ok = skillQuery(composer.Value())
	if !ok {
		t.Fatal("expected string skill query")
	}
	composerQuery, composerStart, composerOK2 := skillQueryFromComposer(composer)
	if !composerOK2 {
		t.Fatal("expected composer skill query")
	}
	if composerQuery != query || composerStart != start {
		t.Fatalf("unexpected composer skill query: got %q %d want %q %d", composerQuery, composerStart, query, start)
	}

	composer.SetValue("/new")
	composer.SetCursor(len("/new"))
	query, ok = slashQuery(composer.Value())
	if !ok {
		t.Fatal("expected string slash query")
	}
	composerQuery, composerOK2 = slashQueryFromComposer(composer)
	if !composerOK2 || composerQuery != query {
		t.Fatalf("unexpected composer slash query: got %q %v want %q true", composerQuery, composerOK2, query)
	}
}

func TestEnterSendsNormalPrompt(t *testing.T) {
	cfg := testConfig(t)
	cfg.DefaultProvider = "openai"
	cfg.DefaultModel = "gpt-5.4"
	cfg.Providers = map[string]config.Provider{
		"openai": {
			Kind:         "openai-compatible",
			AuthMethod:   "api_key",
			BaseURL:      "https://api.openai.com/v1",
			APIKey:       "secret",
			DefaultModel: "gpt-5.4",
		},
	}
	m := App{
		cfg:             cfg,
		composer:        textarea.New(),
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
	}
	m.composer.SetValue("hello")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected send command")
	}
	if !next.loading {
		t.Fatal("expected loading after enter")
	}
	if next.composer.Value() != "" {
		t.Fatalf("expected composer reset, got %q", next.composer.Value())
	}
	if len(next.currentSnapshot.Messages) != 1 || next.currentSnapshot.Messages[0].Summary != "hello" {
		t.Fatalf("expected optimistic user message, got %#v", next.currentSnapshot.Messages)
	}
	if len(next.currentSnapshot.Parts[next.currentSnapshot.Messages[0].ID]) != 1 || next.currentSnapshot.Parts[next.currentSnapshot.Messages[0].ID][0].Body != "hello" {
		t.Fatalf("expected optimistic user part, got %#v", next.currentSnapshot.Parts)
	}
}

func TestAltEnterInsertsNewlineInsteadOfSending(t *testing.T) {
	cfg := testConfig(t)
	cfg.DefaultProvider = "openai"
	cfg.DefaultModel = "gpt-5.4"
	cfg.Providers = map[string]config.Provider{
		"openai": {
			Kind:         "openai-compatible",
			AuthMethod:   "api_key",
			BaseURL:      "https://api.openai.com/v1",
			APIKey:       "secret",
			DefaultModel: "gpt-5.4",
		},
	}
	m := App{
		cfg:             cfg,
		composer:        textarea.New(),
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
	}
	m.composer.SetValue("hello")
	m.composer.SetCursor(len("hello"))

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter, Alt: true})
	next := updated.(*App)
	if cmd != nil {
		t.Fatal("expected no send command for modified enter")
	}
	if next.loading {
		t.Fatal("expected modified enter not to start loading")
	}
	if next.composer.Value() != "hello\n" {
		t.Fatalf("expected newline inserted, got %q", next.composer.Value())
	}
	if len(next.currentSnapshot.Messages) != 0 {
		t.Fatalf("expected no optimistic transcript append, got %#v", next.currentSnapshot.Messages)
	}
}

func TestCtrlVPastesClipboardText(t *testing.T) {
	m := App{
		composer:           textarea.New(),
		attachmentFiles:    attachment.NewManager(t.TempDir()),
		readClipboardImage: func() ([]byte, error) { return nil, nil },
		readClipboardText:  func() (string, error) { return "pasted text", nil },
	}
	m.composer.SetValue("hello ")
	m.composer.SetCursor(len("hello "))

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyCtrlV})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected title sync command after paste")
	}
	if got := next.composer.Value(); got != "hello pasted text" {
		t.Fatalf("unexpected pasted composer value: %q", got)
	}
	if next.status != "Pasted from clipboard" {
		t.Fatalf("unexpected paste status: %q", next.status)
	}
}

func TestCtrlVPastesClipboardImageAsAttachment(t *testing.T) {
	m := App{
		composer:          textarea.New(),
		attachmentFiles:   attachment.NewManager(t.TempDir()),
		readClipboardText: func() (string, error) { return "", nil },
		readClipboardImage: func() ([]byte, error) {
			return []byte("\x89PNG\r\n\x1a\nfake"), nil
		},
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyCtrlV})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected title sync command after image attach")
	}
	if len(next.draftAttachments) != 1 {
		t.Fatalf("expected one draft attachment, got %#v", next.draftAttachments)
	}
	if got := next.composer.Value(); got != "[clipboard image #1] " {
		t.Fatalf("expected inline image placeholder, got %q", got)
	}
	if !strings.Contains(next.status, "Attached image") {
		t.Fatalf("unexpected attach status: %q", next.status)
	}
}

func TestCtrlVPastesClipboardImageWarnsWhenModelDoesNotSupportImages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"image content part unsupported for this model"}`))
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.DefaultProvider = "openai-compatible"
	cfg.DefaultModel = "text-only-model"
	cfg.Providers = map[string]config.Provider{
		"openai-compatible": {Kind: provider.ProviderKindCompatible, BaseURL: server.URL + "/v1", DefaultModel: "text-only-model"},
	}
	m := App{
		cfg:               cfg,
		composer:          textarea.New(),
		attachmentFiles:   attachment.NewManager(t.TempDir()),
		readClipboardText: func() (string, error) { return "", nil },
		readClipboardImage: func() ([]byte, error) {
			return []byte("\x89PNG\r\n\x1a\nfake"), nil
		},
		caps: provider.NewCapabilityStore(t.TempDir()),
		currentSession: domain.Session{
			ProviderID: "openai-compatible",
			ModelID:    "text-only-model",
		},
	}

	updated, _ := m.handleKey(ui.KeyMsg{Type: ui.KeyCtrlV})
	next := updated.(*App)
	if !strings.Contains(next.status, "warning: text-only-model may not support image inputs") {
		t.Fatalf("expected unsupported image warning, got %q", next.status)
	}
}

func TestAttachmentTokenBackspaceRemovesWholeToken(t *testing.T) {
	m := App{
		composer:        textarea.New(),
		attachmentFiles: attachment.NewManager(t.TempDir()),
		draftAttachments: []attachment.Draft{{
			Metadata: attachment.Metadata{Name: "clipboard.png", MIME: "image/png", Path: filepath.Join(t.TempDir(), "clipboard.png")},
			Token:    "[clipboard image #1]",
		}},
	}
	m.setComposerDraftValue("analyze this")
	m.composer.SetCursor(len("[clipboard"))

	updated, _ := m.handleKey(ui.KeyMsg{Type: ui.KeyBackspace})
	next := updated.(*App)
	if got := next.composer.Value(); got != "analyze this" {
		t.Fatalf("expected attachment token to be removed atomically, got %q", got)
	}
	if len(next.draftAttachments) != 0 {
		t.Fatalf("expected attachment removed with token, got %#v", next.draftAttachments)
	}
}

func TestCtrlVPastesAttachmentFilePath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "note.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := App{
		composer:          textarea.New(),
		attachmentFiles:   attachment.NewManager(root),
		readClipboardText: func() (string, error) { return path, nil },
		readClipboardImage: func() ([]byte, error) {
			return nil, nil
		},
	}

	updated, _ := m.handleKey(ui.KeyMsg{Type: ui.KeyCtrlV})
	next := updated.(*App)
	if len(next.draftAttachments) != 1 {
		t.Fatalf("expected one draft attachment, got %#v", next.draftAttachments)
	}
	if next.draftAttachments[0].Name != "note.txt" {
		t.Fatalf("unexpected attachment name: %#v", next.draftAttachments[0])
	}
	if got := next.composer.Value(); got != "[file #1] " {
		t.Fatalf("expected inline file placeholder, got %q", got)
	}
}

func TestBackspaceRemovesLastDraftAttachmentWhenComposerEmpty(t *testing.T) {
	root := t.TempDir()
	m := App{
		composer:        textarea.New(),
		attachmentFiles: attachment.NewManager(root),
		draftAttachments: []attachment.Draft{{
			Metadata: attachment.Metadata{Name: "note.txt", MIME: "text/plain", Path: filepath.Join(root, "note.txt")},
			Token:    "[file #1]",
		}},
	}
	m.setComposerDraftValue("")
	m.composer.SetCursor(len("[file #1]"))

	updated, _ := m.handleKey(ui.KeyMsg{Type: ui.KeyBackspace})
	next := updated.(*App)
	if len(next.draftAttachments) != 0 {
		t.Fatalf("expected attachment to be removed, got %#v", next.draftAttachments)
	}
}

func TestBackspaceAtComposerStartRemovesLastDraftAttachment(t *testing.T) {
	root := t.TempDir()
	m := App{
		composer:        textarea.New(),
		attachmentFiles: attachment.NewManager(root),
		draftAttachments: []attachment.Draft{{
			Metadata: attachment.Metadata{Name: "clipboard.png", MIME: "image/png", Path: filepath.Join(root, "clipboard.png")},
			Token:    "[clipboard image #1]",
		}},
	}
	m.setComposerDraftValue("analyze this image")
	m.composer.SetCursor(len("[clipboard image #1]"))

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyBackspace})
	next := updated.(*App)
	_ = cmd
	if len(next.draftAttachments) != 0 {
		t.Fatalf("expected attachment removed, got %#v", next.draftAttachments)
	}
	if got := next.composer.Value(); got != "analyze this image" {
		t.Fatalf("expected composer text unchanged, got %q", got)
	}
}

func TestDeleteAtComposerEndRemovesLastDraftAttachment(t *testing.T) {
	root := t.TempDir()
	m := App{
		composer:        textarea.New(),
		attachmentFiles: attachment.NewManager(root),
		draftAttachments: []attachment.Draft{{
			Metadata: attachment.Metadata{Name: "clipboard.png", MIME: "image/png", Path: filepath.Join(root, "clipboard.png")},
			Token:    "[clipboard image #1]",
		}},
	}
	m.setComposerDraftValue("analyze this image")
	m.composer.SetCursor(0)

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyDelete})
	next := updated.(*App)
	_ = cmd
	if len(next.draftAttachments) != 0 {
		t.Fatalf("expected attachment removed, got %#v", next.draftAttachments)
	}
	if got := next.composer.Value(); got != "analyze this image" {
		t.Fatalf("expected composer text unchanged, got %q", got)
	}
}

func TestRenderComposerShowsDraftAttachmentInsideComposer(t *testing.T) {
	root := t.TempDir()
	composer := textarea.New()
	composer.Placeholder = "Ask koder or type / for commands"
	composer.SetHeight(composerInputHeight)
	composer.SetWidth(38)
	composer.Focus()

	m := App{
		width:    40,
		palette:  theme.Default().Palette,
		composer: composer,
		draftAttachments: []attachment.Draft{{
			Metadata: attachment.Metadata{Name: "clipboard.png", MIME: "image/png", Path: filepath.Join(root, "clipboard.png")},
		}},
	}
	m.setComposerDraftValue("")

	got := ansi.Strip(m.renderComposer())
	if !strings.Contains(got, "[clipboard image #1]") {
		t.Fatalf("expected attachment label inside composer, got %q", got)
	}
}

func TestEnterSendsPromptWithPastedImageAttachment(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(t)
	cfg.DefaultProvider = "openai"
	cfg.DefaultModel = "gpt-5.4"
	cfg.Providers = map[string]config.Provider{
		"openai": {BaseURL: "https://api.openai.com/v1", APIKey: "secret", DefaultModel: "gpt-5.4"},
	}
	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.attachmentFiles = attachment.NewManager(root)
	m.readClipboardImage = func() ([]byte, error) {
		return []byte("\x89PNG\r\n\x1a\nfake"), nil
	}
	m.readClipboardText = func() (string, error) { return "", nil }

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyCtrlV})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected title sync command after image attach")
	}
	if len(next.draftAttachments) != 1 {
		t.Fatalf("expected one draft attachment, got %#v", next.draftAttachments)
	}
	if got := next.composer.Value(); got != "[clipboard image #1] " {
		t.Fatalf("expected inline image placeholder after paste, got %q", got)
	}

	next.composer.SetValue("analyze this image")
	updated, cmd = next.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	final := updated.(*App)
	if cmd == nil {
		t.Fatal("expected prompt kickoff command after enter")
	}
	if !final.loading {
		t.Fatal("expected enter to start loading")
	}
	if got := final.composer.Value(); got != "" {
		t.Fatalf("expected composer reset after sending, got %q", got)
	}
	if len(final.draftAttachments) != 0 {
		t.Fatalf("expected draft attachments cleared after sending, got %#v", final.draftAttachments)
	}
	if len(final.currentSnapshot.Messages) == 0 {
		t.Fatal("expected optimistic user message")
	}
	last := final.currentSnapshot.Messages[len(final.currentSnapshot.Messages)-1]
	if last.Role != domain.MessageRoleUser {
		t.Fatalf("expected user message, got %#v", last)
	}
	if parts := final.currentSnapshot.Parts[last.ID]; len(parts) == 0 {
		t.Fatal("expected message parts for optimistic user prompt")
	}
}

func TestSubmissionPromptTextStripsAttachmentPlaceholders(t *testing.T) {
	root := t.TempDir()
	m := App{
		composer: textarea.New(),
		draftAttachments: []attachment.Draft{{
			Metadata: attachment.Metadata{Name: "clipboard.png", MIME: "image/png", Path: filepath.Join(root, "clipboard.png")},
		}},
	}
	m.draftAttachments[0].Token = "[clipboard image #1]"
	m.composer.SetValue("[clipboard image #1] analyze this image")
	if got := m.submissionPromptText(); got != "analyze this image" {
		t.Fatalf("submissionPromptText() = %q, want %q", got, "analyze this image")
	}
}

func TestForkSessionCopiesAttachmentFiles(t *testing.T) {
	root := t.TempDir()
	st, err := store.OpenWithOptions(root, store.Options{Backend: store.BackendPebble})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	manager := attachment.NewManager(root)
	cfg := testConfig(t)
	cfg.DefaultProvider = "openai"
	cfg.DefaultModel = "gpt-5.4"
	cfg.Providers = map[string]config.Provider{
		"openai": {BaseURL: "https://api.openai.com/v1", APIKey: "secret", DefaultModel: "gpt-5.4"},
	}
	m := App{
		cfg:             cfg,
		store:           st,
		attachmentFiles: manager,
		workdir:         root,
	}

	session, err := st.CreateSession(context.Background(), "test", "openai", "gpt-5.4", nil)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := st.AddMessage(context.Background(), session.ID, domain.MessageRoleUser, "hello")
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(root, "note.txt")
	if err := os.WriteFile(src, []byte("fork me"), 0o644); err != nil {
		t.Fatal(err)
	}
	draft, err := manager.ImportFile(src)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := manager.AdoptDraft(draft, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddPart(context.Background(), msg.ID, domain.AttachmentPayload{ID: meta.ID, Name: meta.Name, MIME: meta.MIME, Path: meta.Path, Size: meta.Size, Source: meta.Source, Original: meta.Original}); err != nil {
		t.Fatal(err)
	}

	msgAny := m.forkSessionCmd(session.ID)()
	forked, ok := msgAny.(forkSessionMsg)
	if !ok {
		t.Fatalf("unexpected fork message: %#v", msgAny)
	}
	if forked.err != nil {
		t.Fatal(forked.err)
	}
	if forked.forkedID == session.ID {
		t.Fatal("expected distinct forked session id")
	}
	forkParts := forked.load.parts[forked.load.messages[0].ID]
	if len(forkParts) != 1 {
		t.Fatalf("unexpected forked parts: %#v", forkParts)
	}
	forkMeta := forkParts[0].Payload.(domain.AttachmentPayload)
	if forkMeta.Path == meta.Path {
		t.Fatalf("expected copied attachment path, got %q", forkMeta.Path)
	}
	if _, err := os.Stat(forkMeta.Path); err != nil {
		t.Fatalf("expected copied attachment file: %v", err)
	}
}

func TestCtrlYCopiesLatestAssistantMessage(t *testing.T) {
	var copied string
	m := App{
		composer: textarea.New(),
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{
			2: {{Kind: domain.PartKindText, Body: "latest assistant reply"}},
		}, Messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleUser, Summary: "hello"},
			{ID: 2, Role: domain.MessageRoleAssistant, Summary: "latest assistant reply"},
		}},
		writeClipboardText: func(text string) error {
			copied = text
			return nil
		},
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyCtrlY})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected title sync command after copy")
	}
	if copied != "latest assistant reply" {
		t.Fatalf("unexpected copied text: %q", copied)
	}
	if next.status != "Copied last assistant message" {
		t.Fatalf("unexpected copy status: %q", next.status)
	}
}

func TestEnterWhileBusyQueuesSteeringPrompt(t *testing.T) {
	cfg := testConfig(t)
	cfg.DefaultProvider = "openai"
	cfg.DefaultModel = "gpt-5.4"
	cfg.Providers = map[string]config.Provider{
		"openai": {
			Kind:         "openai-compatible",
			AuthMethod:   "api_key",
			BaseURL:      "https://api.openai.com/v1",
			APIKey:       "secret",
			DefaultModel: "gpt-5.4",
		},
	}
	m := App{
		cfg:             cfg,
		composer:        textarea.New(),
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
		loading:         true,
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
		},
	}
	m.composer.SetValue("follow up")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected title sync command after queueing")
	}
	if len(next.currentChat.QueuedInputs) != 1 || next.currentChat.QueuedInputs[0].Text != "follow up" || next.currentChat.QueuedInputs[0].Kind != domain.QueuedInputKindSteer {
		t.Fatalf("expected steering input, got %#v", next.currentChat.QueuedInputs)
	}
	if next.composer.Value() != "" {
		t.Fatalf("expected composer reset after queueing, got %q", next.composer.Value())
	}
	if len(next.currentSnapshot.Messages) != 0 {
		t.Fatalf("expected no optimistic send while busy, got %#v", next.currentSnapshot.Messages)
	}
}

func TestParseBangPrompt(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   bangPrompt
		wantOK bool
	}{
		{name: "single", input: "!ls -l", want: bangPrompt{Command: "ls -l"}, wantOK: true},
		{name: "double", input: "  !!pwd  ", want: bangPrompt{Double: true, Command: "pwd"}, wantOK: true},
		{name: "empty single", input: "!", want: bangPrompt{}, wantOK: true},
		{name: "empty double", input: "!!", want: bangPrompt{Double: true}, wantOK: true},
		{name: "not bang", input: "echo !ls", want: bangPrompt{}, wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseBangPrompt(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("unexpected ok=%v want %v", ok, tt.wantOK)
			}
			if got != tt.want {
				t.Fatalf("unexpected bang prompt %#v want %#v", got, tt.want)
			}
		})
	}
}

func TestFormatBangFollowupPrompt(t *testing.T) {
	got := formatBangFollowupPrompt("ls", tools.Result{
		Output: "",
		Meta:   map[string]string{"exit_code": "7"},
	})
	if !strings.Contains(got, "```bash\nls\n```") {
		t.Fatalf("expected command block, got %q", got)
	}
	if !strings.Contains(got, "Exit code: 7") {
		t.Fatalf("expected exit code, got %q", got)
	}
	if !strings.Contains(got, "```text\n(no output)\n```") {
		t.Fatalf("expected explicit empty output placeholder, got %q", got)
	}
}

func TestBangPromptWithoutProviderRunsShellOnly(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m := App{
		cfg:             testConfig(t),
		store:           st,
		composer:        textarea.New(),
		workdir:         t.TempDir(),
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
	}
	m.composer.SetValue("!printf hi")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected shell command execution")
	}
	if !next.loading {
		t.Fatal("expected busy state while shell command runs")
	}
	if next.composer.Value() != "" {
		t.Fatalf("expected composer reset, got %q", next.composer.Value())
	}

	msgAny := cmd()
	bangMsg, ok := msgAny.(bangCommandMsg)
	if !ok {
		t.Fatalf("expected bangCommandMsg, got %T", msgAny)
	}
	if bangMsg.err != nil {
		t.Fatal(bangMsg.err)
	}
	if bangMsg.followupMode != bangFollowupNone {
		t.Fatalf("expected no llm follow-up, got %v", bangMsg.followupMode)
	}

	updated, cmd = next.Update(bangMsg)
	done := updated.(App)
	if cmd == nil {
		t.Fatal("expected transcript reload command")
	}
	if done.loading {
		t.Fatal("expected shell-only bang command to clear busy state")
	}
	if done.currentSession.ID == 0 || done.currentChat.ID == 0 {
		t.Fatalf("expected draft session/chat to be created, got session=%d chat=%d", done.currentSession.ID, done.currentChat.ID)
	}
	if len(done.currentSnapshot.Messages) != 0 {
		t.Fatalf("expected no synthetic user message, got %#v", done.currentSnapshot.Messages)
	}

	messages, parts, err := st.PartsForChat(context.Background(), done.currentChat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected one persisted tool message, got %d", len(messages))
	}
	if messages[0].Role != domain.MessageRoleTool {
		t.Fatalf("expected tool message role, got %q", messages[0].Role)
	}
	partList := parts[messages[0].ID]
	if len(partList) == 0 {
		t.Fatal("expected persisted tool parts")
	}
	foundOutput := false
	for _, part := range partList {
		if part.Kind == domain.PartKindToolOutput && strings.TrimSpace(part.Body) == "hi" {
			foundOutput = true
		}
	}
	if !foundOutput {
		t.Fatalf("expected bash output part, got %#v", partList)
	}
}

func TestDoubleBangWithoutProviderOpensConnectDialogBeforeRunningShell(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blocked.txt")
	m := App{
		cfg:      testConfig(t),
		composer: textarea.New(),
	}
	m.composer.SetValue("!!touch " + path)

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected title sync command when opening connect dialog")
	}
	if !next.hasConnectDialog() {
		t.Fatal("expected connect dialog to open")
	}
	if next.composer.Value() != "!!touch "+path {
		t.Fatalf("expected composer to remain intact, got %q", next.composer.Value())
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected shell command not to run, stat err=%v", err)
	}
}

func TestDoubleBangIdleCreatesSynthesizedPrompt(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	cfg := testConfig(t)
	cfg.DefaultProvider = "openai"
	cfg.DefaultModel = "gpt-5.4"
	cfg.Providers = map[string]config.Provider{
		"openai": {
			BaseURL:      "https://api.openai.com/v1",
			APIKey:       "secret",
			DefaultModel: "gpt-5.4",
		},
	}
	m := App{
		cfg:             cfg,
		store:           st,
		composer:        textarea.New(),
		workdir:         t.TempDir(),
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
	}
	m.composer.SetValue("!!printf hi")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected shell command execution")
	}

	msgAny := cmd()
	bangMsg, ok := msgAny.(bangCommandMsg)
	if !ok {
		t.Fatalf("expected bangCommandMsg, got %T", msgAny)
	}
	if bangMsg.err != nil {
		t.Fatal(bangMsg.err)
	}
	if bangMsg.followupMode != bangFollowupPrompt {
		t.Fatalf("expected immediate llm follow-up, got %v", bangMsg.followupMode)
	}
	if !strings.Contains(bangMsg.followupPrompt, "```bash\nprintf hi\n```") || !strings.Contains(bangMsg.followupPrompt, "hi") {
		t.Fatalf("expected synthesized prompt to include command and output, got %q", bangMsg.followupPrompt)
	}

	updated, cmd = next.Update(bangMsg)
	done := updated.(App)
	if cmd == nil {
		t.Fatal("expected batched reload and prompt kickoff")
	}
	if len(done.currentSnapshot.Messages) != 1 || done.currentSnapshot.Messages[0].Role != domain.MessageRoleUser {
		t.Fatalf("expected optimistic user prompt, got %#v", done.currentSnapshot.Messages)
	}
	if got := done.currentSnapshot.Messages[0].Summary; !strings.Contains(got, "User-requested shell command:") || !strings.Contains(got, "printf hi") {
		t.Fatalf("unexpected optimistic summary %q", got)
	}

	msgBatch, ok := cmd().(ui.BatchMsg)
	if !ok {
		t.Fatalf("expected batched commands, got %T", cmd())
	}
	if len(msgBatch) < 2 {
		t.Fatalf("expected reload and prompt commands, got %d batched commands", len(msgBatch))
	}
}

func TestDoubleBangWhileBusyEnterQueuesSteeringPrompt(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	cfg := testConfig(t)
	cfg.DefaultProvider = "openai"
	cfg.DefaultModel = "gpt-5.4"
	cfg.Providers = map[string]config.Provider{
		"openai": {
			BaseURL:      "https://api.openai.com/v1",
			APIKey:       "secret",
			DefaultModel: "gpt-5.4",
		},
	}
	m := App{
		cfg:             cfg,
		store:           st,
		composer:        textarea.New(),
		workdir:         t.TempDir(),
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
		loading:         true,
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
		},
	}
	m.composer.SetValue("!!printf hi")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected shell command execution while busy")
	}
	msgAny := cmd()
	bangMsg, ok := msgAny.(bangCommandMsg)
	if !ok {
		t.Fatalf("expected bangCommandMsg, got %T", msgAny)
	}
	if bangMsg.followupMode != bangFollowupSteer {
		t.Fatalf("expected steer follow-up, got %v", bangMsg.followupMode)
	}

	updated, cmd = next.Update(bangMsg)
	done := updated.(App)
	if cmd == nil {
		t.Fatal("expected queue persistence command")
	}
	if !done.loading {
		t.Fatal("expected active model turn to remain busy")
	}
	if len(done.currentChat.QueuedInputs) != 1 {
		t.Fatalf("expected one queued follow-up, got %#v", done.currentChat.QueuedInputs)
	}
	item := done.currentChat.QueuedInputs[0]
	if item.Kind != domain.QueuedInputKindSteer {
		t.Fatalf("expected steering follow-up kind, got %v", item.Kind)
	}
	if !strings.Contains(item.Text, "User-requested shell command:") || !strings.Contains(item.Text, "printf hi") {
		t.Fatalf("expected synthesized queued prompt, got %q", item.Text)
	}
}

func TestDoubleBangWhileBusyTabQueuesSynthesizedPrompt(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	cfg := testConfig(t)
	cfg.DefaultProvider = "openai"
	cfg.DefaultModel = "gpt-5.4"
	cfg.Providers = map[string]config.Provider{
		"openai": {
			BaseURL:      "https://api.openai.com/v1",
			APIKey:       "secret",
			DefaultModel: "gpt-5.4",
		},
	}
	m := App{
		cfg:             cfg,
		store:           st,
		composer:        textarea.New(),
		workdir:         t.TempDir(),
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
		loading:         true,
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
		},
	}
	m.composer.SetValue("!!printf hi")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyTab})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected shell command execution while busy")
	}
	msgAny := cmd()
	bangMsg, ok := msgAny.(bangCommandMsg)
	if !ok {
		t.Fatalf("expected bangCommandMsg, got %T", msgAny)
	}
	if bangMsg.followupMode != bangFollowupQueue {
		t.Fatalf("expected queued follow-up, got %v", bangMsg.followupMode)
	}

	updated, cmd = next.Update(bangMsg)
	done := updated.(App)
	if cmd == nil {
		t.Fatal("expected queue persistence command")
	}
	if len(done.currentChat.QueuedInputs) != 1 {
		t.Fatalf("expected one queued follow-up, got %#v", done.currentChat.QueuedInputs)
	}
	if done.currentChat.QueuedInputs[0].Kind != domain.QueuedInputKindQueued {
		t.Fatalf("expected queued follow-up kind, got %v", done.currentChat.QueuedInputs[0].Kind)
	}
}

func TestUpDownBrowseComposerPromptHistory(t *testing.T) {
	m := App{
		composer: textarea.New(),
		currentSnapshot: chatpkg.Snapshot{Messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleUser, Summary: "first"},
			{ID: 2, Role: domain.MessageRoleAssistant, Summary: "reply"},
			{ID: 3, Role: domain.MessageRoleUser, Summary: "second"},
		}, Parts: map[int64][]domain.Part{}},
	}
	m.composer.SetValue("draft")

	updated, _ := m.handleKey(ui.KeyMsg{Type: ui.KeyUp})
	next := updated.(*App)
	if got := next.composer.Value(); got != "second" {
		t.Fatalf("expected newest history entry, got %q", got)
	}

	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyUp})
	next = updated.(*App)
	if got := next.composer.Value(); got != "first" {
		t.Fatalf("expected older history entry, got %q", got)
	}

	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyDown})
	next = updated.(*App)
	if got := next.composer.Value(); got != "second" {
		t.Fatalf("expected newer history entry, got %q", got)
	}

	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyDown})
	next = updated.(*App)
	if got := next.composer.Value(); got != "draft" {
		t.Fatalf("expected draft restored after newest history entry, got %q", got)
	}
}

func TestCtrlROpensComposerHistoryMenuAndAcceptsSelection(t *testing.T) {
	m := App{
		composer: textarea.New(),
		currentSnapshot: chatpkg.Snapshot{Messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleUser, Summary: "alpha one"},
			{ID: 2, Role: domain.MessageRoleUser, Summary: "beta two"},
			{ID: 3, Role: domain.MessageRoleUser, Summary: "alpha three"},
		}, Parts: map[int64][]domain.Part{}},
	}
	m.composer.SetValue("alpha")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyCtrlR})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected title sync command after opening history search")
	}
	if !next.hasComposerHistoryMenu() {
		t.Fatal("expected composer history menu to open")
	}
	if got := next.composer.Value(); got != "alpha" {
		t.Fatalf("expected draft to remain in composer while searching, got %q", got)
	}
	if got := next.renderFooter(); !strings.Contains(got, "History") || !strings.Contains(got, "alpha three") {
		t.Fatalf("expected history menu in footer, got %q", got)
	}

	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyCtrlR})
	next = updated.(*App)
	if got := next.filteredComposerHistory(next.composerHistory.SearchQuery)[next.composerHistory.SearchIndex]; got != "alpha one" {
		t.Fatalf("expected ctrl-r to move to earlier matching history entry, got %q", got)
	}

	updated, cmd = next.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next = updated.(*App)
	if cmd == nil {
		t.Fatal("expected title sync command after accepting history selection")
	}
	if next.hasComposerHistoryMenu() {
		t.Fatal("expected history menu to close after selection")
	}
	if got := next.composer.Value(); got != "alpha one" {
		t.Fatalf("expected selected history entry in composer, got %q", got)
	}
}

func TestComposerHistoryMenuFiltersWithoutMutatingComposer(t *testing.T) {
	m := App{
		composer: textarea.New(),
		currentSnapshot: chatpkg.Snapshot{Messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleUser, Summary: "first deploy"},
			{ID: 2, Role: domain.MessageRoleUser, Summary: "second status"},
		}, Parts: map[int64][]domain.Part{}},
	}
	m.composer.SetValue("")

	updated, _ := m.handleKey(ui.KeyMsg{Type: ui.KeyCtrlR})
	next := updated.(*App)
	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Runes: []rune("status")})
	next = updated.(*App)

	if !next.hasComposerHistoryMenu() {
		t.Fatal("expected history menu to remain open while filtering")
	}
	if got := next.composer.Value(); got != "" {
		t.Fatalf("expected composer draft to remain unchanged while filtering, got %q", got)
	}
	matches := next.filteredComposerHistory(next.composerHistory.SearchQuery)
	if len(matches) != 1 || matches[0] != "second status" {
		t.Fatalf("expected filtered history match, got %#v", matches)
	}
}

func TestTabWhileBusyQueuesPrompt(t *testing.T) {
	cfg := testConfig(t)
	cfg.DefaultProvider = "openai"
	cfg.DefaultModel = "gpt-5.4"
	cfg.Providers = map[string]config.Provider{
		"openai": {
			Kind:         "openai-compatible",
			AuthMethod:   "api_key",
			BaseURL:      "https://api.openai.com/v1",
			APIKey:       "secret",
			DefaultModel: "gpt-5.4",
		},
	}
	m := App{
		cfg:      cfg,
		composer: textarea.New(),
		loading:  true,
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
		},
	}
	m.composer.SetValue("nudge the plan")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyTab})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected title sync command after queueing")
	}
	if len(next.currentChat.QueuedInputs) != 1 || next.currentChat.QueuedInputs[0].Kind != domain.QueuedInputKindQueued {
		t.Fatalf("expected queued input, got %#v", next.currentChat.QueuedInputs)
	}
}

func TestCtrlGQueuesContinueWhileBusy(t *testing.T) {
	cfg := testConfig(t)
	cfg.DefaultProvider = "openai"
	cfg.DefaultModel = "gpt-5.4"
	cfg.Providers = map[string]config.Provider{
		"openai": {
			Kind:         "openai-compatible",
			AuthMethod:   "api_key",
			BaseURL:      "https://api.openai.com/v1",
			APIKey:       "secret",
			DefaultModel: "gpt-5.4",
		},
	}
	m := App{
		cfg:            cfg,
		composer:       textarea.New(),
		loading:        true,
		currentSession: domain.Session{ID: 9, ProviderID: "openai", ModelID: "gpt-5.4"},
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
		},
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyCtrlG})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected title sync command after queueing continue")
	}
	if len(next.currentChat.QueuedInputs) != 1 || next.currentChat.QueuedInputs[0].Kind != domain.QueuedInputKindContinue {
		t.Fatalf("expected queued continue, got %#v", next.currentChat.QueuedInputs)
	}
}

func TestCtrlGStartsContinueWhenIdle(t *testing.T) {
	cfg := testConfig(t)
	cfg.DefaultProvider = "openai"
	cfg.DefaultModel = "gpt-5.4"
	cfg.Providers = map[string]config.Provider{
		"openai": {
			Kind:         "openai-compatible",
			AuthMethod:   "api_key",
			BaseURL:      "https://api.openai.com/v1",
			APIKey:       "secret",
			DefaultModel: "gpt-5.4",
		},
	}
	m := App{
		cfg:            cfg,
		composer:       textarea.New(),
		currentSession: domain.Session{ID: 9, ProviderID: "openai", ModelID: "gpt-5.4"},
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyCtrlG})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected continue command")
	}
	if !next.loading {
		t.Fatal("expected loading after continue hotkey")
	}
}

func TestLoadMsgDispatchesQueuedPrompt(t *testing.T) {
	cfg := testConfig(t)
	cfg.DefaultProvider = "openai"
	cfg.DefaultModel = "gpt-5.4"
	cfg.Providers = map[string]config.Provider{
		"openai": {
			Kind:         "openai-compatible",
			AuthMethod:   "api_key",
			BaseURL:      "https://api.openai.com/v1",
			APIKey:       "secret",
			DefaultModel: "gpt-5.4",
		},
	}
	m := App{
		cfg:             cfg,
		composer:        textarea.New(),
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
		viewport:        newTranscriptViewport(40, 6),
		currentSession:  domain.Session{ID: 9, ProviderID: "openai", ModelID: "gpt-5.4", Title: "Queued"},
		currentChat:     domain.Chat{QueuedInputs: []domain.QueuedInput{{ID: 1, Text: "queued ask", Kind: domain.QueuedInputKindQueued}}},
	}

	updated, cmd := m.Update(loadMsg{
		current: domain.Session{ID: 9, ProviderID: "openai", ModelID: "gpt-5.4", Title: "Queued"},
		parts:   map[int64][]domain.Part{},
	})
	next := updated.(App)
	if cmd == nil {
		t.Fatal("expected queued input dispatch command")
	}
	if !next.loading {
		t.Fatal("expected queued input dispatch to restart loading")
	}
	if len(next.currentSnapshot.Messages) != 1 || next.currentSnapshot.Messages[0].Summary != "queued ask" {
		t.Fatalf("expected optimistic queued message, got %#v", next.currentSnapshot.Messages)
	}
	if len(next.currentChat.QueuedInputs) != 0 {
		t.Fatalf("expected queued input cleared, got %#v", next.currentChat.QueuedInputs)
	}
}

func TestQueueEditEnterRestoresQueuedPromptToComposer(t *testing.T) {
	m := App{
		cfg:         testConfig(t),
		composer:    textarea.New(),
		currentChat: domain.Chat{QueuedInputs: []domain.QueuedInput{{ID: 1, Text: "queued ask", Kind: domain.QueuedInputKindQueued}}},
	}
	m.queueEditMode = true

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*App)

	_ = cmd
	if len(next.currentChat.QueuedInputs) != 0 {
		t.Fatalf("expected queued prompt cleared, got %#v", next.currentChat.QueuedInputs)
	}
	if next.composer.Value() != "queued ask" {
		t.Fatalf("expected composer to contain restored queued prompt, got %q", next.composer.Value())
	}
}

func TestAltUpRestoresQueuedPromptToComposer(t *testing.T) {
	m := App{
		cfg:      testConfig(t),
		palette:  theme.Default().Palette,
		viewport: newTranscriptViewport(80, 20),
		composer: textarea.New(),
		width:    80,
		height:   24,
		currentChat: domain.Chat{QueuedInputs: []domain.QueuedInput{
			{ID: 1, Text: "queued ask", Kind: domain.QueuedInputKindQueued},
		}},
	}

	_ = m.ViewLines()
	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyUp, Alt: true})
	next := updated.(*App)

	_ = cmd
	if len(next.currentChat.QueuedInputs) != 0 {
		t.Fatalf("expected queued prompt cleared, got %#v", next.currentChat.QueuedInputs)
	}
	if next.composer.Value() != "queued ask" {
		t.Fatalf("expected composer to contain restored queued prompt, got %q", next.composer.Value())
	}
	if got := strings.Join(next.ViewLines(), "\n"); !strings.Contains(got, "queued ask") {
		t.Fatalf("expected rendered composer to contain restored queued prompt, got %q", got)
	}
}

func TestQueueEditAltUpRestoresSelectedQueuedPromptToComposer(t *testing.T) {
	m := App{
		cfg:      testConfig(t),
		palette:  theme.Default().Palette,
		viewport: newTranscriptViewport(80, 20),
		composer: textarea.New(),
		width:    80,
		height:   24,
		currentChat: domain.Chat{QueuedInputs: []domain.QueuedInput{
			{ID: 1, Text: "first", Kind: domain.QueuedInputKindQueued},
			{ID: 2, Text: "second", Kind: domain.QueuedInputKindSteer},
		}},
	}
	m.queueEditMode = true
	m.queueSelection = 1
	m.setComposerValue("current draft")

	_ = m.ViewLines()
	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyUp, Alt: true})
	next := updated.(*App)

	_ = cmd
	if next.composer.Value() != "second" {
		t.Fatalf("expected composer to contain selected queued prompt, got %q", next.composer.Value())
	}
	if len(next.currentChat.QueuedInputs) != 2 {
		t.Fatalf("expected current draft to replace selected queue item, got %#v", next.currentChat.QueuedInputs)
	}
	if next.currentChat.QueuedInputs[0].Text != "first" || next.currentChat.QueuedInputs[1].Text != "current draft" {
		t.Fatalf("expected selected queued prompt replaced with current draft, got %#v", next.currentChat.QueuedInputs)
	}
	if got := strings.Join(next.ViewLines(), "\n"); !strings.Contains(got, "second") {
		t.Fatalf("expected rendered composer to contain selected queued prompt, got %q", got)
	}
}

func TestQueueEditEnterSwapsQueuedPromptWithExistingDraft(t *testing.T) {
	m := App{
		cfg:         testConfig(t),
		composer:    textarea.New(),
		currentChat: domain.Chat{QueuedInputs: []domain.QueuedInput{{ID: 1, Text: "queued ask", Kind: domain.QueuedInputKindSteer}}},
	}
	m.queueEditMode = true
	m.setComposerValue("current draft")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*App)

	_ = cmd
	if next.composer.Value() != "queued ask" {
		t.Fatalf("expected composer to contain restored queued prompt, got %q", next.composer.Value())
	}
	if len(next.currentChat.QueuedInputs) != 1 {
		t.Fatal("expected previous draft to be re-queued")
	}
	if next.currentChat.QueuedInputs[0].Text != "current draft" {
		t.Fatalf("expected current draft to be re-queued, got %#v", next.currentChat.QueuedInputs)
	}
	if next.currentChat.QueuedInputs[0].Kind != domain.QueuedInputKindQueued {
		t.Fatalf("expected swapped draft to be queued as normal follow-up, got %#v", next.currentChat.QueuedInputs)
	}
}

func TestQueueEditEnterClearsQueuedContinue(t *testing.T) {
	m := App{
		cfg:         testConfig(t),
		composer:    textarea.New(),
		currentChat: domain.Chat{QueuedInputs: []domain.QueuedInput{{ID: 1, Kind: domain.QueuedInputKindContinue}}},
	}
	m.queueEditMode = true
	m.setComposerValue("keep draft")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*App)

	_ = cmd
	if len(next.currentChat.QueuedInputs) != 0 {
		t.Fatalf("expected queued continue cleared, got %#v", next.currentChat.QueuedInputs)
	}
	if next.composer.Value() != "keep draft" {
		t.Fatalf("expected composer draft unchanged, got %q", next.composer.Value())
	}
}

func TestWindowTitleUsesSessionTitle(t *testing.T) {
	m := App{
		cfg:            config.Default(),
		currentSession: domain.Session{ID: 7, Title: "Helpful Session Title"},
	}
	got := m.windowTitle()
	if got != "K Helpful Session Title" {
		t.Fatalf("unexpected window title: %q", got)
	}
}

func TestWindowTitleUsesAnimatedSpinnerFrame(t *testing.T) {
	cfg := testConfig(t)
	cfg.UI.Spinner = "circles"
	m := App{
		cfg: cfg,
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
			spinner: spinnerModel{
				active: true,
				frame:  2,
			},
		},
		currentSession: domain.Session{Title: "Build fixes"},
	}
	got := m.windowTitle()
	if !strings.HasPrefix(got, "◑K ") {
		t.Fatalf("unexpected animated window title: %q", got)
	}
}

func TestSyncDebugRuntimeIncludesViewportState(t *testing.T) {
	rec := debugsrv.NewRecorder()
	m := App{
		debug:           rec,
		status:          "Ready",
		currentSession:  domain.Session{ID: 7, Title: "Debug Session", ProviderID: "test", ModelID: "model"},
		currentSnapshot: chatpkg.Snapshot{Messages: []domain.Message{{ID: 1}, {ID: 2}}},
		viewport:        newTranscriptViewport(40, 6),
	}
	m.viewport.SetContent("line one\nline two")

	m.syncDebugRuntime()

	got := rec.Runtime()
	if got.CurrentSession != 7 || got.ViewportWidth != 40 || got.ViewportHeight != 6 {
		t.Fatalf("unexpected runtime snapshot: %#v", got)
	}
	if got.MessageCount != 2 {
		t.Fatalf("expected message count 2, got %#v", got)
	}
	if len(got.TranscriptItems) != 0 {
		t.Fatalf("expected transcript items omitted without deep debug, got %#v", got)
	}
}

func TestSyncDebugRuntimeSkipsIdenticalSnapshots(t *testing.T) {
	rec := debugsrv.NewRecorder()
	m := App{
		debug:           rec,
		status:          "Ready",
		currentSession:  domain.Session{ID: 7, Title: "Debug Session", ProviderID: "test", ModelID: "model"},
		currentSnapshot: chatpkg.Snapshot{Messages: []domain.Message{{ID: 1}, {ID: 2}}},
		viewport:        newTranscriptViewport(40, 6),
	}
	m.viewport.SetContent("line one\nline two")

	m.syncDebugRuntime()
	first := rec.Runtime().Timestamp
	m.syncDebugRuntime()
	second := rec.Runtime().Timestamp
	if !second.Equal(first) {
		t.Fatalf("expected identical runtime snapshot to be skipped, got timestamps %v then %v", first, second)
	}
}

func TestSyncDebugRuntimeIncludesTranscriptItemsInDeepDebug(t *testing.T) {
	rec := debugsrv.NewRecorder()
	rec.SetDeepDebug(true)
	m := App{
		debug:          rec,
		status:         "Ready",
		currentSession: domain.Session{ID: 7, Title: "Debug Session", ProviderID: "test", ModelID: "model"},
		viewport:       newTranscriptViewport(40, 6),
		currentSnapshot: chatpkg.Snapshot{Messages: []domain.Message{{
			ID:   1,
			Role: domain.MessageRoleAssistant,
		}}, Parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindText, Body: "hello"}},
		}},
		transcriptDirty: true,
	}
	m.viewport.SetContent("line one\nline two")

	m.syncDebugRuntime()

	got := rec.Runtime()
	if !got.DeepDebug {
		t.Fatalf("expected deep debug enabled, got %#v", got)
	}
	if len(got.TranscriptItems) != 1 {
		t.Fatalf("expected transcript items in deep debug, got %#v", got)
	}
}

func TestSyncDebugRuntimeIncludesInterruptAndFocusState(t *testing.T) {
	rec := debugsrv.NewRecorder()
	composer := textarea.New()
	m := App{
		debug:             rec,
		status:            "Streaming LLM response ...",
		activeEventStream: true,
		currentSession:    domain.Session{ID: 7, Title: "Debug Session", ProviderID: "test", ModelID: "model"},
		currentChat:       domain.Chat{ID: 9, SessionID: 7},
		viewport:          newTranscriptViewport(40, 6),
		composer:          composer,
		width:             80,
		height:            24,
		activeOpCancel:    func() {},
		activeOpCancels: map[int64]context.CancelFunc{
			9: func() {},
		},
	}
	m.viewport.SetContent("line one\nline two")
	m.startBusy(busyScopeTranscript, "Waiting for LLM response")
	m.setTranscriptBusyPhase(transcriptBusyPhaseResponse)

	m.syncDebugRuntime()

	got := rec.Runtime()
	if got.CurrentChat != 9 || !got.Loading || !got.ActiveEventStream {
		t.Fatalf("unexpected runtime snapshot: %#v", got)
	}
	if !got.TranscriptBusy || got.BusyScope != "transcript" {
		t.Fatalf("expected transcript busy state, got %#v", got)
	}
	if !got.CanInterrupt || !got.HasActiveCancel || !got.HasChatCancel {
		t.Fatalf("expected interrupt capability state, got %#v", got)
	}
	if got.FocusedWindow != string(mainWindowID) || !got.ComposerFocused || !got.InterruptKeyTarget {
		t.Fatalf("expected main-window focus and esc target state, got %#v", got)
	}
}

func TestRenderTranscriptToolMessageFallsBackToSummaryWhenBodyMissing(t *testing.T) {
	cfg := testConfig(t)
	m := App{
		cfg:             cfg,
		palette:         theme.Resolve("tokyonight").Palette,
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
	}

	got := m.renderTranscriptMessage(domain.Message{
		ID:      1,
		Role:    domain.MessageRoleTool,
		Summary: "bash completed with no output",
	})
	if !strings.Contains(got, "bash completed with no output") {
		t.Fatalf("expected tool summary fallback in transcript, got %q", got)
	}
}

func TestRefreshViewportGroupsToolRunMessagesIntoCard(t *testing.T) {
	cfg := testConfig(t)
	m := App{
		cfg:     cfg,
		palette: theme.Resolve("tokyonight").Palette,
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{
			1: {{
				Kind:     domain.PartKindToolCall,
				Body:     `{"command":"git status","tool":"bash","tool_call_id":"call_1"}`,
				MetaJSON: `{"command":"git status","tool":"bash","tool_call_id":"call_1"}`,
			}},
			2: {{
				Kind:     domain.PartKindApprovalRequest,
				Body:     "Approval required for bash: git status",
				MetaJSON: `{"approval_id":"7","tool":"bash","status":"pending","command":"git status","tool_call_id":"call_1"}`,
			}},
			3: {{
				Kind:     domain.PartKindToolOutput,
				Body:     "On branch main",
				MetaJSON: `{"tool":"bash","command":"git status","tool_call_id":"call_1"}`,
			}},
		}, Messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleAssistant},
			{ID: 2, Role: domain.MessageRoleTool},
			{ID: 3, Role: domain.MessageRoleTool, Summary: "bash"},
		}},
		viewport: newTranscriptViewport(80, 12),
	}

	m.refreshViewport()
	got := m.viewport.View()
	if !strings.Contains(got, "Ran command git status") {
		t.Fatalf("expected grouped tool title in transcript, got %q", got)
	}
	if strings.Contains(got, "│") || strings.Contains(got, "╭") || strings.Contains(got, "╰") {
		t.Fatalf("expected compact tool row without border chrome, got %q", got)
	}
	if !strings.Contains(got, "On branch main") {
		t.Fatalf("expected tool output preview, got %q", got)
	}
	if strings.Contains(got, `"tool":"bash"`) || strings.Contains(got, "Approval required for bash") {
		t.Fatalf("expected compact tool card instead of raw transcript blobs, got %q", got)
	}
}

func TestRenderApprovalPromptUsesApprovalDialog(t *testing.T) {
	cfg := testConfig(t)
	m := App{
		cfg:             cfg,
		palette:         theme.Resolve("tokyonight").Palette,
		viewport:        newTranscriptViewport(80, 12),
		currentSnapshot: chatpkg.Snapshot{Approvals: []store.Approval{{ID: 9, Tool: domain.ToolKindBash, Command: `{"command":"git status","tool_call_id":"call_1"}`}}},
	}

	got := m.renderApprovalPrompt()
	if !strings.Contains(got, "Approval required") || !strings.Contains(got, "Run command") {
		t.Fatalf("expected typed approval dialog, got %q", got)
	}
	if !strings.Contains(got, "Approve this time") || !strings.Contains(got, "Deny") {
		t.Fatalf("expected approval actions in dialog, got %q", got)
	}
	if strings.Contains(got, `{"command":"git status"`) {
		t.Fatalf("expected approval dialog to avoid raw JSON, got %q", got)
	}
}

func TestRefreshViewportSkipsSyntheticAssistantToolSummary(t *testing.T) {
	cfg := testConfig(t)
	m := App{
		cfg:     cfg,
		palette: theme.Resolve("tokyonight").Palette,
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{
			1: {{
				Kind:     domain.PartKindToolCall,
				MetaJSON: `{"path":"README.md","tool":"read","tool_call_id":"call_2"}`,
			}},
		}, Messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleAssistant, Summary: "tool:read"},
		}},
		viewport: newTranscriptViewport(80, 8),
	}

	m.refreshViewport()
	got := m.viewport.View()
	if strings.Contains(got, "tool:read") {
		t.Fatalf("expected synthetic tool summary to stay hidden, got %q", got)
	}
	if !strings.Contains(got, "Read file") || !strings.Contains(got, "README.md") {
		t.Fatalf("expected grouped read tool card, got %q", got)
	}
}

func TestToolOutputUsesRequestPreviewFromMeta(t *testing.T) {
	cfg := testConfig(t)
	m := App{
		cfg:     cfg,
		palette: theme.Resolve("tokyonight").Palette,
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{
			1: {{
				Kind:     domain.PartKindToolOutput,
				Body:     "# heading",
				MetaJSON: `{"tool":"read","path":"README.md","tool_call_id":"call_2"}`,
			}},
		}, Messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleTool, Summary: "read"},
		}},
		viewport: newTranscriptViewport(80, 8),
	}

	m.refreshViewport()
	got := m.viewport.View()
	if !strings.Contains(got, "README.md") {
		t.Fatalf("expected read path from tool output metadata, got %q", got)
	}
	if strings.Count(got, "README.md") != 1 {
		t.Fatalf("expected read path to appear once in collapsed card, got %q", got)
	}
	if strings.Contains(got, "\nread\n") {
		t.Fatalf("expected generic summary to be replaced by request preview, got %q", got)
	}
}

func TestEnterWithoutProviderOpensConnectDialog(t *testing.T) {
	m := App{
		cfg:      config.Default(),
		composer: textarea.New(),
	}
	m.composer.SetValue("hello")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*App)
	if cmd != nil {
		t.Fatal("expected no async command when provider is missing")
	}
	if !next.hasConnectDialog() {
		t.Fatal("expected connect dialog to open")
	}
	if next.composer.Value() != "hello" {
		t.Fatalf("expected prompt to remain in composer, got %q", next.composer.Value())
	}
}

func TestExactSlashCommandDoesNotConsumeEnterForAutocomplete(t *testing.T) {
	m := App{
		composer: textarea.New(),
	}
	m.composer.SetValue("/new")
	m.updateComposerMenus()

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected enter to continue to normal command handling")
	}
	if !next.loading {
		t.Fatal("expected loading after slash command enter")
	}
}

func TestExactSlashCommandWithArgsConsumesEnterForAutocomplete(t *testing.T) {
	m := App{
		composer: textarea.New(),
	}
	m.composer.SetValue("/mouse")
	m.updateComposerMenus()

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*App)
	if cmd != nil {
		t.Fatal("expected no command while autocompleting needs-args slash command")
	}
	if next.loading {
		t.Fatal("expected no loading while autocompleting needs-args slash command")
	}
	if got := next.composer.Value(); got != "/mouse " {
		t.Fatalf("expected /mouse autocompletion, got %q", got)
	}
}

func TestSlashSelectionExecutesNoArgsCommandDirectly(t *testing.T) {
	m := App{
		composer: textarea.New(),
	}
	m.composer.SetValue("/per")
	m.updateComposerMenus()

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected direct command execution")
	}
	if !next.hasPicker() {
		t.Fatal("expected /permissions to open immediately from slash menu")
	}
	if next.composer.Value() != "" {
		t.Fatalf("expected composer reset after command execution, got %q", next.composer.Value())
	}
}

func TestPermissionsPickerApplyAfterSlashCommandInvalidatesComposerArea(t *testing.T) {
	m := App{
		cfg:         testConfig(t),
		palette:     theme.Default().Palette,
		composer:    textarea.New(),
		viewport:    newTranscriptViewport(80, 20),
		renderCache: &modelRenderCache{},
		width:       80,
		height:      24,
	}
	m.composer.SetValue("/per")
	m.updateComposerMenus()

	_ = m.ViewLines()
	if !m.ensureRenderCache().composerAreaValid {
		t.Fatal("expected composer area cache to be primed")
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected direct command execution")
	}
	if !next.hasPicker() {
		t.Fatal("expected /permissions to open immediately from slash menu")
	}

	updated, cmd = next.submitPickerSelection(permission.ProfileAsk)
	final := updated.(*App)
	if cmd == nil {
		t.Fatal("expected title sync command after applying permission profile")
	}
	if final.composer.Value() != "" {
		t.Fatalf("expected empty composer after applying permission profile, got %q", final.composer.Value())
	}
	if final.ensureRenderCache().composerAreaValid {
		t.Fatal("expected composer area cache invalidated after clearing slash command")
	}
}

func TestRunPromptErrorAppendsAssistantErrorToTranscript(t *testing.T) {
	m := App{
		composer:        textarea.New(),
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
		viewport:        newTranscriptViewport(40, 6),
	}

	updated, cmd := m.Update(runPromptMsg{err: errors.New("connection refused")})
	next := updated.(App)
	if cmd == nil {
		t.Fatal("expected title sync command on immediate prompt error")
	}
	if next.loading {
		t.Fatal("expected loading cleared after prompt error")
	}
	if len(next.currentSnapshot.Messages) != 1 {
		t.Fatalf("expected local assistant error message, got %#v", next.currentSnapshot.Messages)
	}
	if next.currentSnapshot.Messages[0].Role != domain.MessageRoleAssistant {
		t.Fatalf("expected assistant role, got %s", next.currentSnapshot.Messages[0].Role)
	}
	if got := next.currentSnapshot.Parts[next.currentSnapshot.Messages[0].ID][0].Body; got != "Error: connection refused" {
		t.Fatalf("unexpected local error part: %q", got)
	}
	if !strings.Contains(next.viewport.View(), "Error: connection refused") {
		t.Fatalf("expected viewport to show error, got %q", next.viewport.View())
	}
}

func TestRunPromptMsgKeepsExistingRuntimeSubscription(t *testing.T) {
	rt := new(chatpkg.Chat)
	updates := make(chan chatpkg.Update)
	unsubCalled := 0
	m := App{
		cfg:                   config.Default().WithStateDir(t.TempDir()),
		composer:              textarea.New(),
		currentSnapshot:       chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
		viewport:              newTranscriptViewport(40, 6),
		currentRuntime:        rt,
		currentRuntimeUpdates: updates,
		currentRuntimeUnsub:   func() { unsubCalled++ },
	}

	updated, _ := m.Update(runPromptMsg{
		session:        domain.Session{ID: 1},
		chat:           domain.Chat{ID: 2},
		runtime:        rt,
		runtimeUpdates: updates,
		runtimeUnsub:   func() { unsubCalled += 100 },
	})
	next := updated.(App)

	if unsubCalled != 0 {
		t.Fatalf("expected existing runtime subscription to stay installed, got unsubCalled=%d", unsubCalled)
	}
	if next.currentRuntime != rt {
		t.Fatal("expected runtime to remain attached")
	}
	if next.currentRuntimeUpdates != updates {
		t.Fatal("expected runtime updates channel to remain attached")
	}
}

func TestRuntimeUpdateMsgAppliesRuntimeSnapshot(t *testing.T) {
	now := time.Now().UTC()
	message := domain.Message{ID: 10, SessionID: 1, ChatID: 2, Role: domain.MessageRoleUser, Summary: "queued steer", CreatedAt: now}
	part := domain.Part{
		ID:        11,
		MessageID: 10,
		Kind:      domain.PartKindText,
		Payload:   domain.TextPayload{Text: "queued steer"},
		Body:      "queued steer",
		CreatedAt: now,
	}
	updates := make(chan chatpkg.Update)
	m := App{
		cfg:             config.Default().WithStateDir(t.TempDir()),
		composer:        textarea.New(),
		currentRuntime:  &chatpkg.Chat{},
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
		viewport:        newTranscriptViewport(80, 20),
		currentSession:  domain.Session{ID: 1, Title: "Session"},
		currentChat:     domain.Chat{ID: 2, SessionID: 1},
	}

	updated, _ := m.Update(runtimeUpdateMsg{
		chatID: 2,
		update: chatpkg.Update{
			Snapshot: chatpkg.Snapshot{
				Session:      domain.Session{ID: 1, Title: "Session"},
				Chat:         domain.Chat{ID: 2, SessionID: 1, QueuedInputs: []domain.QueuedInput{{ID: 9, Kind: domain.QueuedInputKindQueued, Text: "later"}}},
				Messages:     []domain.Message{message},
				Parts:        map[int64][]domain.Part{10: {part}},
				QueuedInputs: []domain.QueuedInput{{ID: 9, Kind: domain.QueuedInputKindQueued, Text: "later"}},
				PendingAssistant: chatpkg.PendingAssistantTurn{
					Text:      "streaming text",
					CreatedAt: now,
				},
				Status:     chatpkg.StatusStreamingResponse,
				StatusText: "Streaming LLM response ...",
				Context:    domain.ContextUsage{TotalTokens: 42, Estimated: true},
				Active:     true,
			},
			TranscriptChanged: true,
			QueueChanged:      true,
			StatusChanged:     true,
			ContextChanged:    true,
			Active:            true,
			Status:            chatpkg.StatusStreamingResponse,
			StatusText:        "Streaming LLM response ...",
			Queue:             []domain.QueuedInput{{ID: 9, Kind: domain.QueuedInputKindQueued, Text: "later"}},
		},
		updates: updates,
	})
	next := updated.(App)

	if got := next.currentSnapshot.Messages; len(got) != 1 || got[0].Summary != "queued steer" {
		t.Fatalf("expected runtime snapshot messages applied, got %#v", got)
	}
	if len(next.currentChat.QueuedInputs) != 1 || next.currentChat.QueuedInputs[0].Text != "later" {
		t.Fatalf("expected runtime queue applied, got %#v", next.currentChat.QueuedInputs)
	}
	if got := next.currentSnapshot.PendingAssistant; got.Text != "streaming text" {
		t.Fatalf("expected pending assistant from runtime snapshot, got %#v", got)
	}
	if !next.activeEventStream {
		t.Fatal("expected runtime update to mark active event stream")
	}
	if next.status != "Streaming LLM response ..." {
		t.Fatalf("unexpected status %q", next.status)
	}
}

func TestRepeatedStreamingRuntimeUpdateDoesNotInvalidateTranscript(t *testing.T) {
	m := App{
		cfg:             config.Default().WithStateDir(t.TempDir()),
		composer:        textarea.New(),
		currentRuntime:  &chatpkg.Chat{},
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
		viewport:        newTranscriptViewport(80, 20),
		currentSession:  domain.Session{ID: 1, Title: "Session"},
		currentChat:     domain.Chat{ID: 2, SessionID: 1},
		width:           80,
		height:          24,
		loading:         true,
		status:          "Streaming LLM response ...",
	}
	m.busy.start(busyScopeTranscript, "Streaming LLM response ...")
	m.busy.setTranscriptPhase(transcriptBusyPhaseResponse)
	m.transcriptDirty = false

	m.syncBusyFromRuntimeUpdate(chatpkg.Update{
		Active:     true,
		Status:     chatpkg.StatusStreamingResponse,
		StatusText: "Streaming LLM response ...",
	})

	if m.transcriptDirty {
		t.Fatal("expected repeated streaming busy update to avoid transcript invalidation")
	}
	if m.busy.transcriptPhase != transcriptBusyPhaseResponse {
		t.Fatalf("expected response phase to remain active, got %#v", m.busy.transcriptPhase)
	}
	if m.status != "Streaming LLM response ..." {
		t.Fatalf("unexpected status %q", m.status)
	}
}

func TestSetQueuedInputsUpdatesRuntimeSnapshotQueue(t *testing.T) {
	m := App{
		currentRuntime:  &chatpkg.Chat{},
		currentChat:     domain.Chat{ID: 2, SessionID: 1},
		currentSnapshot: chatpkg.Snapshot{Chat: domain.Chat{ID: 2, SessionID: 1, QueuedInputs: []domain.QueuedInput{{ID: 1, Kind: domain.QueuedInputKindQueued, Text: "before"}}}, QueuedInputs: []domain.QueuedInput{{ID: 1, Kind: domain.QueuedInputKindQueued, Text: "before"}}},
	}

	nextItems := []domain.QueuedInput{{ID: 2, Kind: domain.QueuedInputKindSteer, Text: "after"}}
	m.setQueuedInputs(nextItems)

	if len(m.activeQueuedInputs()) != 1 || m.activeQueuedInputs()[0].Text != "after" {
		t.Fatalf("expected active queue to come from updated runtime snapshot, got %#v", m.activeQueuedInputs())
	}
	if len(m.currentSnapshot.QueuedInputs) != 1 || m.currentSnapshot.QueuedInputs[0].Text != "after" {
		t.Fatalf("expected runtime snapshot queue updated, got %#v", m.currentSnapshot.QueuedInputs)
	}
	if len(m.currentSnapshot.Chat.QueuedInputs) != 1 || m.currentSnapshot.Chat.QueuedInputs[0].Text != "after" {
		t.Fatalf("expected runtime snapshot chat queue updated, got %#v", m.currentSnapshot.Chat.QueuedInputs)
	}
}

func TestApplyEventKeepsRuntimePendingAssistantSnapshotInSync(t *testing.T) {
	m := App{
		currentRuntime:  &chatpkg.Chat{},
		currentSnapshot: chatpkg.Snapshot{Chat: domain.Chat{ID: 2, SessionID: 1}},
	}

	m.applyEvent(domain.Event{Kind: domain.EventKindMessageDelta, Text: "The"})
	m.applyEvent(domain.Event{Kind: domain.EventKindReasoning, Text: "thinking"})

	if got := m.activePendingAssistant(); got.Text != "The" || got.Reasoning != "thinking" {
		t.Fatalf("expected active pending assistant snapshot updated, got %#v", got)
	}

	m.clearPendingAssistantTurn()
	if got := m.activePendingAssistant(); strings.TrimSpace(got.Text) != "" || strings.TrimSpace(got.Reasoning) != "" {
		t.Fatalf("expected active pending assistant snapshot cleared, got %#v", got)
	}
}

func TestApplyEventKeepsRuntimeSnapshotMetadataInSync(t *testing.T) {
	m := App{
		currentRuntime:  &chatpkg.Chat{},
		currentSession:  domain.Session{ID: 1, Title: "Old", PermissionProfile: permission.ProfileWriteAsk},
		currentChat:     domain.Chat{ID: 2, SessionID: 1},
		currentSnapshot: chatpkg.Snapshot{Session: domain.Session{ID: 1, Title: "Old", PermissionProfile: permission.ProfileWriteAsk}, Chat: domain.Chat{ID: 2, SessionID: 1}},
	}

	m.applyEvent(domain.Event{Kind: domain.EventKindSessionTitle, Text: "New"})
	m.applyEvent(domain.Event{Kind: domain.EventKindStatus, Meta: map[string]string{"permission_profile": permission.ProfileAsk}})
	m.applyEvent(domain.Event{Kind: domain.EventKindUsage, Usage: domain.Usage{PromptTokens: 123}})

	if got := m.currentSnapshot.Session.Title; got != "New" {
		t.Fatalf("expected runtime snapshot title updated, got %q", got)
	}
	if got := m.currentSnapshot.Session.PermissionProfile; got != permission.ProfileAsk {
		t.Fatalf("expected runtime snapshot permission profile updated, got %q", got)
	}
	if got := m.currentSnapshot.Chat.LastKnownContextTokens; got != 123 || !m.currentSnapshot.Chat.ContextTokensKnown {
		t.Fatalf("expected runtime snapshot chat context updated, got %#v", m.currentSnapshot.Chat)
	}
}

func TestNewSessionMsgClearsBusyState(t *testing.T) {
	m := App{
		busy: busyModel{
			active: true,
			scope:  busyScopeSidebar,
			status: "Creating session…",
			spinner: spinnerModel{
				active: true,
			},
		},
		loading:         true,
		composer:        textarea.New(),
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
		viewport:        newTranscriptViewport(40, 6),
	}

	updated, _ := m.Update(newSessionMsg{
		session:   domain.Session{Title: "New Session"},
		parts:     map[int64][]domain.Part{},
		workspace: workspace.Status{},
	})
	next := updated.(App)
	if next.loading {
		t.Fatal("expected new session to clear loading")
	}
	if next.busy.active {
		t.Fatal("expected new session to stop busy state")
	}
	if next.status != "Started new session" {
		t.Fatalf("unexpected status: %q", next.status)
	}
}

func TestForkCommandCreatesForkedSession(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	session, err := st.CreateSession(context.Background(), "Source Session", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := st.AddMessage(context.Background(), session.ID, domain.MessageRoleUser, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddPart(context.Background(), msg.ID, domain.TextPayload{Text: "hello"}); err != nil {
		t.Fatal(err)
	}

	m := App{
		cfg:             cfg,
		store:           st,
		composer:        textarea.New(),
		viewport:        newTranscriptViewport(40, 6),
		currentSession:  session,
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
		workdir:         t.TempDir(),
	}
	m.composer.SetValue("/fork")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected fork command")
	}
	if !next.loading {
		t.Fatal("expected loading while forking")
	}

	msgAny := next.forkSessionCmd(session.ID)()
	forkMsg, ok := msgAny.(forkSessionMsg)
	if !ok {
		t.Fatalf("expected forkSessionMsg, got %T", msgAny)
	}
	updated, _ = next.Update(forkMsg)
	forked := updated.(App)
	if forked.currentSession.ID == session.ID {
		t.Fatal("expected forked session id to differ from source")
	}
	if forked.currentSession.ParentID == nil || *forked.currentSession.ParentID != session.ID {
		t.Fatalf("expected parent id %d, got %#v", session.ID, forked.currentSession.ParentID)
	}
	if len(forked.currentSnapshot.Messages) != 1 || forked.currentSnapshot.Messages[0].Summary != "hello" {
		t.Fatalf("unexpected forked messages: %#v", forked.currentSnapshot.Messages)
	}
	if forked.status == "" || !strings.Contains(forked.status, "Forked session") {
		t.Fatalf("unexpected fork status: %q", forked.status)
	}
}

func TestToolLikeSlashCommandIsRejectedLocally(t *testing.T) {
	m := App{
		composer: textarea.New(),
	}
	m.composer.SetValue("/read README.md")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*App)
	if cmd != nil {
		t.Fatal("expected no command for hidden tool-like slash input")
	}
	if next.loading {
		t.Fatal("expected hidden tool-like slash input to stay local")
	}
	if next.status != "unknown command: /read README.md" {
		t.Fatalf("unexpected status: %q", next.status)
	}
}

func TestApprovalDialogConsumesEnter(t *testing.T) {
	m := App{
		composer:        textarea.New(),
		currentSnapshot: chatpkg.Snapshot{Approvals: []store.Approval{{ID: 7}}},
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected approval command")
	}
	if !next.loading {
		t.Fatal("expected loading after approval enter")
	}
}

func TestApprovalDialogOpensPermissionsPicker(t *testing.T) {
	m := App{
		cfg:             testConfig(t),
		composer:        textarea.New(),
		currentSnapshot: chatpkg.Snapshot{Approvals: []store.Approval{{ID: 7, Tool: domain.ToolKindBash, Command: `{"command":"git status"}`}}},
	}
	m.ensureApprovalDialog()
	m.approvalDialog.SetButtonIndex(4)

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected sync title command")
	}
	if !next.hasPicker() {
		t.Fatal("expected permission picker to open from approval dialog")
	}
	if next.picker.mode != pickerModePermissions {
		t.Fatalf("expected permissions picker mode, got %v", next.picker.mode)
	}
	if next.picker.approvalID != 7 {
		t.Fatalf("expected approval id to be preserved, got %d", next.picker.approvalID)
	}
	if next.loading {
		t.Fatal("expected opening permissions picker to avoid starting approval command")
	}
}

func TestApprovalDialogArrowNavigationThenEnterOpensPermissionsPicker(t *testing.T) {
	m := App{
		cfg:             testConfig(t),
		composer:        textarea.New(),
		currentSnapshot: chatpkg.Snapshot{Approvals: []store.Approval{{ID: 7, Tool: domain.ToolKindBash, Command: `{"command":"git status"}`}}},
	}

	var updated ui.Model = &m
	var cmd ui.Cmd
	for i := 0; i < 4; i++ {
		updated, cmd = updated.(*App).handleKey(ui.KeyMsg{Type: ui.KeyRight})
		next := updated.(*App)
		if cmd != nil {
			t.Fatal("expected navigation to avoid starting a command")
		}
		if !next.hasApprovalDialog() {
			t.Fatal("expected approval dialog to remain open")
		}
	}
	next := updated.(*App)
	if next.approvalDialog.ButtonIndex() != 4 {
		t.Fatalf("expected right arrow to focus permissions button, got %d", next.approvalDialog.ButtonIndex())
	}

	updated, cmd = next.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next = updated.(*App)
	if cmd == nil {
		t.Fatal("expected sync title command")
	}
	if !next.hasPicker() {
		t.Fatal("expected permission picker to open from approval dialog")
	}
	if next.picker.mode != pickerModePermissions {
		t.Fatalf("expected permissions picker mode, got %v", next.picker.mode)
	}
	if next.picker.approvalID != 7 {
		t.Fatalf("expected approval id to be preserved, got %d", next.picker.approvalID)
	}
}

func TestApprovalDialogAltHotkeys(t *testing.T) {
	m := App{
		cfg:             testConfig(t),
		composer:        textarea.New(),
		currentSnapshot: chatpkg.Snapshot{Approvals: []store.Approval{{ID: 7, Tool: domain.ToolKindBash, Command: `{"command":"git status"}`}}},
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Alt: true, Runes: []rune("s")})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected sync title command")
	}
	if !next.hasPicker() || next.picker.approvalID != 7 {
		t.Fatalf("expected alt+s to open permission picker for approval, got %#v", next.picker)
	}

	m = App{
		cfg:             testConfig(t),
		composer:        textarea.New(),
		currentSnapshot: chatpkg.Snapshot{Approvals: []store.Approval{{ID: 7, Tool: domain.ToolKindBash, Command: `{"command":"git status"}`}}},
	}
	updated, cmd = m.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Alt: true, Runes: []rune("t")})
	next = updated.(*App)
	if cmd == nil {
		t.Fatal("expected alt+t to trigger approval command")
	}
	if !next.loading {
		t.Fatal("expected alt+t to start approval flow")
	}

	m = App{
		cfg:             testConfig(t),
		composer:        textarea.New(),
		currentSnapshot: chatpkg.Snapshot{Approvals: []store.Approval{{ID: 7, Tool: domain.ToolKindBash, Command: `{"command":"git status"}`}}},
	}
	updated, cmd = m.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Alt: true, Runes: []rune("d")})
	next = updated.(*App)
	if cmd == nil {
		t.Fatal("expected alt+d to trigger deny command")
	}
	if !next.loading {
		t.Fatal("expected alt+d to start deny flow")
	}
}

func TestAltOOpensNextLLMRequestPreview(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	workdir := t.TempDir()
	m := App{
		cfg:            cfg,
		composer:       textarea.New(),
		agent:          agent.New(cfg, st, tools.NewRegistry(workdir), nil, workdir),
		currentSession: domain.Session{ProviderID: "test", ModelID: "test-model"},
		width:          100,
		height:         30,
	}
	m.composer.SetValue("draft question")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Alt: true, Runes: []rune("o")})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected preview command")
	}
	msg := cmd()
	if _, ok := msg.(llmPreviewMsg); !ok {
		t.Fatalf("expected llmPreviewMsg, got %T", msg)
	}
	updated, cmd = next.Update(msg)
	if cmd == nil {
		t.Fatal("expected sync title command after preview opens")
	}
	previewModel := updated.(App)
	rendered := (&previewModel).renderLLMPreview()
	if !strings.Contains(rendered, "Next LLM Request") {
		t.Fatalf("expected preview title, got %q", rendered)
	}
	if !strings.Contains(rendered, `"model": "test-model"`) || !strings.Contains(rendered, `"draft question"`) {
		t.Fatalf("expected preview payload in rendered output, got %q", rendered)
	}
}

func TestAltOWithoutDraftShowsStatus(t *testing.T) {
	m := App{
		cfg:      testConfig(t),
		composer: textarea.New(),
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Alt: true, Runes: []rune("o")})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected sync title command")
	}
	if next.hasLLMPreview() {
		t.Fatal("expected no preview")
	}
	if next.status != "No draft prompt to preview" {
		t.Fatalf("unexpected status: %q", next.status)
	}
}

func TestLLMPreviewScrollUsesElementOffsetState(t *testing.T) {
	m := App{
		cfg:      testConfig(t),
		composer: textarea.New(),
		width:    80,
		height:   12,
	}
	m.openLLMPreview("Preview", strings.Join([]string{
		"line 1",
		"line 2",
		"line 3",
		"line 4",
		"line 5",
		"line 6",
		"line 7",
	}, "\n"))
	m.llmPreviewHeight = 3
	m.llmPreviewWidth = 20

	if !m.handleLLMPreviewKey(ui.KeyMsg{Type: ui.KeyDown}) {
		t.Fatal("expected preview down key to be handled")
	}
	if m.llmPreviewYOffset != 1 {
		t.Fatalf("expected preview offset 1, got %d", m.llmPreviewYOffset)
	}

	rendered := ansi.Strip(m.renderLLMPreview())
	if !strings.Contains(rendered, "line 2") {
		t.Fatalf("expected scrolled preview to show later lines, got %q", rendered)
	}
	if strings.Contains(rendered, "line 1") {
		t.Fatalf("expected first line to scroll out of view, got %q", rendered)
	}
}

func TestQuitCommandQuits(t *testing.T) {
	m := App{
		composer: textarea.New(),
	}
	m.composer.SetValue("/quit")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected quit command")
	}
	if next.loading {
		t.Fatal("expected quit to stop loading")
	}
}

func TestMouseOnCommandEnablesMouseCapture(t *testing.T) {
	m := App{
		composer: textarea.New(),
	}
	m.composer.SetValue("/mouse on")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected mouse enable command")
	}
	if !next.mouseEnabled {
		t.Fatal("expected mouse capture enabled")
	}
	if next.status != "Mouse capture enabled" {
		t.Fatalf("unexpected status: %q", next.status)
	}
}

func TestMouseOffCommandDisablesMouseCapture(t *testing.T) {
	m := App{
		composer:     textarea.New(),
		mouseEnabled: true,
	}
	m.composer.SetValue("/mouse off")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected mouse disable command")
	}
	if next.mouseEnabled {
		t.Fatal("expected mouse capture disabled")
	}
	if next.status != "Mouse capture disabled" {
		t.Fatalf("unexpected status: %q", next.status)
	}
}

func TestInitEnablesMouseWhenConfigured(t *testing.T) {
	cfg := testConfig(t)
	cfg.UI.Mouse = true

	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !m.mouseEnabled {
		t.Fatal("expected mouseEnabled to follow config")
	}

	cmd := m.Init()
	if cmd == nil {
		t.Fatal("expected init command")
	}
	if _, ok := cmd().(ui.BatchMsg); !ok {
		t.Fatalf("expected batched init command when mouse is enabled, got %T", cmd())
	}
}

func TestCtrlCUsesQuitPath(t *testing.T) {
	m := App{
		composer: textarea.New(),
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyCtrlC})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected quit command")
	}
	if next.status != "Quitting" {
		t.Fatalf("unexpected status: %q", next.status)
	}
}

func TestEscInterruptCancelsActiveOperationImmediately(t *testing.T) {
	cancelled := false
	m := App{
		composer: textarea.New(),
		loading:  true,
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
		},
		activeOpCancel: func() { cancelled = true },
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEsc})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected title sync command after esc")
	}
	if !cancelled {
		t.Fatal("expected active operation to be cancelled")
	}
	if next.status != "Interrupting…" {
		t.Fatalf("unexpected esc status: %q", next.status)
	}
	if !next.interruptArmedAt.IsZero() {
		t.Fatal("expected interrupt arming to stay cleared")
	}
}

func TestEscInterruptCancelsActiveOperationWhenNotLoading(t *testing.T) {
	cancelled := false
	m := App{
		composer: textarea.New(),
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
		},
		activeOpCancel: func() { cancelled = true },
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEsc})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected title sync command after esc")
	}
	if !cancelled {
		t.Fatal("expected active operation to be cancelled")
	}
	if next.status != "Interrupting…" {
		t.Fatalf("unexpected esc status: %q", next.status)
	}
}

func TestHandleKeyEscInterruptsWhileMainWindowFocused(t *testing.T) {
	cancelled := false
	m := App{
		composer: textarea.New(),
		width:    100,
		height:   30,
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
		},
		activeOpCancel: func() { cancelled = true },
	}
	root := m.syncUIRoot()
	if got := root.FocusedWindow(); got != mainWindowID {
		t.Fatalf("expected focused main window, got %q", got)
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEsc})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected title sync command after esc")
	}
	if !cancelled {
		t.Fatal("expected active operation to be cancelled")
	}
	if next.status != "Interrupting…" {
		t.Fatalf("unexpected esc status: %q", next.status)
	}
	if len(next.currentSnapshot.Messages) != 1 {
		t.Fatalf("expected local interrupted notice message, got %#v", next.currentSnapshot.Messages)
	}
	if got := next.currentSnapshot.Parts[next.currentSnapshot.Messages[0].ID]; len(got) != 1 || got[0].Kind != domain.PartKindEventNotice || got[0].Body != "Interrupted" {
		t.Fatalf("expected interrupted event notice part, got %#v", got)
	}
}

func TestEscInterruptCancelsCurrentChatOperationFromMap(t *testing.T) {
	cancelled := false
	m := App{
		composer: textarea.New(),
		width:    100,
		height:   30,
		currentChat: domain.Chat{
			ID: 42,
		},
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
		},
		activeOpCancels: map[int64]context.CancelFunc{
			42: func() { cancelled = true },
		},
	}
	root := m.syncUIRoot()
	if got := root.FocusedWindow(); got != mainWindowID {
		t.Fatalf("expected focused main window, got %q", got)
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEsc})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected title sync command after esc")
	}
	if !cancelled {
		t.Fatal("expected current chat operation to be cancelled")
	}
	if next.status != "Interrupting…" {
		t.Fatalf("unexpected esc status: %q", next.status)
	}
	if len(next.currentSnapshot.Messages) != 1 {
		t.Fatalf("expected local interrupted notice message, got %#v", next.currentSnapshot.Messages)
	}
	if got := next.currentSnapshot.Parts[next.currentSnapshot.Messages[0].ID]; len(got) != 1 || got[0].Kind != domain.PartKindEventNotice || got[0].Body != "Interrupted" {
		t.Fatalf("expected interrupted event notice part, got %#v", got)
	}
}

func TestFinishOperationWithCanceledErrorKeepsBusyUntilActiveStreamEnds(t *testing.T) {
	m := App{
		composer: textarea.New(),
		loading:  true,
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
		},
		activeEventStream: true,
	}

	updated, cmd := m.finishOperationWithError(context.Canceled)
	next := updated.(App)
	if cmd == nil {
		t.Fatal("expected title sync command after interrupted finish")
	}
	if !next.busy.active {
		t.Fatal("expected busy state to remain active until stream end")
	}
	if !next.loading {
		t.Fatal("expected loading to remain true until stream end")
	}
	if next.status != "Interrupted" {
		t.Fatalf("unexpected status: %q", next.status)
	}
}

func TestFinishOperationWithCanceledErrorStopsBusyWithoutActiveStream(t *testing.T) {
	m := App{
		composer: textarea.New(),
		loading:  true,
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
		},
	}

	updated, cmd := m.finishOperationWithError(context.Canceled)
	next := updated.(App)
	if cmd == nil {
		t.Fatal("expected title sync command after interrupted finish")
	}
	if next.busy.active {
		t.Fatal("expected busy state to stop when no stream is active")
	}
	if next.loading {
		t.Fatal("expected loading to stop when no stream is active")
	}
	if next.status != "Interrupted" {
		t.Fatalf("unexpected status: %q", next.status)
	}
}

func TestExitSummaryIncludesSessionDetails(t *testing.T) {
	m := App{
		currentSession:  domain.Session{ID: 4, Title: "Testing Session Review Flow"},
		currentSnapshot: chatpkg.Snapshot{Messages: []domain.Message{{ID: 1}, {ID: 2}, {ID: 3}}},
	}

	got := m.exitSummary()
	want := `Closed session 4 "Testing Session Review Flow" with 3 messages.`
	if got != want {
		t.Fatalf("unexpected summary: %q", got)
	}
}

func TestSessionPickerEscapeCreatesNewSession(t *testing.T) {
	m := App{
		composer:      textarea.New(),
		sessionDialog: &dialogs.SessionDialog{},
		sessions:      []domain.Session{{ID: 1}},
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEsc})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected new session command")
	}
	if !next.loading {
		t.Fatal("expected loading after picker escape")
	}
}

func TestSessionPickerRendersCenteredDialogWithPreview(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	session, err := st.CreateSession(context.Background(), "Generated Session Title", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := st.AddMessage(context.Background(), session.ID, domain.MessageRoleAssistant, "summary")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddPart(context.Background(), msg.ID, domain.UsagePayload{Usage: domain.Usage{PromptTokens: 123, CompletionTokens: 456, TotalTokens: 579}}); err != nil {
		t.Fatal(err)
	}

	m := App{
		width:   100,
		height:  30,
		store:   st,
		palette: theme.Default().Palette,
		sessions: []domain.Session{{
			ID:          session.ID,
			Title:       "Generated Session Title",
			LastMessage: "summary",
			CreatedAt:   time.Date(2026, 4, 20, 10, 30, 0, 0, time.UTC),
			UpdatedAt:   time.Date(2026, 4, 20, 12, 45, 0, 0, time.UTC),
		}},
	}
	m.openSessionPicker()

	got := m.View()
	if !strings.Contains(got, "Resume Session") {
		t.Fatalf("expected centered dialog title, got %q", got)
	}
	if !strings.Contains(got, "Generated Session Title") {
		t.Fatalf("expected title in dialog, got %q", got)
	}
	if !strings.Contains(got, "summary") {
		t.Fatalf("expected last message preview in dialog, got %q", got)
	}
	if strings.Contains(got, "Session ID: 1") {
		t.Fatalf("expected session details to stay in the table, got %q", got)
	}
	if !strings.Contains(got, "ID") || !strings.Contains(got, "Created") || !strings.Contains(got, "Modified") || !strings.Contains(got, "Tokens") {
		t.Fatalf("expected session table headers in dialog, got %q", got)
	}
	if !strings.Contains(got, "123/456") {
		t.Fatalf("expected token summary in table, got %q", got)
	}
	if !strings.Contains(got, "OK") || !strings.Contains(got, "Cancel") {
		t.Fatalf("expected dialog buttons in session dialog, got %q", got)
	}
	if !strings.Contains(got, "Enter resumes the highlighted session. Esc creates a new session.") {
		t.Fatalf("expected helper text in dialog, got %q", got)
	}
	if strings.Contains(got, "\x1b[") || strings.Contains(got, "[38;") || strings.Contains(got, "[1;") {
		t.Fatalf("expected plain cell-rendered session picker output, got %q", got)
	}
}

func TestSessionPickerLeavesScreenMarginForRightBorder(t *testing.T) {
	m := App{
		width:  72,
		height: 24,
		sessions: []domain.Session{{
			ID:          1,
			Title:       "Session A",
			LastMessage: "summary",
		}},
	}

	m.openSessionPicker()
	got := m.View()
	lines := strings.Split(got, "\n")
	foundTopBorder := false
	for _, line := range lines {
		if strings.Contains(line, "╮") {
			foundTopBorder = true
			if idx := strings.Index(line, "╮"); idx >= 0 {
				col := ansi.StringWidth(line[:idx])
				if col >= m.width-1 {
					t.Fatalf("expected right border to render inside screen margin, got %q", line)
				}
			}
			break
		}
	}
	if !foundTopBorder {
		t.Fatalf("expected session dialog border in view, got %q", got)
	}
}

func TestOpenSessionPickerShowsCWDWhenAllSessionsEnabled(t *testing.T) {
	m := App{
		startupOptions: StartupOptions{ShowAllSessions: true},
		sessions: []domain.Session{{
			ID:          1,
			Title:       "Session A",
			LastMessage: "summary",
			CWD:         "/tmp/worktree",
		}},
	}

	m.openSessionPicker()
	got := m.renderSessionDialog()
	if !strings.Contains(got, "CWD") || !strings.Contains(got, "/tmp/worktree") {
		t.Fatalf("expected cwd column in picker, got %q", got)
	}
}

func TestOpenSessionPickerBlursComposer(t *testing.T) {
	m := App{
		composer: textarea.New(),
		sessions: []domain.Session{{
			ID:          1,
			Title:       "Session A",
			LastMessage: "summary",
		}},
	}

	if !m.composer.Focused() {
		t.Fatal("expected composer to start focused")
	}

	m.openSessionPicker()

	if m.composer.Focused() {
		t.Fatal("expected session picker to blur the hidden composer")
	}
}

func TestNewWithWorkdirStartsComposerFocusedWithBlinkTimer(t *testing.T) {
	cfg := testConfig(t)
	cfg.UI.CursorBlink = true
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	workdir := t.TempDir()
	m, err := NewWithWorkdir(cfg, st, nil, StartupModeNew, nil, workdir, StartupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !m.composer.Focused() {
		t.Fatal("expected composer to start focused")
	}
	if !m.composer.CursorVisible() {
		t.Fatal("expected startup composer cursor to be visible")
	}
	if timers := m.ensureUIRoot().ActiveTimers(composerBlinkTimerOwner); len(timers) == 0 {
		t.Fatal("expected startup composer to own a blink timer")
	}
}

func TestClosingSessionPickerRefocusesComposer(t *testing.T) {
	m := App{
		composer: textarea.New(),
		sessions: []domain.Session{{
			ID:          1,
			Title:       "Session A",
			LastMessage: "summary",
		}},
	}

	m.openSessionPicker()
	m.closeSessionDialog()

	if !m.composer.Focused() {
		t.Fatal("expected closing the session picker to refocus the composer")
	}
}

func TestOpenSessionPickerStopsComposerBlinkTimer(t *testing.T) {
	m := App{
		composer: textarea.New(),
		palette:  theme.Default().Palette,
		width:    80,
		height:   24,
		sessions: []domain.Session{{
			ID:          1,
			Title:       "Session A",
			LastMessage: "summary",
		}},
	}

	m.ensureMainScreenView().SyncComposerBlinkTimer(m.ensureUIRoot())
	if timers := m.syncUIRoot().ActiveTimers(composerBlinkTimerOwner); len(timers) == 0 {
		t.Fatal("expected focused composer to own a blink timer")
	}

	m.openSessionPicker()
	if timers := m.syncUIRoot().ActiveTimers(composerBlinkTimerOwner); len(timers) != 0 {
		t.Fatalf("expected session picker to stop composer blink timer, got %v", timers)
	}
}

func TestClosingSessionPickerRestartsComposerBlinkTimer(t *testing.T) {
	m := App{
		composer: textarea.New(),
		palette:  theme.Default().Palette,
		width:    80,
		height:   24,
		sessions: []domain.Session{{
			ID:          1,
			Title:       "Session A",
			LastMessage: "summary",
		}},
	}

	m.openSessionPicker()
	m.closeSessionDialog()

	if timers := m.syncUIRoot().ActiveTimers(composerBlinkTimerOwner); len(timers) == 0 {
		t.Fatal("expected closing session picker to restart composer blink timer")
	}
}

func TestSelectingSessionKeepsComposerBlinkStopped(t *testing.T) {
	m := App{
		composer: textarea.New(),
		palette:  theme.Default().Palette,
		width:    80,
		height:   24,
		sessions: []domain.Session{{
			ID:          7,
			Title:       "Session A",
			LastMessage: "summary",
		}},
	}

	m.openSessionPicker()
	updated, cmd := m.Update(ui.KeyMsg{Type: ui.KeyEnter})
	if cmd == nil {
		t.Fatal("expected resume command")
	}
	next := asModelPtr(t, updated)
	if timers := next.syncUIRoot().ActiveTimers(composerBlinkTimerOwner); len(timers) != 0 {
		t.Fatalf("expected composer blink timer to stay stopped while session dialog is active, got %v", timers)
	}
}

func TestWithRootTimersDoesNotQueueDuplicateTicks(t *testing.T) {
	m := App{
		composer: textarea.New(),
		palette:  theme.Default().Palette,
		width:    80,
		height:   24,
	}
	m.composer.Focus()

	first := m.withRootTimers(nil)
	if first == nil {
		t.Fatal("expected initial root timer command")
	}
	if !m.rootTimerPending {
		t.Fatal("expected root timer to be marked pending")
	}
	firstSeq := m.rootTimerSeq
	firstDue := m.rootTimerPendingAt

	second := m.withRootTimers(nil)
	if second != nil {
		t.Fatal("expected duplicate root timer scheduling to be suppressed")
	}
	if m.rootTimerSeq != firstSeq {
		t.Fatalf("expected root timer sequence to remain %d, got %d", firstSeq, m.rootTimerSeq)
	}
	if !m.rootTimerPendingAt.Equal(firstDue) {
		t.Fatalf("expected pending due time to stay %v, got %v", firstDue, m.rootTimerPendingAt)
	}
}

func TestVisibleSessionsFiltersByExactCWD(t *testing.T) {
	m := App{workdir: "/repo/a"}
	sessions := []domain.Session{
		{ID: 1, CWD: "/repo/a"},
		{ID: 2, CWD: "/repo/b"},
		{ID: 3, ProjectRoot: "/repo"},
	}

	got := m.visibleSessions(sessions)
	if len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("expected only matching cwd session, got %#v", got)
	}
}

func TestFormatRelativeSessionTime(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		when time.Time
		want string
	}{
		{name: "zero", when: time.Time{}, want: "-"},
		{name: "now", when: now.Add(-30 * time.Second), want: "now"},
		{name: "minutes", when: now.Add(-3 * time.Minute), want: "3m ago"},
		{name: "hours", when: now.Add(-10 * time.Hour), want: "10h ago"},
		{name: "days", when: now.Add(-48 * time.Hour), want: "2d ago"},
	}

	for _, tc := range cases {
		if got := formatRelativeSessionTime(tc.when); got != tc.want {
			t.Fatalf("%s: expected %q, got %q", tc.name, tc.want, got)
		}
	}
}

func TestUpdateLoadHidesSessionPicker(t *testing.T) {
	m := App{
		composer:      textarea.New(),
		palette:       theme.Default().Palette,
		width:         80,
		height:        24,
		sessionDialog: &dialogs.SessionDialog{},
	}
	m.composer.Blur()

	updated := m.UpdateLoad(loadMsg{
		current: domain.Session{ID: 4},
	})

	if updated.hasSessionDialog() {
		t.Fatal("expected session dialog to close after loading a session")
	}
	if updated.currentSession.ID != 4 {
		t.Fatalf("unexpected current session: %#v", updated.currentSession)
	}
	if !updated.composer.Focused() {
		t.Fatal("expected loading a session to refocus the composer")
	}
}

func TestUpdateLoadPreservesActiveInterruptForBusyChat(t *testing.T) {
	cancelled := false
	m := App{
		composer: textarea.New(),
		palette:  theme.Default().Palette,
		width:    80,
		height:   24,
		loading:  true,
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
		},
		activeOpCancel: func() { cancelled = true },
		chatBusy: map[int64]busyModel{
			7: {active: true, scope: busyScopeTranscript},
		},
	}

	updated := m.UpdateLoad(loadMsg{
		current: domain.Session{ID: 4},
		chat:    domain.Chat{ID: 7, SessionID: 4},
	})
	if !updated.canInterruptActiveOperation() {
		t.Fatal("expected busy loaded chat to retain interrupt capability")
	}

	nextModel, cmd := updated.handleKey(ui.KeyMsg{Type: ui.KeyEsc})
	next := nextModel.(*App)
	if cmd == nil {
		t.Fatal("expected title sync command after esc")
	}
	if !cancelled {
		t.Fatal("expected preserved active operation to be cancelled")
	}
	if next.status != "Interrupting…" {
		t.Fatalf("unexpected esc status: %q", next.status)
	}
	if len(next.currentSnapshot.Messages) != 1 {
		t.Fatalf("expected local interrupted notice message, got %#v", next.currentSnapshot.Messages)
	}
	if got := next.currentSnapshot.Parts[next.currentSnapshot.Messages[0].ID]; len(got) != 1 || got[0].Kind != domain.PartKindEventNotice || got[0].Body != "Interrupted" {
		t.Fatalf("expected interrupted event notice part, got %#v", got)
	}
}

func TestAppendingPromptPreservesRetainedTranscriptPrefix(t *testing.T) {
	m := App{
		cfg:              testConfig(t),
		palette:          theme.Default().Palette,
		viewport:         newTranscriptViewport(80, 18),
		renderCache:      &modelRenderCache{},
		composer:         textarea.New(),
		width:            80,
		height:           24,
		currentSnapshot:  chatpkg.Snapshot{Parts: make(map[int64][]domain.Part)},
		expandedToolRuns: make(map[string]bool),
		transcriptDirty:  true,
	}

	m.appendLocalUserPrompt("first", nil, nil)
	retained := m.ensureRetainedTranscript()
	items := retained.Items()
	if len(items) != 1 {
		t.Fatalf("expected one retained transcript item, got %d", len(items))
	}
	first := items[0].Node

	m.appendLocalUserPrompt("second", nil, nil)
	items = retained.Items()
	if len(items) != 2 {
		t.Fatalf("expected two retained transcript items, got %d", len(items))
	}
	if items[0].Node != first {
		t.Fatal("expected appending a prompt to preserve the existing retained transcript element")
	}
	if m.transcriptDirty {
		t.Fatal("expected transcript sync to clear the dirty flag after append")
	}
}

func TestUpdateLoadRefreshesCurrentSnapshotForSameChat(t *testing.T) {
	m := App{composer: textarea.New(), palette: theme.Default().Palette, width: 80, height: 24}
	first := m.UpdateLoad(loadMsg{
		current:  domain.Session{ID: 4},
		chat:     domain.Chat{ID: 7, SessionID: 4},
		messages: []domain.Message{{ID: 10, ChatID: 7, Role: domain.MessageRoleUser, Summary: "first"}},
		parts: map[int64][]domain.Part{
			10: {{ID: 11, MessageID: 10, Kind: domain.PartKindText, Payload: domain.TextPayload{Text: "first"}}},
		},
	})
	if len(first.currentSnapshot.Messages) != 1 {
		t.Fatalf("expected loaded snapshot")
	}

	second := first.UpdateLoad(loadMsg{
		current:  domain.Session{ID: 4},
		chat:     domain.Chat{ID: 7, SessionID: 4},
		messages: []domain.Message{{ID: 10, ChatID: 7, Role: domain.MessageRoleUser, Summary: "updated"}},
		parts: map[int64][]domain.Part{
			10: {{ID: 11, MessageID: 10, Kind: domain.PartKindText, Payload: domain.TextPayload{Text: "updated"}}},
		},
	})
	if len(second.currentSnapshot.Messages) != 1 {
		t.Fatalf("expected refreshed snapshot")
	}
	if got := second.currentSnapshot.Messages[0].Summary; got != "updated" {
		t.Fatalf("summary = %q", got)
	}
	if got := second.currentSnapshot.Parts[10]; len(got) != 1 || got[0].Text() != "updated" {
		t.Fatalf("parts = %#v", got)
	}
}

func TestAppendLocalUserPromptHydratesTranscriptFromSnapshot(t *testing.T) {
	m := App{
		cfg:              testConfig(t),
		palette:          theme.Default().Palette,
		viewport:         newTranscriptViewport(80, 18),
		renderCache:      &modelRenderCache{},
		composer:         textarea.New(),
		width:            80,
		height:           24,
		expandedToolRuns: make(map[string]bool),
		transcriptDirty:  true,
	}

	m.appendLocalUserPrompt("first", nil, nil)
	m.syncRetainedTranscript()

	if len(m.currentSnapshot.Messages) != 1 {
		t.Fatalf("expected current snapshot to contain prompt")
	}
	if got := m.currentSnapshot.Messages[0].Summary; got != "first" {
		t.Fatalf("summary = %q", got)
	}
	item, ok := m.transcriptItems[0].(*userMessageTranscriptItem)
	if !ok {
		t.Fatalf("expected user transcript item, got %T", m.transcriptItems[0])
	}
	if got := item.msg.Summary; got != "first" {
		t.Fatalf("item summary = %q", got)
	}
	if len(item.parts) != 1 || item.parts[0].Text() != "first" {
		t.Fatalf("item parts = %#v", item.parts)
	}
}

func TestThemeCommandOpensFilterablePicker(t *testing.T) {
	m := App{
		cfg:      config.Default(),
		composer: textarea.New(),
	}
	m.composer.SetValue("/theme")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*App)
	if cmd != nil {
		t.Fatal("expected no async command for theme picker")
	}
	if !next.hasThemeDialog() {
		t.Fatal("expected theme dialog to open")
	}
	if len(next.themeDialog.Themes) == 0 {
		t.Fatal("expected theme matches")
	}
}

func TestThemePickerFiltersAndPreviewsSelection(t *testing.T) {
	cfg := testConfig(t)
	cfg.UI.Theme = "tokyonight"

	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.openThemePicker()

	updated, _ := m.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Runes: []rune("g")})
	next := updated.(*App)
	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Runes: []rune("r")})
	next = updated.(*App)

	if len(next.themeDialog.Themes) == 0 {
		t.Fatal("expected filtered theme matches")
	}
	current, ok := next.themeDialog.Current()
	if !ok || current != "gruvbox" {
		t.Fatalf("expected gruvbox after filtering, got %#v", current)
	}
	if next.cfg.UI.Theme != "gruvbox" {
		t.Fatalf("expected live theme preview to apply gruvbox, got %q", next.cfg.UI.Theme)
	}
}

func TestSetThemeRefreshesTranscriptColors(t *testing.T) {
	cfg := testConfig(t)
	cfg.UI.Theme = "tokyonight"

	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.width = 80
	m.height = 24
	m.viewport = newTranscriptViewport(60, 12)
	m.currentSnapshot.Messages = []domain.Message{{
		ID:        1,
		Role:      domain.MessageRoleAssistant,
		Summary:   "hello",
		CreatedAt: time.Now(),
	}}
	m.currentSnapshot.Parts = map[int64][]domain.Part{
		1: {{Kind: domain.PartKindText, Body: "hello"}},
	}

	m.refreshViewport()
	before := m.viewSurface()
	beforeR, beforeG, beforeB, beforeOK := firstSurfaceFG(before)
	if !beforeOK {
		t.Fatal("expected transcript foreground color before theme change")
	}

	if err := m.setTheme("flexoki", false); err != nil {
		t.Fatal(err)
	}

	after := m.viewSurface()
	afterR, afterG, afterB, afterOK := firstSurfaceFG(after)
	if !afterOK {
		t.Fatal("expected transcript foreground color after theme change")
	}
	if beforeR == afterR && beforeG == afterG && beforeB == afterB {
		t.Fatal("expected transcript colors to update after theme change")
	}
}

func firstSurfaceFG(view ui.SurfaceView) (uint8, uint8, uint8, bool) {
	for y := 0; y < view.SurfaceHeight(); y++ {
		for x := 0; x < view.SurfaceWidth(); x++ {
			r, g, b, ok := view.SurfaceCellFG(x, y)
			if ok {
				return r, g, b, true
			}
		}
	}
	return 0, 0, 0, false
}

func TestThemePickerEscapeRestoresOriginalTheme(t *testing.T) {
	cfg := testConfig(t)
	cfg.UI.Theme = "flexoki"

	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.openThemePicker()
	m.themeDialog.Update(ui.KeyMsg{Type: ui.KeyRight})
	m.previewSelectedTheme()
	if m.cfg.UI.Theme == "flexoki" {
		t.Fatal("expected theme preview to change current theme before cancel")
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEsc})
	next := updated.(*App)
	if cmd != nil {
		t.Fatal("expected no async command on theme picker cancel")
	}
	if next.cfg.UI.Theme != "flexoki" {
		t.Fatalf("expected original theme restored, got %q", next.cfg.UI.Theme)
	}
	if next.hasThemeDialog() {
		t.Fatal("expected theme dialog to close on cancel")
	}
}

func TestThemePickerEnterSavesTheme(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.UI.Theme = "flexoki"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.openThemePicker()
	m.themeDialog.Update(ui.KeyMsg{Type: ui.KeyRight})

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*App)
	if cmd != nil {
		t.Fatal("expected no async command on theme apply")
	}
	if next.hasThemeDialog() {
		t.Fatal("expected theme dialog to close after selection")
	}
	if next.cfg.UI.Theme == "flexoki" {
		t.Fatal("expected theme selection to persist a new theme")
	}
	if !strings.Contains(next.status, "Theme set to") {
		t.Fatalf("expected status update after theme apply, got %q", next.status)
	}
}

func TestMouseClickOnThemePickerOKAppliesSelection(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.UI.Theme = "flexoki"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.mouseEnabled = true
	m.width = 100
	m.height = 28
	m.openThemePicker()
	m.themeDialog.Query = "gr"
	m.themeDialog.SetCurrentValue("gruvbox")
	m.themeDialog.Update(ui.KeyMsg{Type: ui.KeyTab})
	m.previewSelectedTheme()
	bounds := m.centeredWindowBounds(m.renderThemeDialogElement())
	runtime := ui.Runtime{}
	ctx := &ui.Context{Palette: m.palette, Runtime: &runtime}
	element := m.renderThemeDialogElement()
	if element == nil {
		t.Fatal("expected theme dialog element")
	}
	_ = ui.PaintNodeSurface(ctx, element, ui.Rect{W: bounds.W, H: bounds.H})
	var okControl ui.Control
	found := false
	for _, control := range runtime.Controls() {
		if control.ID == "ok" {
			okControl = control
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected OK control to be registered")
	}
	if action := m.themeDialog.ActivateControl("ok"); action.Kind != dialogs.ThemeDialogActionSelect {
		t.Fatalf("expected OK control to select current item, got %#v", action)
	}
	okX := bounds.X + okControl.Rect.X + 1
	okY := bounds.Y + okControl.Rect.Y
	if control, ok := runtime.Hit(ui.Point{X: okControl.Rect.X, Y: okControl.Rect.Y}); !ok || control.ID != "ok" {
		t.Fatalf("expected local hit to resolve OK control, got %#v %v", control, ok)
	}

	updated, _ := m.Update(ui.MouseMsg{
		Action: ui.MouseActionPress,
		Button: ui.MouseButtonLeft,
		X:      okX,
		Y:      okY,
	})
	next := asModelPtr(t, updated)
	if next.hasThemeDialog() {
		t.Fatalf("expected theme dialog to close after clicking OK, status=%q theme=%q", next.status, next.cfg.UI.Theme)
	}
	if next.cfg.UI.Theme != "gruvbox" {
		t.Fatalf("expected theme selection to persist gruvbox, got %q", next.cfg.UI.Theme)
	}
	if !strings.Contains(next.status, "Theme set to") {
		t.Fatalf("expected status update after clicking OK, got %q", next.status)
	}
}

func TestPrefsCommandOpensPreferencesDialog(t *testing.T) {
	m := App{
		cfg:      config.Default(),
		composer: textarea.New(),
	}
	m.composer.SetValue("/preferences")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected spinner tick command when opening preferences")
	}
	if !next.hasPreferencesDialog() {
		t.Fatal("expected preferences dialog to open")
	}
}

func TestToolsCommandOpensToolsDialog(t *testing.T) {
	m := App{
		cfg:      config.Default(),
		composer: textarea.New(),
	}
	m.composer.SetValue("/tools")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected title sync command when opening tools dialog")
	}
	if !next.hasToolsDialog() {
		t.Fatal("expected tools dialog to open")
	}
}

func TestApplySessionToolStatesUpdatesDraftSession(t *testing.T) {
	m := App{cfg: config.Default()}

	err := m.applySessionToolStates(map[domain.ToolKind]bool{
		domain.ToolKindRead: true,
		domain.ToolKindBash: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.currentSession.ToolStates[domain.ToolKindBash] {
		t.Fatalf("expected bash disabled in draft session, got %#v", m.currentSession.ToolStates)
	}
}

func TestProviderCommandOpensProviderDialog(t *testing.T) {
	m := App{
		cfg:      config.Default(),
		composer: textarea.New(),
	}
	m.composer.SetValue("/provider")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected title sync command when opening provider dialog")
	}
	if !next.hasProviderDialog() {
		t.Fatal("expected provider dialog to open")
	}
}

func TestDisconnectAliasOpensProviderDialog(t *testing.T) {
	m := App{
		cfg: config.Config{
			Providers: map[string]config.Provider{
				"openai": {Name: "OpenAI", BaseURL: "https://api.openai.com/v1"},
			},
		},
		composer: textarea.New(),
	}
	m.composer.SetValue("/disconnect")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected title sync command when opening provider dialog")
	}
	if !next.hasProviderDialog() {
		t.Fatal("expected provider dialog to open")
	}
}

func TestConnectAliasOpensProviderDialog(t *testing.T) {
	m := App{
		cfg:      config.Default(),
		composer: textarea.New(),
	}
	m.composer.SetValue("/connect")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected title sync command")
	}
	if !next.hasProviderDialog() {
		t.Fatal("expected provider dialog to open")
	}
}

func TestModelCommandWithoutProviderShowsStatus(t *testing.T) {
	m := App{
		cfg:      config.Default(),
		composer: textarea.New(),
	}
	m.composer.SetValue("/model")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected title sync command")
	}
	if next.status != "Configure a provider first with /provider" {
		t.Fatalf("unexpected status: %q", next.status)
	}
}

func TestModelCommandLoadsModelsAcrossProviders(t *testing.T) {
	cfg := testConfig(t)
	cfg.DefaultProvider = "openai"
	cfg.DefaultModel = "gpt-5.4"
	cfg.Providers = map[string]config.Provider{
		"openai": {
			Name:         "OpenAI",
			Kind:         "openai-compatible",
			AuthMethod:   "api_key",
			BaseURL:      "https://api.openai.com/v1",
			APIKey:       "secret",
			DefaultModel: "gpt-5.4",
		},
	}
	m := App{
		cfg:      cfg,
		composer: textarea.New(),
	}
	m.composer.SetValue("/model")

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected async model load command")
	}
	if next.status != "Loading models…" {
		t.Fatalf("unexpected status: %q", next.status)
	}
}

func TestSaveProviderDraftPersistsDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	m := App{cfg: cfg}
	draft, err := provider.BuildDraft("openai", nil)
	if err != nil {
		t.Fatal(err)
	}
	draft.APIKey = "secret"
	draft.Model = "gpt-5.4"

	if err := m.saveProviderDraft(draft); err != nil {
		t.Fatal(err)
	}
	if !m.cfg.HasUsableDefaultProvider() {
		t.Fatal("expected usable default provider after save")
	}
	if got := m.cfg.DefaultModel; got != "gpt-5.4" {
		t.Fatalf("unexpected default model: %q", got)
	}
	reloaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.DefaultProvider != "openai" {
		t.Fatalf("unexpected saved default provider: %q", reloaded.DefaultProvider)
	}
}

func TestSaveProviderDraftDefaultsContextWindowWhenUnset(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	m := App{cfg: cfg}
	draft := provider.ConnectDraft{
		ProviderID: "openai-compatible",
		Kind:       provider.ProviderKindCompatible,
		Name:       "Compatible",
		BaseURL:    "http://127.0.0.1:8888/v1",
		Model:      "coder.gguf",
	}

	if err := m.saveProviderDraft(draft); err != nil {
		t.Fatal(err)
	}
	providerCfg, ok := m.cfg.Provider("openai-compatible")
	if !ok {
		t.Fatal("expected provider config to be saved")
	}
	if providerCfg.ContextWindow != 32768 {
		t.Fatalf("expected default context window 32768, got %d", providerCfg.ContextWindow)
	}
}

func TestEnsureRuntimeContextWindowDetectsAndPersistsCompatibleLocalProvider(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"coder.gguf","max_model_len":131072}]}`))
		case "/props":
			if got := r.URL.Query().Get("model"); got != "coder.gguf" {
				t.Fatalf("unexpected model query: %q", got)
			}
			_, _ = w.Write([]byte(`{"default_generation_settings":{"n_ctx":131072}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	cfg.Providers = map[string]config.Provider{
		"openai-compatible": {
			Name:          "Compatible",
			Kind:          "openai-compatible",
			AuthMethod:    "local_endpoint",
			BaseURL:       server.URL + "/v1",
			DefaultModel:  "coder.gguf",
			ContextWindow: 32768,
		},
	}
	m := App{cfg: cfg, runtimeCtxChecked: map[string]bool{}}
	session := domain.Session{ProviderID: "openai-compatible", ModelID: "coder.gguf"}

	providerID, contextWindow, checked, err := m.ensureRuntimeContextWindow(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if providerID != "openai-compatible" || !checked {
		t.Fatalf("unexpected runtime detection result: provider=%q checked=%v", providerID, checked)
	}
	if contextWindow != 131072 {
		t.Fatalf("expected detected context window 131072, got %d", contextWindow)
	}
	providerCfg, ok := m.cfg.Provider("openai-compatible")
	if !ok || providerCfg.ContextWindow != 131072 {
		t.Fatalf("expected detected context window to persist, got %#v", providerCfg)
	}
}

func TestEnsureRuntimeContextWindowDetectsAndPersistsCompatibleAPIKeyProvider(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models", "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"Lorbus/Qwen3.6-27B-int4-AutoRound","max_model_len":262144}]}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	cfg.Providers = map[string]config.Provider{
		"openai": {
			Name:          "OpenAI Compatible",
			Kind:          "openai-compatible",
			AuthMethod:    "api_key",
			BaseURL:       server.URL + "/v1",
			APIKey:        "test-key",
			DefaultModel:  "Lorbus/Qwen3.6-27B-int4-AutoRound",
			ContextWindow: 32768,
		},
	}
	m := App{cfg: cfg, runtimeCtxChecked: map[string]bool{}}
	session := domain.Session{ProviderID: "openai", ModelID: "Lorbus/Qwen3.6-27B-int4-AutoRound"}

	providerID, contextWindow, checked, err := m.ensureRuntimeContextWindow(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if providerID != "openai" || !checked {
		t.Fatalf("unexpected runtime detection result: provider=%q checked=%v", providerID, checked)
	}
	if contextWindow != 262144 {
		t.Fatalf("expected detected context window 262144, got %d", contextWindow)
	}
	providerCfg, ok := m.cfg.Provider("openai")
	if !ok || providerCfg.ContextWindow != 262144 {
		t.Fatalf("expected detected context window to persist, got %#v", providerCfg)
	}
}

func TestEnsureRuntimeContextWindowRecordsTiming(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models", "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"model-a","max_model_len":16384}]}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	cfg.Providers = map[string]config.Provider{
		"openai": {
			Name:          "OpenAI Compatible",
			Kind:          "openai-compatible",
			BaseURL:       server.URL + "/v1",
			DefaultModel:  "model-a",
			ContextWindow: 4096,
		},
	}
	rec := debugsrv.NewRecorder()
	m := App{cfg: cfg, runtimeCtxChecked: map[string]bool{}, debug: rec}
	session := domain.Session{ID: 42, ProviderID: "openai", ModelID: "model-a"}

	_, _, _, err = m.ensureRuntimeContextWindow(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}

	events := rec.Events(session.ID)
	for _, evt := range events {
		if evt.Source == "lifecycle" && evt.Kind == "context_window_timing" {
			if evt.Text != "ensure_runtime_context_window" {
				t.Fatalf("unexpected timing text: %#v", evt)
			}
			if evt.Meta["provider_id"] != "openai" || evt.Meta["model_id"] != "model-a" {
				t.Fatalf("unexpected timing meta: %#v", evt.Meta)
			}
			if evt.Meta["duration_ms"] == "" {
				t.Fatalf("expected duration_ms in timing meta: %#v", evt.Meta)
			}
			return
		}
	}
	t.Fatal("expected context_window_timing lifecycle event")
}

func TestLoadSessionCmdRecordsChatLoadTiming(t *testing.T) {
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := st.AddMessage(context.Background(), session.ID, domain.MessageRoleAssistant, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddPart(context.Background(), msg.ID, domain.TextPayload{Text: "hello"}); err != nil {
		t.Fatal(err)
	}

	rec := debugsrv.NewRecorder()
	m := App{
		store:          st,
		debug:          rec,
		workdir:        t.TempDir(),
		startupOptions: StartupOptions{ShowAllSessions: true},
	}

	got := m.loadSessionCmd(session.ID)()
	if got == nil {
		t.Fatal("expected loadSessionCmd to return a message")
	}

	events := rec.Events(session.ID)
	var sawLoadTotal bool
	for _, evt := range events {
		if evt.Source != "lifecycle" || evt.Kind != "chat_load_timing" {
			continue
		}
		if evt.Meta["duration_ms"] == "" {
			t.Fatalf("expected duration_ms in chat load timing event: %#v", evt)
		}
		if evt.Text == "load_chat_total" {
			sawLoadTotal = true
		}
	}
	if !sawLoadTotal {
		t.Fatalf("expected loadSessionCmd to record chat_load_timing events, got %#v", events)
	}
}

func TestUpdateLoadSchedulesContextWindowDetectionForCurrentSession(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Providers = map[string]config.Provider{
		"openai-compatible": {
			Name:         "Compatible",
			Kind:         "openai-compatible",
			AuthMethod:   "local_endpoint",
			BaseURL:      "http://127.0.0.1:8888/v1",
			DefaultModel: "coder.gguf",
		},
	}
	m := App{
		cfg:               cfg,
		composer:          textarea.New(),
		runtimeCtxChecked: map[string]bool{},
	}

	load := loadMsg{
		current: domain.Session{ID: 1, ProviderID: "openai-compatible", ModelID: "coder.gguf"},
	}
	updated, cmd := m.Update(load)
	if cmd == nil {
		t.Fatal("expected follow-up command after load")
	}
	next := updated.(App)
	if next.currentSession.ProviderID != "openai-compatible" {
		t.Fatalf("unexpected current session: %#v", next.currentSession)
	}
}

func TestDisconnectProviderClearsDefaultAndFallsBack(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.DefaultProvider = "openai"
	cfg.DefaultModel = "gpt-5.4"
	cfg.Providers = map[string]config.Provider{
		"openai": {
			Name:         "OpenAI",
			Kind:         "openai-compatible",
			AuthMethod:   "api_key",
			BaseURL:      "https://api.openai.com/v1",
			DefaultModel: "gpt-5.4",
		},
		"ollama": {
			Name:         "Ollama",
			Kind:         "openai-compatible",
			AuthMethod:   "local_endpoint",
			BaseURL:      "http://127.0.0.1:11434/v1",
			DefaultModel: "qwen",
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	m := App{
		cfg:            cfg,
		currentSession: domain.Session{ProviderID: "openai", ModelID: "gpt-5.4"},
	}

	if err := m.disconnectProvider("openai"); err != nil {
		t.Fatal(err)
	}
	if m.cfg.DefaultProvider != "ollama" {
		t.Fatalf("expected fallback default provider, got %q", m.cfg.DefaultProvider)
	}
	if m.currentSession.ProviderID != "ollama" || m.currentSession.ModelID != "qwen" {
		t.Fatalf("expected current session to fall back, got %#v", m.currentSession)
	}
	reloaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Providers["openai"]; ok {
		t.Fatal("expected provider removed from saved config")
	}
}

func TestSelectModelUpdatesConfigAndCurrentSession(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.DefaultProvider = "openai"
	cfg.DefaultModel = "gpt-5.4"
	cfg.Providers = map[string]config.Provider{
		"openai": {
			Name:         "OpenAI",
			Kind:         "openai-compatible",
			AuthMethod:   "api_key",
			BaseURL:      "https://api.openai.com/v1",
			DefaultModel: "gpt-5.4",
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	session, err := st.CreateSession(context.Background(), "test", "openai", "gpt-5.4", nil)
	if err != nil {
		t.Fatal(err)
	}
	m := App{
		cfg:            cfg,
		store:          st,
		currentSession: session,
	}

	if err := m.selectModel("openai", "gpt-4.1-mini", provider.ModelPresetDefault); err != nil {
		t.Fatal(err)
	}
	if m.cfg.DefaultProvider != "openai" || m.cfg.DefaultModel != "gpt-4.1-mini" || m.currentSession.ProviderID != "openai" || m.currentSession.ModelID != "gpt-4.1-mini" {
		t.Fatalf("unexpected model selection state: provider=%q cfg=%q sessionProvider=%q sessionModel=%q", m.cfg.DefaultProvider, m.cfg.DefaultModel, m.currentSession.ProviderID, m.currentSession.ModelID)
	}
	if got := m.cfg.Providers["openai"].ModelPreset; got != provider.ModelPresetDefault {
		t.Fatalf("expected model preset to persist, got %q", got)
	}
	reloaded, err := st.GetSession(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ModelID != "gpt-4.1-mini" {
		t.Fatalf("expected persisted session model, got %q", reloaded.ModelID)
	}
}

func TestSelectModelSwitchesCurrentSessionProvider(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.DefaultProvider = "openai"
	cfg.DefaultModel = "gpt-5.4"
	cfg.Providers = map[string]config.Provider{
		"openai": {
			Name:         "OpenAI",
			Kind:         "openai-compatible",
			AuthMethod:   "api_key",
			BaseURL:      "https://api.openai.com/v1",
			DefaultModel: "gpt-5.4",
		},
		"groq": {
			Name:         "Groq",
			Kind:         "openai-compatible",
			AuthMethod:   "api_key",
			BaseURL:      "https://api.groq.com/openai/v1",
			DefaultModel: "llama-3.3-70b-versatile",
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	session, err := st.CreateSession(context.Background(), "test", "openai", "gpt-5.4", nil)
	if err != nil {
		t.Fatal(err)
	}
	m := App{
		cfg:            cfg,
		store:          st,
		currentSession: session,
	}

	if err := m.selectModel("groq", "llama-3.3-70b-versatile", provider.ModelPresetDefault); err != nil {
		t.Fatal(err)
	}
	if m.currentSession.ProviderID != "groq" || m.currentSession.ModelID != "llama-3.3-70b-versatile" {
		t.Fatalf("expected session provider/model switch, got provider=%q model=%q", m.currentSession.ProviderID, m.currentSession.ModelID)
	}
	if m.cfg.DefaultProvider != "groq" || m.cfg.DefaultModel != "llama-3.3-70b-versatile" {
		t.Fatalf("expected default provider/model switch, got provider=%q model=%q", m.cfg.DefaultProvider, m.cfg.DefaultModel)
	}
	reloaded, err := st.GetSession(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ProviderID != "groq" || reloaded.ModelID != "llama-3.3-70b-versatile" {
		t.Fatalf("expected persisted session provider/model switch, got provider=%q model=%q", reloaded.ProviderID, reloaded.ModelID)
	}
}

func TestModelListMsgOpensModelDialog(t *testing.T) {
	m := App{}
	updated, _ := m.Update(modelListMsg{
		providerID: "openai",
		models: []domain.Model{
			{ID: "gpt-5.4", OwnedBy: "openai"},
			{ID: "gpt-4.1-mini", OwnedBy: "openai"},
		},
	})
	next := updated.(App)
	if !next.hasModelDialog() {
		t.Fatal("expected model dialog to open")
	}
}

func TestOpenModelDialogUsesProviderPreset(t *testing.T) {
	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{
		"openai": {
			BaseURL:      "https://api.openai.com/v1",
			APIKey:       "secret",
			DefaultModel: "Qwen/Qwen3.6-35B-A3B",
			ModelPreset:  provider.ModelPresetQwen36PreserveThinking,
		},
	}
	cfg.DefaultProvider = "openai"
	cfg.DefaultModel = "Qwen/Qwen3.6-35B-A3B"

	m := App{cfg: cfg}
	m.openModelDialog(singleProviderModelEntries("openai", "openai", []domain.Model{{ID: "Qwen/Qwen3.6-35B-A3B"}}))
	if m.modelDialog == nil {
		t.Fatal("expected model dialog")
	}
	if m.modelDialog.PresetID != provider.ModelPresetQwen36PreserveThinking {
		t.Fatalf("expected persisted provider preset, got %q", m.modelDialog.PresetID)
	}
}

func TestPreferencesDialogCancelRestoresOriginalUI(t *testing.T) {
	cfg := testConfig(t)
	cfg.UI.Theme = "flexoki"

	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.openPreferencesDialog()

	updated, _ := m.handleKey(ui.KeyMsg{Type: ui.KeyShiftTab})
	next := updated.(*App)
	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyRight})
	next = updated.(*App)
	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyTab})
	next = updated.(*App)
	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyRight})
	next = updated.(*App)
	if next.cfg.UI.Theme == "flexoki" {
		t.Fatal("expected preferences preview to change current theme")
	}

	updated, cmd := next.handleKey(ui.KeyMsg{Type: ui.KeyEsc})
	next = updated.(*App)
	if cmd == nil {
		t.Fatal("expected title sync command when cancelling preferences")
	}
	if next.cfg.UI.Theme != "flexoki" {
		t.Fatalf("expected original theme restored, got %q", next.cfg.UI.Theme)
	}
	if next.hasPreferencesDialog() {
		t.Fatal("expected preferences dialog to close on cancel")
	}
}

func TestPreferencesDialogApplySavesUIConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.UI.Theme = "flexoki"
	cfg.UI.ShowSidebar = true
	cfg.MaxToolLoopSteps = 500
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.openPreferencesDialog()

	updated, _ := m.handleKey(ui.KeyMsg{Type: ui.KeyShiftTab})
	next := updated.(*App)
	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyRight})
	next = updated.(*App)
	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyTab})
	next = updated.(*App)
	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyRight})
	next = updated.(*App)
	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyTab})
	next = updated.(*App)
	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next = updated.(*App)

	if next.hasPreferencesDialog() {
		t.Fatal("expected preferences dialog to close after apply")
	}
	if next.cfg.UI.Theme == "flexoki" {
		t.Fatal("expected preferences apply to persist a different theme")
	}
	if next.cfg.MaxToolLoopSteps != 500 {
		t.Fatalf("expected tool loop limit unchanged, got %d", next.cfg.MaxToolLoopSteps)
	}
	reloaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.UI.Theme != next.cfg.UI.Theme {
		t.Fatalf("expected saved theme %q, got %q", next.cfg.UI.Theme, reloaded.UI.Theme)
	}
	if reloaded.MaxToolLoopSteps != next.cfg.MaxToolLoopSteps {
		t.Fatalf("expected saved tool loop limit %d, got %d", next.cfg.MaxToolLoopSteps, reloaded.MaxToolLoopSteps)
	}
}

func TestPreferencesDialogApplySavesToolLoopLimit(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.openPreferencesDialog()

	updated, _ := m.handleKey(ui.KeyMsg{Type: ui.KeyRight})
	next := updated.(*App)
	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyTab})
	next = updated.(*App)
	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next = updated.(*App)

	if next.cfg.MaxToolLoopSteps != 501 {
		t.Fatalf("expected tool loop limit incremented to 501, got %d", next.cfg.MaxToolLoopSteps)
	}
	reloaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.MaxToolLoopSteps != 501 {
		t.Fatalf("expected saved tool loop limit 501, got %d", reloaded.MaxToolLoopSteps)
	}
}

func TestPreferencesDialogApplySavesCompactionPreferences(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.openPreferencesDialog()

	nextValues := m.preferences.DraftValues()
	nextValues.AutoCompactAt = 75
	nextValues.CompactionKeepToolBatches = 4
	cmd, err := m.applyPreferences(nextValues, true)
	if err != nil {
		t.Fatal(err)
	}
	if cmd != nil {
		_ = cmd()
	}
	if m.cfg.AutoCompactAt != 75 {
		t.Fatalf("expected auto compact threshold 75, got %d", m.cfg.AutoCompactAt)
	}
	if m.cfg.CompactionKeepToolBatches != 4 {
		t.Fatalf("expected kept tool batches 4, got %d", m.cfg.CompactionKeepToolBatches)
	}
	reloaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.AutoCompactAt != 75 {
		t.Fatalf("expected saved auto compact threshold 75, got %d", reloaded.AutoCompactAt)
	}
	if reloaded.CompactionKeepToolBatches != 4 {
		t.Fatalf("expected saved kept tool batches 4, got %d", reloaded.CompactionKeepToolBatches)
	}
}

func TestWorkingIndicatorShownWhenModelWorking(t *testing.T) {
	m := App{
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
			spinner: spinnerModel{
				active: true,
			},
		},
	}

	got := m.workingIndicator()
	if got == "" {
		t.Fatal("expected working indicator while model is working")
	}
}

func TestRenderHeaderIsEmpty(t *testing.T) {
	m := App{
		currentSession: domain.Session{ID: 2, ProviderID: "test", ModelID: "model"},
		status:         "Waiting for model…",
	}

	got := m.renderHeader()
	if got != "" {
		t.Fatalf("expected empty header, got %q", got)
	}
}

func TestRenderSidebarShowsStatusAndSessionInfo(t *testing.T) {
	m := App{
		currentSession: domain.Session{ID: 2, Title: "Testing Session", ProviderID: "test", ModelID: "model", PermissionProfile: "default", ProjectChecksum: "agents-1"},
		currentChat:    domain.Chat{ID: 7, Title: "Maze Fix", WorkflowRole: domain.WorkflowRoleExecution},
		chats: []domain.Chat{
			{ID: 7, Title: "Maze Fix", WorkflowRole: domain.WorkflowRoleExecution},
			{ID: 8, Title: "Review"},
		},
		status: "Working ...",
		debug: func() *debugsrv.Recorder {
			rec := debugsrv.NewRecorder()
			rec.SetDebugAPI("127.0.0.1:61347")
			return rec
		}(),
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
			spinner: spinnerModel{
				active: true,
			},
		},
		workdir: "/tmp/project",
		workspace: workspace.Status{
			AgentsFiles:    1,
			AgentsChecksum: "agents-1",
		},
		cfg: config.Config{
			Providers: map[string]config.Provider{
				"test": {ContextWindow: 32768},
			},
		},
		milestonePlan: store.MilestonePlan{
			Milestones: []store.Milestone{
				{Ref: "investigate", Title: "Investigate", Status: domain.MilestoneStatusCompleted, Position: 0},
				{Ref: "implement", Title: "Implement", Status: domain.MilestoneStatusInProgress, Position: 1},
				{Ref: "ship", Title: "Ship", Status: domain.MilestoneStatusPending, Position: 2},
			},
		},
		todos: []store.TodoItem{
			{ID: 1, Content: "Write tests", Status: domain.TodoStatusCompleted},
			{ID: 2, Content: "Fix bug", Status: domain.TodoStatusInProgress},
			{ID: 3, Content: "Polish copy", Status: domain.TodoStatusPending},
		},
		currentSnapshot: chatpkg.Snapshot{Messages: []domain.Message{{ID: 1}}, Parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindSystemNotice, Body: "usage", MetaJSON: `{"PromptTokens":4096,"CompletionTokens":2048,"CachedTokens":1024,"TotalTokens":8192}`}},
		}},
		contextTokens:          8192,
		contextTokensEstimated: true,
	}
	m.syncUsageFromHistory()
	m.busy.setTranscriptPhase(transcriptBusyPhaseWaiting)

	got := m.renderSidebar()
	if !strings.Contains(got, "Session #2") || !strings.Contains(got, "Testing Session") || !strings.Contains(got, "Model  test / model") {
		t.Fatalf("expected sidebar to include session details, got %q", got)
	}
	if !strings.Contains(got, "Chat    #7  1/2  execution  1 msg  Maze Fix") {
		t.Fatalf("expected sidebar to include compact chat details, got %q", got)
	}
	if !strings.Contains(got, "Waiting for LLM response") {
		t.Fatalf("expected sidebar to include status, got %q", got)
	}
	if !strings.Contains(got, "Help    Alt-H help  Ctrl-S toggle  Alt+, wide  Alt+. narrow") {
		t.Fatalf("expected sidebar to include help hint, got %q", got)
	}
	if strings.Contains(got, "enter send/select") || strings.Contains(got, "/provider") {
		t.Fatalf("expected sidebar to omit detailed hotkeys and commands, got %q", got)
	}
	if !strings.Contains(got, "Context ~8.2k / 32.8k (25%)") {
		t.Fatalf("expected sidebar to include context usage, got %q", got)
	}
	if !strings.Contains(got, "Tokens in 4.1k  out 2.0k  cache 1.0k") {
		t.Fatalf("expected sidebar to include token totals, got %q", got)
	}
	if !strings.Contains(got, "Debug   127.0.0.1:61347") {
		t.Fatalf("expected sidebar to include debug api status, got %q", got)
	}
	if !strings.Contains(got, "AGENTS   Up to date") {
		t.Fatalf("expected sidebar to include AGENTS summary, got %q", got)
	}
	for _, needle := range []string{
		"\n\nModel  test / model\nStatus ",
		"\n\nWorkspace /tmp/project",
		"\n\nMilestones: 1/3 done\n  ✓ Investigate\n  ◐ Implement\n  ○ Ship",
		"\n\nTodos: 1/3 done\n  ✓ Write tests\n  ◐ Fix bug\n  ○ Polish copy",
		"\n\nChats",
		"\n\nDebug   127.0.0.1:61347",
		"\n\nHelp    Alt-H help  Ctrl-S toggle  Alt+, wide  Alt+. narrow",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected sidebar to include %q, got %q", needle, got)
		}
	}
	if strings.Contains(got, "saved   ") || strings.Contains(got, "live    ") || strings.Contains(got, "files   ") {
		t.Fatalf("expected sidebar to omit AGENTS checksum details, got %q", got)
	}
	if strings.Contains(got, "Pending approvals") {
		t.Fatalf("expected sidebar to omit pending approvals, got %q", got)
	}
	if strings.Contains(got, "Profile ") || strings.Contains(got, "profile ") || strings.Contains(got, "mouse   ") {
		t.Fatalf("expected sidebar to omit session profile and mouse status, got %q", got)
	}
}

func TestSidebarUsageUpdatesFromLiveUsageEvent(t *testing.T) {
	m := App{
		width:  120,
		height: 40,
		currentSession: domain.Session{
			ID:         2,
			Title:      "Testing Session",
			ProviderID: "test",
			ModelID:    "model",
		},
		currentChat: domain.Chat{ID: 7, SessionID: 2, LastKnownContextTokens: 1500, ContextTokensKnown: false},
		showSidebar: true,
		cfg: config.Config{
			Providers: map[string]config.Provider{
				"test": {ContextWindow: 32768},
			},
		},
		currentSnapshot: chatpkg.Snapshot{Messages: []domain.Message{{ID: 1}}, Parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindSystemNotice, Body: "usage", MetaJSON: `{"PromptTokens":1000,"CompletionTokens":500,"CachedTokens":100,"TotalTokens":1500}`}},
		}},
	}
	m.syncUsageFromHistory()
	m.syncContextFromChat()

	before := m.renderSidebar()
	if !strings.Contains(before, "Context ~1.5k / 32.8k (4%)") {
		t.Fatalf("expected hydrated context usage before live event, got %q", before)
	}
	if !strings.Contains(before, "Tokens in 1.0k  out 500  cache 100") {
		t.Fatalf("expected hydrated token totals before live event, got %q", before)
	}

	m.applyEvent(domain.Event{Kind: domain.EventKindUsage, Usage: domain.Usage{
		PromptTokens:     200,
		CompletionTokens: 50,
		CachedTokens:     20,
		TotalTokens:      250,
	}})

	after := m.renderSidebar()
	if !strings.Contains(after, "Context 200 / 32.8k (0%)") {
		t.Fatalf("expected precise context after live usage event, got %q", after)
	}
	if !strings.Contains(after, "Tokens in 1.2k  out 550  cache 120") {
		t.Fatalf("expected live token totals after usage event, got %q", after)
	}
}

func TestSidebarUsageWithoutPromptTokensPreservesEstimate(t *testing.T) {
	m := App{
		width:  120,
		height: 40,
		currentSession: domain.Session{
			ID:         2,
			Title:      "Testing Session",
			ProviderID: "test",
			ModelID:    "model",
		},
		currentChat: domain.Chat{ID: 7, SessionID: 2},
		showSidebar: true,
		cfg: config.Config{
			Providers: map[string]config.Provider{
				"test": {ContextWindow: 32768},
			},
		},
		contextTokens:          1500,
		contextTokensEstimated: true,
	}

	m.applyEvent(domain.Event{Kind: domain.EventKindUsage, Usage: domain.Usage{
		CompletionTokens: 300,
		CachedTokens:     100,
	}})

	got := m.renderSidebar()
	if !strings.Contains(got, "Context ~1.5k / 32.8k (4%)") {
		t.Fatalf("expected missing prompt tokens to preserve estimate, got %q", got)
	}
	if !strings.Contains(got, "Tokens in 0  out 300  cache 100") {
		t.Fatalf("expected live token totals, got %q", got)
	}
}

func TestSidebarAlwaysShowsContextLineWithoutProvider(t *testing.T) {
	m := App{
		width:       120,
		height:      40,
		currentChat: domain.Chat{ID: 7},
		showSidebar: true,
	}

	got := m.renderSidebar()
	if !strings.Contains(got, "Context - / -") {
		t.Fatalf("expected placeholder context line, got %q", got)
	}
}

func TestUpdateLoadEstimatesContextForNewChat(t *testing.T) {
	cfg := testConfig(t)
	cfg.DefaultProvider = "test"
	cfg.Providers = map[string]config.Provider{
		"test": {ContextWindow: 32768},
	}
	workdir := t.TempDir()
	engine := agent.New(cfg, nil, tools.NewRegistry(workdir), nil, workdir)

	m := App{
		cfg:         cfg,
		agent:       engine,
		composer:    textarea.New(),
		showSidebar: true,
		width:       120,
		height:      40,
	}

	load := loadMsg{
		current: domain.Session{
			ID:             1,
			ProviderID:     "test",
			ModelID:        "test-model",
			AgentsResolved: "## Repo\n- Keep changes minimal.",
		},
		chat:  domain.Chat{ID: 7, SessionID: 1, ProviderID: "test", ModelID: "test-model"},
		parts: map[int64][]domain.Part{},
	}
	m = m.UpdateLoad(load)

	if m.contextTokens <= 0 {
		t.Fatalf("expected context estimate after load, got %d", m.contextTokens)
	}
	if !m.contextTokensEstimated {
		t.Fatal("expected loaded context estimate to be marked estimated")
	}
	got := m.renderSidebar()
	if !strings.Contains(got, "Context ~") || !strings.Contains(got, "/ 32.8k (") {
		t.Fatalf("expected estimated context line after load, got %q", got)
	}
}

func TestSidebarContextAccumulatesStreamedTokenEstimate(t *testing.T) {
	m := App{
		width:          120,
		height:         40,
		currentSession: domain.Session{ID: 2, ProviderID: "test", ModelID: "model"},
		currentChat:    domain.Chat{ID: 7, SessionID: 2, LastKnownContextTokens: 1000, ContextTokensKnown: true},
		showSidebar:    true,
		cfg: config.Config{
			Providers: map[string]config.Provider{
				"test": {ContextWindow: 32768},
			},
		},
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
	}
	m.syncContextFromChat()

	m.applyEvent(domain.Event{Kind: domain.EventKindMessageDelta, Text: "one two"})
	m.applyEvent(domain.Event{Kind: domain.EventKindReasoning, Text: "thinking now"})

	got := m.renderSidebar()
	if !strings.Contains(got, "Context ~1.0k / 32.8k (3%)") {
		t.Fatalf("expected streamed estimate to be included, got %q", got)
	}
	m.applyEvent(domain.Event{Kind: domain.EventKindUsage, Usage: domain.Usage{PromptTokens: 1200, CompletionTokens: 25, TotalTokens: 1225}})
	got = m.renderSidebar()
	if !strings.Contains(got, "Context ~1.2k / 32.8k (3%)") {
		t.Fatalf("expected pending streamed output to keep context estimated until message persistence, got %q", got)
	}
	if m.currentChat.LastKnownContextTokens != 1200 || !m.currentChat.ContextTokensKnown {
		t.Fatalf("expected current chat usage updated, got %#v", m.currentChat)
	}
}

func TestSidebarUsageAccumulatesMultipleLiveUsageEvents(t *testing.T) {
	m := App{
		width:          120,
		height:         40,
		currentSession: domain.Session{ID: 2, ProviderID: "test", ModelID: "model"},
		currentChat:    domain.Chat{ID: 7, SessionID: 2, LastKnownContextTokens: 1000, ContextTokensKnown: false},
		showSidebar:    true,
		cfg: config.Config{
			Providers: map[string]config.Provider{
				"test": {ContextWindow: 32768},
			},
		},
	}
	m.syncContextFromChat()

	m.applyEvent(domain.Event{Kind: domain.EventKindUsage, Usage: domain.Usage{PromptTokens: 200, CompletionTokens: 50, TotalTokens: 250}})
	m.applyEvent(domain.Event{Kind: domain.EventKindUsage, Usage: domain.Usage{CompletionTokens: 30, TotalTokens: 30}})

	got := m.renderSidebar()
	if !strings.Contains(got, "Context 200 / 32.8k (0%)") {
		t.Fatalf("expected latest prompt usage to anchor chat context, got %q", got)
	}
	if !strings.Contains(got, "Tokens in 200  out 80") {
		t.Fatalf("expected live usage to accumulate across multiple events, got %q", got)
	}
}

func TestSidebarContextUsesCompactedChatEstimateNotCompactionRequestUsage(t *testing.T) {
	workdir := t.TempDir()
	engine := agent.New(testConfig(t), nil, tools.NewRegistry(workdir), nil, workdir)
	m := App{
		width:  120,
		height: 40,
		agent:  engine,
		currentSession: domain.Session{
			ID:         33,
			Title:      "Testing Session",
			ProviderID: "test",
			ModelID:    "model",
		},
		currentChat: domain.Chat{ID: 24, SessionID: 33, WorkflowRole: domain.WorkflowRoleGeneral, LastKnownContextTokens: 42, ContextTokensKnown: false},
		showSidebar: true,
		cfg: config.Config{
			Providers: map[string]config.Provider{
				"test": {ContextWindow: 32768},
			},
		},
		currentSnapshot: chatpkg.Snapshot{Messages: []domain.Message{{ID: 1, Role: domain.MessageRoleAssistant, Summary: "Compacted session summary"}}, Parts: map[int64][]domain.Part{
			1: {
				{
					Kind: domain.PartKindCompaction,
					Body: "## Goal\nFix the centering bug.\n\n## Constraints\nKeep changes minimal.\n\n## Current State\nOnly the compacted summary remains.\n",
				},
				{
					Kind:     domain.PartKindSystemNotice,
					Body:     "usage",
					MetaJSON: `{"PromptTokens":25023,"CompletionTokens":625,"CachedTokens":0,"TotalTokens":25648}`,
				},
			},
		}},
	}
	m.syncUsageFromHistory()
	m.syncContextFromChat()

	got := m.renderSidebar()
	if !strings.Contains(got, "Context ") {
		t.Fatalf("expected context estimate, got %q", got)
	}
	if strings.Contains(got, "Context ~25.6k") {
		t.Fatalf("expected context estimate to ignore compaction-request usage, got %q", got)
	}
	if !strings.Contains(got, "Tokens in 25.0k  out 625") {
		t.Fatalf("expected token totals to preserve compaction request accounting, got %q", got)
	}
}

func TestSidebarContextAnchorsOnLatestUsageAndEstimatesTail(t *testing.T) {
	m := App{
		width:          120,
		height:         40,
		currentSession: domain.Session{ID: 2, ProviderID: "test", ModelID: "model"},
		currentChat:    domain.Chat{ID: 7, SessionID: 2, LastKnownContextTokens: 3000, ContextTokensKnown: true},
		showSidebar:    true,
		cfg: config.Config{
			Providers: map[string]config.Provider{
				"test": {ContextWindow: 32768},
			},
		},
		currentSnapshot: chatpkg.Snapshot{Messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleAssistant},
			{ID: 2, Role: domain.MessageRoleUser},
			{ID: 3, Role: domain.MessageRoleAssistant},
		}, Parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindUsage, Payload: domain.UsagePayload{Usage: domain.Usage{PromptTokens: 3000, CompletionTokens: 120, TotalTokens: 3120}}}},
			2: {{Kind: domain.PartKindText, Payload: domain.TextPayload{Text: "please inspect these three files"}}},
			3: {{Kind: domain.PartKindToolCall, Payload: domain.ToolCallPayload{Tool: domain.ToolKindRead, ToolCallID: "call_1", Args: map[string]string{"path": "cmd/app.go"}}}},
		}},
	}

	m.syncContextFromChat()

	got := m.renderSidebar()
	if !strings.Contains(got, "Context ~3.0k / 32.8k (9%)") {
		t.Fatalf("expected anchored tail estimate in sidebar, got %q", got)
	}
	if m.contextTokens <= 3000 {
		t.Fatalf("expected tail tokens added to anchor, got %d", m.contextTokens)
	}
}

func TestSidebarMilestonesAndTodosRenderNoneWhenEmpty(t *testing.T) {
	m := App{}
	got := m.renderSidebar()
	if !strings.Contains(got, "Milestones: None") {
		t.Fatalf("expected empty milestones line, got %q", got)
	}
	if !strings.Contains(got, "Todos: None") {
		t.Fatalf("expected empty todos line, got %q", got)
	}
}

func TestSidebarWidthHotkeysAdjustWidth(t *testing.T) {
	m := App{
		showSidebar:          true,
		width:                120,
		height:               30,
		sidebarWidthOverride: 34,
		viewport:             newTranscriptViewport(80, 10),
	}

	start := m.sidebarWidth()
	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Alt: true, Runes: []rune("[")})
	if cmd != nil {
		t.Fatalf("expected no command from sidebar shrink hotkey, got %#v", cmd)
	}
	next := updated.(*App)
	if next.sidebarWidth() >= start {
		t.Fatalf("expected sidebar width to shrink, start=%d next=%d", start, next.sidebarWidth())
	}
	shrunk := next.sidebarWidth()

	updated, cmd = next.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Alt: true, Runes: []rune("]")})
	if cmd != nil {
		t.Fatalf("expected no command from sidebar grow hotkey, got %#v", cmd)
	}
	grown := updated.(*App)
	if grown.sidebarWidth() <= shrunk {
		t.Fatalf("expected sidebar width to grow, prev=%d next=%d", shrunk, grown.sidebarWidth())
	}

	beforeWidenAlt := grown.sidebarWidth()
	updated, cmd = grown.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Alt: true, Runes: []rune(",")})
	if cmd != nil {
		t.Fatalf("expected no command from alternate sidebar grow hotkey, got %#v", cmd)
	}
	widenedAlt := updated.(*App)
	if widenedAlt.sidebarWidth() <= beforeWidenAlt {
		t.Fatalf("expected alt+, to grow sidebar width, start=%d next=%d", beforeWidenAlt, widenedAlt.sidebarWidth())
	}

	beforeNarrowAlt := widenedAlt.sidebarWidth()
	updated, cmd = widenedAlt.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Alt: true, Runes: []rune(".")})
	if cmd != nil {
		t.Fatalf("expected no command from alternate sidebar shrink hotkey, got %#v", cmd)
	}
	narrowedAlt := updated.(*App)
	if narrowedAlt.sidebarWidth() >= beforeNarrowAlt {
		t.Fatalf("expected alt+. to shrink sidebar width, prev=%d next=%d", beforeNarrowAlt, narrowedAlt.sidebarWidth())
	}
}

func TestSidebarWidthHotkeysPersistPreference(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.showSidebar = true
	m.width = 120
	m.height = 30
	m.viewport = newTranscriptViewport(80, 10)

	updated, _ := m.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Alt: true, Runes: []rune(",")})
	next := updated.(*App)
	if next.cfg.UI.SidebarWidth != next.sidebarWidth() {
		t.Fatalf("expected saved sidebar width to match live width, got saved=%d live=%d", next.cfg.UI.SidebarWidth, next.sidebarWidth())
	}

	reloaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.UI.SidebarWidth != next.sidebarWidth() {
		t.Fatalf("expected reloaded sidebar width %d, got %d", next.sidebarWidth(), reloaded.UI.SidebarWidth)
	}
}

func TestSidebarWidthHotkeysGrowRenderedSidebarColumn(t *testing.T) {
	m := App{
		cfg:                  testConfig(t),
		composer:             textarea.New(),
		showSidebar:          true,
		width:                120,
		height:               30,
		sidebarWidthOverride: 30,
		viewport:             newTranscriptViewport(80, 10),
	}

	before := m.sidebarWidth()
	updated, _ := m.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Alt: true, Runes: []rune(",")})
	next := updated.(*App)
	after := next.sidebarWidth()
	if after <= before {
		t.Fatalf("expected sidebar width override to grow, before=%d after=%d", before, after)
	}
	_ = next.viewSurface()
	if got := next.ensureMainScreenView().SidebarBasis(); got != after {
		t.Fatalf("expected sidebar flex basis to track width, got %d want %d", got, after)
	}
}

func TestPreferencesDialogApplySavesSidebarWidth(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.UI.SidebarWidth = 30
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.openPreferencesDialog()

	updated, _ := m.handleKey(ui.KeyMsg{Type: ui.KeyShiftTab})
	next := updated.(*App)
	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyRight})
	next = updated.(*App)
	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyTab})
	next = updated.(*App)
	for i := 0; i < 6; i++ {
		updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyDown})
		next = updated.(*App)
	}
	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyRight})
	next = updated.(*App)
	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyTab})
	next = updated.(*App)
	updated, _ = next.handleKey(ui.KeyMsg{Type: ui.KeyEnter})
	next = updated.(*App)

	if next.cfg.UI.SidebarWidth != 31 {
		t.Fatalf("expected sidebar width incremented to 31, got %d", next.cfg.UI.SidebarWidth)
	}
	reloaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.UI.SidebarWidth != 31 {
		t.Fatalf("expected saved sidebar width 31, got %d", reloaded.UI.SidebarWidth)
	}
}

func TestRefreshViewportShowsProviderHintWithoutProvider(t *testing.T) {
	m := App{
		cfg:      config.Default(),
		viewport: newTranscriptViewport(40, 6),
	}
	m.refreshViewport()
	if got := m.viewport.View(); !strings.Contains(got, "/provider") {
		t.Fatalf("expected provider hint in empty viewport, got %q", got)
	}
}

func TestAltHTogglesHelpDialog(t *testing.T) {
	m := App{
		cfg:      testConfig(t),
		composer: textarea.New(),
		width:    120,
		height:   40,
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Alt: true, Runes: []rune("h")})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected sync command when opening help dialog")
	}
	if !next.hasHelpModal() {
		t.Fatal("expected help dialog to open")
	}
	view := next.View()
	if !strings.Contains(view, "Help") || !strings.Contains(view, "/provider") || !strings.Contains(view, "Ctrl-V") || !strings.Contains(view, "Alt-P") || !strings.Contains(view, "Ctrl-R") {
		t.Fatalf("expected help dialog content, got %q", view)
	}
	if !strings.Contains(view, "Ctrl-PgUp/PgDn") {
		t.Fatalf("expected chat navigation hotkey in help dialog, got %q", view)
	}
	if !strings.Contains(view, "PgUp/PgDn") {
		t.Fatalf("expected help dialog scroll footer, got %q", view)
	}
	if !strings.Contains(next.helpBody, "Queue Edit Mode") || !strings.Contains(next.helpBody, "restore selected queued prompt to composer") {
		t.Fatalf("expected queue editing help body content, got %q", next.helpBody)
	}

	updated, cmd = next.handleKey(ui.KeyMsg{Type: ui.KeyEnd})
	next = updated.(*App)
	if cmd != nil {
		t.Fatal("expected scroll key to update modal in place")
	}
	if next.helpYOffset == 0 {
		t.Fatal("expected help modal to become scrollable")
	}

	updated, cmd = next.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Alt: true, Runes: []rune("h")})
	next = updated.(*App)
	if cmd == nil {
		t.Fatal("expected sync command when closing help dialog")
	}
	if next.hasHelpModal() {
		t.Fatal("expected help dialog to close")
	}
}

func TestCtrlPageKeysSwitchChats(t *testing.T) {
	m := App{
		cfg:            testConfig(t),
		composer:       textarea.New(),
		currentSession: domain.Session{ID: 7},
		currentChat:    domain.Chat{ID: 11, SessionID: 7},
		chats: []domain.Chat{
			{ID: 11, SessionID: 7},
			{ID: 12, SessionID: 7},
			{ID: 13, SessionID: 7},
		},
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyCtrlPgDown})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected chat switch command for ctrl+pgdown")
	}
	if !next.loading {
		t.Fatal("expected chat switch to enter busy state")
	}
	if next.status != "Switching to chat 12…" {
		t.Fatalf("expected next-chat status, got %q", next.status)
	}

	updated, cmd = m.handleKey(ui.KeyMsg{Type: ui.KeyCtrlPgUp})
	next = updated.(*App)
	if cmd == nil {
		t.Fatal("expected chat switch command for ctrl+pgup")
	}
	if next.status != "Switching to chat 13…" {
		t.Fatalf("expected previous-chat wrap status, got %q", next.status)
	}
}

func TestMouseClickOnHelpModalCloseIndicatorClosesModal(t *testing.T) {
	cfg := testConfig(t)
	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.mouseEnabled = true
	m.width = 100
	m.height = 40
	m.openHelpModal()

	view := m.View()
	lines := strings.Split(ansi.Strip(view), "\n")
	closeX, closeY := -1, -1
	for y, line := range lines {
		if idx := strings.Index(line, "[X]"); idx >= 0 {
			closeX, closeY = ansi.StringWidth(line[:idx])+1, y
			break
		}
	}
	if closeX < 0 || closeY < 0 {
		t.Fatalf("failed to find help modal close indicator in %q", view)
	}

	updated, cmd := m.Update(ui.MouseMsg{
		Action: ui.MouseActionPress,
		Button: ui.MouseButtonLeft,
		X:      closeX,
		Y:      closeY,
	})
	next := asModelPtr(t, updated)
	_ = cmd
	if next.hasHelpModal() {
		t.Fatal("expected help modal to close from mouse click")
	}
}

func TestMouseClickOnPermissionsPickerCloseIndicatorClosesPicker(t *testing.T) {
	m := App{
		cfg:          testConfig(t),
		mouseEnabled: true,
		width:        100,
		height:       40,
		palette:      theme.Resolve("tokyonight").Palette,
		composer:     textarea.New(),
	}
	m.openPermissionsPicker()

	view := m.View()
	lines := strings.Split(ansi.Strip(view), "\n")
	closeX, closeY := -1, -1
	for y, line := range lines {
		if idx := strings.Index(line, "[X]"); idx >= 0 {
			closeX, closeY = ansi.StringWidth(line[:idx])+1, y
			break
		}
	}
	if closeX < 0 || closeY < 0 {
		t.Fatalf("failed to find permissions picker close indicator in %q", view)
	}

	updated, cmd := m.Update(ui.MouseMsg{
		Action: ui.MouseActionPress,
		Button: ui.MouseButtonLeft,
		X:      closeX,
		Y:      closeY,
	})
	next := asModelPtr(t, updated)
	_ = cmd
	if next.hasPicker() {
		t.Fatal("expected permissions picker to close from mouse click")
	}
	if next.status != "Permission mode selection cancelled" {
		t.Fatalf("expected permission picker cancel status, got %q", next.status)
	}
}

func TestMouseClickOnModelDialogCloseIndicatorCancelsDialog(t *testing.T) {
	m := App{
		cfg:          testConfig(t),
		mouseEnabled: true,
		width:        100,
		height:       28,
		palette:      theme.Resolve("tokyonight").Palette,
		composer:     textarea.New(),
	}
	updated, _ := m.Update(modelListMsg{
		providerID: "openai",
		models: []domain.Model{
			{ID: "gpt-5.4", OwnedBy: "openai"},
			{ID: "gpt-4.1-mini", OwnedBy: "openai"},
		},
	})
	nextModel := updated.(App)

	view := nextModel.View()
	lines := strings.Split(ansi.Strip(view), "\n")
	closeX, closeY := -1, -1
	for y, line := range lines {
		if idx := strings.Index(line, "[X]"); idx >= 0 {
			closeX, closeY = ansi.StringWidth(line[:idx])+1, y
			break
		}
	}
	if closeX < 0 || closeY < 0 {
		t.Fatalf("failed to find model dialog close indicator in %q", view)
	}

	updated, cmd := nextModel.Update(ui.MouseMsg{
		Action: ui.MouseActionPress,
		Button: ui.MouseButtonLeft,
		X:      closeX,
		Y:      closeY,
	})
	next := asModelPtr(t, updated)
	_ = cmd
	if next.hasModelDialog() {
		t.Fatal("expected model dialog to close from mouse click")
	}
	if next.status != "Model selection cancelled" {
		t.Fatalf("expected model dialog cancel status, got %q", next.status)
	}
}

func TestAltPTogglesSystemOutput(t *testing.T) {
	cfg := testConfig(t)
	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Alt: true, Runes: []rune("p")})
	next := updated.(*App)
	if cmd != nil {
		t.Fatalf("expected no command, got %v", cmd)
	}
	if !next.showSystem {
		t.Fatal("expected alt+p to enable system output")
	}

	updated, cmd = next.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Alt: true, Runes: []rune("p")})
	next = updated.(*App)
	if cmd != nil {
		t.Fatalf("expected no command, got %v", cmd)
	}
	if next.showSystem {
		t.Fatal("expected alt+p to disable system output")
	}
}

func TestAltRTogglesReasoningOutput(t *testing.T) {
	cfg := testConfig(t)
	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Alt: true, Runes: []rune("r")})
	next := updated.(*App)
	if cmd != nil {
		t.Fatalf("expected no command, got %v", cmd)
	}
	if !next.showReasoning {
		t.Fatal("expected alt+r to enable reasoning output")
	}

	updated, cmd = next.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Alt: true, Runes: []rune("r")})
	next = updated.(*App)
	if cmd != nil {
		t.Fatalf("expected no command, got %v", cmd)
	}
	if next.showReasoning {
		t.Fatal("expected alt+r to disable reasoning output")
	}
}

func TestAltRPreservesVisibleTopLine(t *testing.T) {
	cfg := testConfig(t)
	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}

	m.currentSession = domain.Session{ID: 1}
	m.width = 24
	m.height = 12
	m.viewport = newTranscriptViewport(24, 4)
	m.currentSnapshot.Messages = []domain.Message{
		{ID: 1, SessionID: 1, Role: domain.MessageRoleAssistant},
	}
	m.currentSnapshot.Parts = map[int64][]domain.Part{
		1: {
			{Kind: domain.PartKindReasoning, Body: "thinking line 1\nthinking line 2\nthinking line 3"},
			{Kind: domain.PartKindText, Body: strings.Repeat("text line ", 40)},
		},
	}

	m.resize()
	m.refreshViewport()
	m.viewport.SetYOffset(2)
	m.refreshViewportPreserve()

	beforeLines := m.viewport.VisibleSurface().Lines()
	if len(beforeLines) == 0 {
		t.Fatal("expected visible transcript lines before toggle")
	}
	beforeTop := strings.TrimRight(ansi.Strip(beforeLines[0]), " ")
	if beforeTop == "" {
		t.Fatalf("expected non-empty top line before toggle, got %q", beforeTop)
	}

	updated, cmd := m.handleKey(ui.KeyMsg{Type: ui.KeyRunes, Alt: true, Runes: []rune("r")})
	next := updated.(*App)
	if cmd != nil {
		t.Fatalf("expected no command, got %v", cmd)
	}
	afterLines := next.viewport.VisibleSurface().Lines()
	if len(afterLines) == 0 {
		t.Fatal("expected visible transcript lines after toggle")
	}
	afterTop := strings.TrimRight(ansi.Strip(afterLines[0]), " ")
	if afterTop != beforeTop {
		t.Fatalf("expected top line %q to remain visible after toggle, got %q", beforeTop, afterTop)
	}
}

func TestRenderAgentsSidebarStatusColors(t *testing.T) {
	none := App{}.renderAgentsSidebarStatus()
	if none != "None" {
		t.Fatalf("expected plain None status, got %q", none)
	}

	upToDate := App{
		currentSession: domain.Session{ProjectChecksum: "abc"},
		workspace:      workspace.Status{AgentsFiles: 1, AgentsChecksum: "abc"},
	}.renderAgentsSidebarStatus()
	if upToDate != "Up to date" {
		t.Fatalf("expected plain Up to date status, got %q", upToDate)
	}

	changed := App{
		currentSession: domain.Session{ProjectChecksum: "abc"},
		workspace:      workspace.Status{AgentsFiles: 1, AgentsChecksum: "def"},
	}.renderAgentsSidebarStatus()
	if changed != "Changed" {
		t.Fatalf("expected plain Changed status, got %q", changed)
	}
}

func TestRenderBodyAppliesSidebarThemeBackground(t *testing.T) {
	m := App{
		showSidebar: true,
		palette:     theme.Resolve("tokyonight").Palette,
		viewport:    newTranscriptViewport(40, 6),
	}
	m.viewport.SetContent("history")

	got := m.renderBody()
	if strings.Contains(got, "\x1b[") || strings.Contains(got, "[38;") || strings.Contains(got, "[48;") {
		t.Fatalf("expected plain body composition without leaked ANSI, got %q", got)
	}
}

func TestRenderBodyClipsSidebarToViewportHeight(t *testing.T) {
	m := App{
		showSidebar: true,
		palette:     theme.Resolve("tokyonight").Palette,
		viewport:    newTranscriptViewport(40, 6),
		workdir:     "/tmp/project",
	}
	m.viewport.SetContent("history")

	got := m.renderBody()
	want := m.viewport.Height + (mainScreenVerticalInset * 2)
	if h := ui.TextHeight(got); h != want {
		t.Fatalf("expected body height %d, got %d from %q", want, h, got)
	}
}

func TestRenderBodyOmitsTranscriptBorder(t *testing.T) {
	m := App{
		palette:  theme.Resolve("tokyonight").Palette,
		viewport: newTranscriptViewport(38, 6),
	}
	m.viewport.SetContent("history")

	got := ansi.Strip(m.renderBody())
	if strings.ContainsAny(got, "┌┐└┘│") {
		t.Fatalf("expected transcript pane without border, got %q", got)
	}
}

func TestRenderBodyKeepsSidebarAtConfiguredWidth(t *testing.T) {
	m := App{
		showSidebar: true,
		width:       120,
		palette:     theme.Resolve("tokyonight").Palette,
		viewport:    newTranscriptViewport(87, 6),
		workdir:     "/tmp/project",
	}
	m.viewport.SetContent("history")

	got := m.renderBody()
	lines := strings.Split(ansi.Strip(got), "\n")
	if len(lines) == 0 {
		t.Fatalf("expected rendered body, got %q", got)
	}
	sidebarWidth := m.sidebarWidth()
	first := lines[0]
	if ansi.StringWidth(first) != 120 {
		t.Fatalf("expected body width 120, got %d in %q", ansi.StringWidth(first), first)
	}
	if sidebarWidth <= 0 || sidebarWidth >= 120 {
		t.Fatalf("unexpected sidebar width %d", sidebarWidth)
	}
	if strings.HasPrefix(strings.TrimSpace(first), "Session") {
		t.Fatalf("expected transcript pane before sidebar, got %q", first)
	}
}

func TestRenderBodyUsesTranscriptElementInsteadOfViewportString(t *testing.T) {
	cfg := testConfig(t)
	m := App{
		cfg:            cfg,
		currentSession: domain.Session{ID: 1, ProviderID: "test", ModelID: "model"},
		currentSnapshot: chatpkg.Snapshot{Messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleAssistant},
		}, Parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindText, Body: "fresh transcript"}},
		}},
		viewport: newTranscriptViewport(32, 6),
	}
	m.viewport.SetContent("stale viewport")

	got := ansi.Strip(m.renderBody())
	if !strings.Contains(got, "fresh transcript") {
		t.Fatalf("expected body to render transcript element, got %q", got)
	}
	if strings.Contains(got, "stale viewport") {
		t.Fatalf("expected body to ignore stale viewport string, got %q", got)
	}
}

func TestComposerAreaAutoExpandsForMultilineDraft(t *testing.T) {
	m := App{
		cfg:         testConfig(t),
		palette:     theme.Default().Palette,
		viewport:    newTranscriptViewport(40, 8),
		renderCache: &modelRenderCache{},
		composer:    textarea.New(),
		width:       60,
		height:      20,
	}
	m.composer.SetValue("draft text that wraps across multiple footer rows in the composer area")
	m.refreshViewport()

	if got := m.composerAreaHeight(); got <= composerHeight {
		t.Fatalf("expected composer area to grow beyond %d, got %d", composerHeight, got)
	}
}

func TestSyncRetainedTranscriptItemsReplacesMatchingKeys(t *testing.T) {
	retained := ui.NewRetainedTranscript()
	first := ui.TranscriptItem{
		Key:  "same",
		Node: ui.NewCachedElement(ui.AsNode(ui.Paragraph{Text: "before"}), 1),
	}
	second := ui.TranscriptItem{
		Key:  "same",
		Node: ui.NewCachedElement(ui.AsNode(ui.Paragraph{Text: "after"}), 1),
	}

	m := App{}
	m.syncRetainedTranscriptItems(retained, []ui.TranscriptItem{first})
	m.syncRetainedTranscriptItems(retained, []ui.TranscriptItem{second})

	rendered := ui.PaintNodeSurface(&ui.Context{}, ui.AsNode(retained), ui.Rect{W: 16, H: 1})
	got := strings.Join(rendered.Lines(), "\n")
	if !strings.Contains(got, "after") {
		t.Fatalf("expected retained transcript item to be replaced, got %q", got)
	}
	if strings.Contains(got, "before") {
		t.Fatalf("expected stale retained transcript item to be removed, got %q", got)
	}
}

func TestViewUsesFullTerminalWidthWithSidebar(t *testing.T) {
	m := App{
		showSidebar: true,
		palette:     theme.Resolve("tokyonight").Palette,
		width:       100,
		height:      12,
		viewport:    newTranscriptViewport(68, 8),
		workdir:     "/tmp/project",
	}
	m.viewport.SetContent("history")

	got := m.View()
	lines := strings.Split(ansi.Strip(got), "\n")
	if len(lines) == 0 {
		t.Fatalf("expected rendered view, got empty output")
	}
	for _, line := range lines {
		if w := ui.TextWidth(line); w != m.width {
			t.Fatalf("expected rendered line width %d, got %d from %q", m.width, w, got)
		}
	}
}

func TestRefreshViewportAppendsWorkingLine(t *testing.T) {
	m := App{
		currentSession:  domain.Session{ID: 1},
		status:          "Working ...",
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
		viewport:        newTranscriptViewport(40, 6),
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
			spinner: spinnerModel{
				active: true,
			},
		},
	}

	m.startWaitingForLLM()
	m.refreshViewport()
	got := m.renderBody()
	if !strings.Contains(got, "Waiting for LLM response") || !strings.Contains(got, ui.SpinnerFrame(config.Default().UI.Spinner, 0)) {
		t.Fatalf("expected transcript activity line, got %q", got)
	}
}

func TestRenderFooterOmitsHotkeyHints(t *testing.T) {
	m := App{
		composer: textarea.New(),
	}

	got := m.renderFooter()
	if strings.Contains(got, "enter send/select") || strings.Contains(got, "/permissions") {
		t.Fatalf("expected footer to omit hotkey hints, got %q", got)
	}
}

func TestRenderFooterShowsQueuedPromptPreviewAboveComposer(t *testing.T) {
	composer := textarea.New()
	composer.Placeholder = "Ask koder or type / for commands"
	composer.SetHeight(composerInputHeight)
	composer.SetWidth(38)
	composer.Focus()

	m := App{
		width:    40,
		composer: composer,
		currentChat: domain.Chat{QueuedInputs: []domain.QueuedInput{{
			ID:   1,
			Text: "queued submission",
			Kind: domain.QueuedInputKindQueued,
		}}},
	}

	got := ansi.Strip(m.renderFooter())
	if !strings.Contains(got, "Queued inputs") || !strings.Contains(got, "queued submission") {
		t.Fatalf("expected queued prompt preview above composer, got %q", got)
	}
	if strings.Index(got, "queued submission") > strings.Index(got, "Ask koder or type / for commands") {
		t.Fatalf("expected queued preview to render above composer, got %q", got)
	}
}

func TestRenderFooterOnlyUsesComposerHeightWhenNoAuxiliaryContent(t *testing.T) {
	composer := textarea.New()
	composer.Placeholder = "Ask koder or type / for commands"
	composer.SetHeight(composerInputHeight)
	composer.SetWidth(38)
	composer.Focus()

	m := App{
		width:    40,
		composer: composer,
	}

	got := ansi.Strip(m.renderFooter())
	if height := ui.TextHeight(got); height != composerHeight {
		t.Fatalf("expected footer height %d with only composer visible, got %d in %q", composerHeight, height, got)
	}
}

func TestViewBottomAlignsFooter(t *testing.T) {
	composer := textarea.New()
	composer.Placeholder = "Ask koder or type / for commands"
	composer.SetHeight(composerInputHeight)
	composer.SetWidth(38)
	composer.Focus()

	m := App{
		width:    40,
		height:   12,
		composer: composer,
		viewport: newTranscriptViewport(38, 4),
	}
	m.viewport.SetContent("history")

	got := m.View()
	lines := strings.Split(got, "\n")
	if len(lines) != 12 {
		t.Fatalf("expected placed view to match height, got %d lines", len(lines))
	}
	bottom := strings.Join(lines[len(lines)-3:], "\n")
	if !strings.Contains(bottom, "Ask koder or type / for") {
		t.Fatalf("expected composer box at bottom, got %q", got)
	}
}

func TestViewShowsAllVisibleTranscriptLinesAboveComposer(t *testing.T) {
	composer := textarea.New()
	composer.Placeholder = "Ask koder or type / for commands"
	composer.SetHeight(composerInputHeight)
	composer.Focus()

	m := App{
		cfg:            testConfig(t),
		palette:        theme.Resolve("tokyonight").Palette,
		width:          40,
		height:         12,
		composer:       composer,
		currentSession: domain.Session{ID: 1},
		currentSnapshot: chatpkg.Snapshot{Messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleAssistant},
			{ID: 2, Role: domain.MessageRoleUser},
		}, Parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindText, Body: "assistant context"}},
			2: {{Kind: domain.PartKindText, Body: "final user line one\nfinal user line two"}},
		}},
		viewport: newTranscriptViewport(37, 8),
	}

	m.resize()
	m.refreshViewport()

	viewLines := strings.Split(strings.Join(m.viewSurface().Lines(), "\n"), "\n")
	transcriptLines := m.viewport.VisibleSurface().Lines()
	if len(transcriptLines) == 0 {
		t.Fatal("expected visible transcript lines")
	}
	composerIdx := indexOfTrimmedLineContaining(viewLines, "Ask koder or type / for commands")
	if composerIdx < 0 {
		t.Fatalf("expected composer in rendered view, got:\n%s", strings.Join(viewLines, "\n"))
	}
	lastTranscriptLine := strings.TrimRight(ansi.Strip(transcriptLines[len(transcriptLines)-1]), " ")
	lastTranscriptIdx := indexOfTrimmedLine(viewLines[:composerIdx], lastTranscriptLine)
	if lastTranscriptIdx < 0 {
		t.Fatalf("expected final visible transcript line %q before composer\nview:\n%s\n\ntranscript:\n%s", lastTranscriptLine, strings.Join(viewLines, "\n"), strings.Join(transcriptLines, "\n"))
	}
	if lastTranscriptIdx >= composerIdx {
		t.Fatalf("expected final visible transcript line before composer, got transcript=%d composer=%d", lastTranscriptIdx, composerIdx)
	}
}

func TestViewDoesNotLeaveLargeGapBetweenTranscriptAndComposer(t *testing.T) {
	composer := textarea.New()
	composer.Placeholder = "Ask koder or type / for commands"
	composer.SetHeight(composerInputHeight)
	composer.Focus()

	m := App{
		cfg:            testConfig(t),
		palette:        theme.Resolve("tokyonight").Palette,
		width:          40,
		height:         12,
		composer:       composer,
		currentSession: domain.Session{ID: 1},
		currentSnapshot: chatpkg.Snapshot{Messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleAssistant},
		}, Parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindText, Body: "continue"}},
		}},
		viewport: newTranscriptViewport(37, 8),
	}

	m.resize()
	m.refreshViewport()

	viewLines := strings.Split(strings.Join(m.viewSurface().Lines(), "\n"), "\n")
	composerIdx := indexOfTrimmedLineContaining(viewLines, "Ask koder or type / for commands")
	if composerIdx < 0 {
		t.Fatalf("expected composer in rendered view, got:\n%s", strings.Join(viewLines, "\n"))
	}
	lastContentIdx := lastNonEmptyTrimmedLineIndex(viewLines[:composerIdx])
	if lastContentIdx < 0 {
		t.Fatalf("expected transcript content before composer, got:\n%s", strings.Join(viewLines, "\n"))
	}
	gap := composerIdx - lastContentIdx - 1
	if gap > 1 {
		t.Fatalf("expected at most one spacer line between transcript and composer, got %d\nview:\n%s", gap, strings.Join(viewLines, "\n"))
	}
}

func TestViewShowsLastUserBubbleLineBeforeComposer(t *testing.T) {
	composer := textarea.New()
	composer.Placeholder = "Ask koder or type / for commands"
	composer.SetHeight(composerInputHeight)
	composer.Focus()

	m := App{
		cfg:            testConfig(t),
		palette:        theme.Resolve("tokyonight").Palette,
		width:          40,
		height:         10,
		composer:       composer,
		currentSession: domain.Session{ID: 1},
		currentSnapshot: chatpkg.Snapshot{Messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleUser},
		}, Parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindText, Body: "final line"}},
		}},
		viewport: newTranscriptViewport(37, 6),
	}

	m.resize()
	m.refreshViewport()

	viewLines := strings.Split(strings.Join(m.viewSurface().Lines(), "\n"), "\n")
	composerIdx := indexOfTrimmedLineContaining(viewLines, "Ask koder or type / for commands")
	if composerIdx < 0 {
		t.Fatalf("expected composer in rendered view, got:\n%s", strings.Join(viewLines, "\n"))
	}
	lastBubbleIdx := lastIndexOfTrimmedLineContaining(viewLines[:composerIdx], "▀")
	if lastBubbleIdx < 0 {
		t.Fatalf("expected last user bubble line before composer, got:\n%s", strings.Join(viewLines, "\n"))
	}
	if composerIdx <= lastBubbleIdx {
		t.Fatalf("expected user bubble to render before composer, got:\n%s", strings.Join(viewLines, "\n"))
	}
}

func indexOfTrimmedLine(lines []string, want string) int {
	for idx, line := range lines {
		if strings.Contains(strings.TrimRight(ansi.Strip(line), " "), want) {
			return idx
		}
	}
	return -1
}

func indexOfTrimmedLineContaining(lines []string, want string) int {
	for idx, line := range lines {
		if strings.Contains(strings.TrimRight(ansi.Strip(line), " "), want) {
			return idx
		}
	}
	return -1
}

func lastIndexOfTrimmedLineContaining(lines []string, want string) int {
	for idx := len(lines) - 1; idx >= 0; idx-- {
		if strings.Contains(strings.TrimRight(ansi.Strip(lines[idx]), " "), want) {
			return idx
		}
	}
	return -1
}

func lastNonEmptyTrimmedLineIndex(lines []string) int {
	for idx := len(lines) - 1; idx >= 0; idx-- {
		if strings.TrimSpace(ansi.Strip(lines[idx])) != "" {
			return idx
		}
	}
	return -1
}

func TestResizeUsesMeasuredFooterHeight(t *testing.T) {
	m := App{
		width:    80,
		height:   24,
		composer: textarea.New(),
	}
	m.composer.SetHeight(4)

	m.resize()

	want := 24 - m.statusPaneHeight() - (mainScreenVerticalInset * 2)
	if want < 5 {
		want = 5
	}
	if m.viewport.Height != want {
		t.Fatalf("expected viewport height %d from measured footer, got %d", want, m.viewport.Height)
	}
	if got := m.transcriptViewportHeight(); got >= m.viewport.Height {
		t.Fatalf("expected transcript viewport height to shrink inside main layout, got transcript=%d main=%d", got, m.viewport.Height)
	}
}

func TestRenderComposerUsesThreeLineBoxAndFullWidth(t *testing.T) {
	palette := theme.Resolve("tokyonight").Palette
	cfg := testConfig(t)
	m := App{
		cfg:         cfg,
		width:       80,
		showSidebar: true,
		composer:    textarea.New(),
		palette:     palette,
	}
	m.composer.Placeholder = "Ask koder or type / for commands"
	m.composer.Prompt = mPrompt(cfg)
	m.composer.SetHeight(composerInputHeight)
	m.composer.SetWidth(m.composerWidth())
	applyComposerTheme(&m.composer, palette)

	got := m.renderComposer()
	if ui.TextHeight(got) != 3 {
		t.Fatalf("expected 3-line composer box, got %d lines in %q", ui.TextHeight(got), got)
	}
	lines := strings.Split(got, "\n")
	if !strings.Contains(lines[0], "▄") || !strings.Contains(lines[len(lines)-1], "▀") {
		t.Fatalf("expected half-block top and bottom lines, got %q", got)
	}
	if !strings.HasPrefix(lines[0], "▄") || !strings.HasPrefix(lines[len(lines)-1], "▀") {
		t.Fatalf("expected half-height accent strip on separator rows, got %q", got)
	}
	if !strings.Contains(lines[1], "█") {
		t.Fatalf("expected block accent glyph on content line, got %q", lines[1])
	}
	for _, line := range lines {
		if ui.TextWidth(line) != m.composerWidth() {
			t.Fatalf("expected composer line width %d, got %d in %q", m.composerWidth(), ui.TextWidth(line), line)
		}
	}
}

func TestRenderUserMessageUsesAccentBarOnAllLines(t *testing.T) {
	cfg := testConfig(t)
	m := App{
		cfg:     cfg,
		palette: theme.Resolve("tokyonight").Palette,
		viewport: transcriptViewport{
			Width: 40,
		},
	}

	got := newTranscriptRenderer(&m).renderUserMessage("hello", "")
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 user message lines, got %d in %q", len(lines), got)
	}
	if !strings.Contains(lines[0], "▄") || !strings.Contains(lines[2], "▀") {
		t.Fatalf("expected half-block separator rows, got %q", got)
	}
	if !strings.HasPrefix(lines[0], "▄") || !strings.HasPrefix(lines[2], "▀") {
		t.Fatalf("expected half-height accent strip on separator rows, got %q", got)
	}
	if !strings.Contains(lines[1], "█") {
		t.Fatalf("expected block accent on content row, got %q", lines[1])
	}
}

func TestRenderUserMessageCanDisableHalfBlocks(t *testing.T) {
	cfg := testConfig(t)
	cfg.UI.HalfBlocks = false
	m := App{
		cfg:     cfg,
		palette: theme.Resolve("tokyonight").Palette,
		viewport: transcriptViewport{
			Width: 40,
		},
	}

	got := newTranscriptRenderer(&m).renderUserMessage("hello", "")
	if strings.Contains(got, "▄") || strings.Contains(got, "▀") || strings.Contains(got, "█") {
		t.Fatalf("expected classic user message rendering when half blocks disabled, got %q", got)
	}
	if !strings.Contains(got, "┃") {
		t.Fatalf("expected classic accent bar when half blocks disabled, got %q", got)
	}
}

func TestRenderTranscriptUserMessageFallsBackToSummaryWhenPartsMissing(t *testing.T) {
	cfg := testConfig(t)
	m := App{
		cfg:             cfg,
		palette:         theme.Resolve("tokyonight").Palette,
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
		viewport: transcriptViewport{
			Width: 40,
		},
	}

	got := m.renderTranscriptMessage(domain.Message{
		ID:      1,
		Role:    domain.MessageRoleUser,
		Summary: "what tools are available",
	})
	if !strings.Contains(got, "what tools are available") {
		t.Fatalf("expected user summary fallback in transcript, got %q", got)
	}
}

func TestRefreshViewportOmitsWorkingLineForGenericLoading(t *testing.T) {
	m := App{
		currentSession:  domain.Session{ID: 1},
		loading:         true,
		status:          "Resuming session 2…",
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
		viewport:        newTranscriptViewport(40, 6),
		busy: busyModel{
			active: true,
			scope:  busyScopeSidebar,
			spinner: spinnerModel{
				active: true,
			},
		},
	}

	m.refreshViewport()
	got := m.renderBody()
	if strings.Contains(got, "Resuming session 2") || strings.Contains(got, "[=") {
		t.Fatalf("expected no model activity line for generic loading, got %q", got)
	}
}

func TestSpinnerTickRefreshesTranscriptActivity(t *testing.T) {
	m := App{
		currentSession:  domain.Session{ID: 1},
		status:          "Working ...",
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
		viewport:        newTranscriptViewport(40, 6),
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
			spinner: spinnerModel{
				active: true,
			},
		},
	}

	m.refreshViewport()
	before := m.renderBody()

	updated, cmd := m.Update(spinnerTickMsg{})
	next := updated.(App)
	after := next.renderBody()

	if before == after {
		t.Fatalf("expected spinner tick to refresh transcript activity, before=%q after=%q", before, after)
	}
	if cmd == nil {
		t.Fatal("expected follow-up spinner tick command")
	}
}

func TestStatusEventKeepsTranscriptSpinnerActive(t *testing.T) {
	m := App{}
	m.startBusy(busyScopeTranscript, "Compacting session...")

	m.applyEvent(domain.Event{Kind: domain.EventKindStatus, Text: "Compacting session..."})

	if !m.busy.transcriptActive() {
		t.Fatal("expected transcript spinner to remain active for status updates during busy work")
	}
	if got := m.renderTranscriptActivity(); !strings.Contains(got, "Compacting session...") || !strings.Contains(got, ui.SpinnerFrame(config.Default().UI.Spinner, 0)) {
		t.Fatalf("expected transcript activity to still render, got %q", got)
	}
}

func TestBusyIndicatorLayoutRefreshKeepsTranscriptAnchoredAtBottom(t *testing.T) {
	cfg := testConfig(t)
	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.currentSession = domain.Session{ID: 1}
	m.width = 60
	m.height = 16
	m.viewport = newTranscriptViewport(60, 8)
	m.currentSnapshot.Messages = []domain.Message{
		{ID: 1, SessionID: 1, Role: domain.MessageRoleAssistant},
	}
	m.currentSnapshot.Parts = map[int64][]domain.Part{
		1: {
			{Kind: domain.PartKindText, Body: strings.Join([]string{
				"line 1", "line 2", "line 3", "line 4", "line 5", "line 6", "line 7", "line 8", "line 9", "line 10", "line 11", "line 12",
			}, "\n")},
		},
	}

	m.resize()
	m.refreshViewport()
	if !m.viewport.AtBottom() {
		t.Fatal("expected transcript to start at bottom")
	}

	beforeHeight := m.transcriptViewportHeight()
	m.startBusy(busyScopeTranscript, "Working ...")

	if !m.viewport.AtBottom() {
		t.Fatal("expected transcript to remain anchored at bottom when busy indicator appears")
	}
	afterHeight := m.transcriptViewportHeight()
	if afterHeight >= beforeHeight {
		t.Fatalf("expected transcript viewport height to shrink when busy indicator appears, before=%d after=%d", beforeHeight, afterHeight)
	}
	if got := m.viewport.VisibleSurface().SurfaceHeight(); got != afterHeight {
		t.Fatalf("expected visible transcript surface height %d after busy layout change, got %d", afterHeight, got)
	}

	m.stopBusy()
	if !m.viewport.AtBottom() {
		t.Fatal("expected transcript to remain anchored at bottom when busy indicator disappears")
	}
	if got := m.viewport.VisibleSurface().SurfaceHeight(); got != m.transcriptViewportHeight() {
		t.Fatalf("expected visible transcript surface height %d after busy layout reset, got %d", m.transcriptViewportHeight(), got)
	}
}

func TestStatusEventDoesNotOverrideTranscriptLLMPhase(t *testing.T) {
	m := App{}
	m.startWaitingForLLM()

	m.applyEvent(domain.Event{Kind: domain.EventKindStatus, Text: "Checking project instructions..."})

	if got := m.transcriptBusyStatus(); got != "Waiting for LLM response" {
		t.Fatalf("expected transcript llm status to remain stable, got %q", got)
	}
}

func TestTranscriptBusyPhaseTransitions(t *testing.T) {
	m := App{}
	m.startWaitingForLLM()
	if got := m.transcriptBusyStatus(); got != "Waiting for LLM response" {
		t.Fatalf("expected waiting status, got %q", got)
	}

	m.applyEvent(domain.Event{Kind: domain.EventKindReasoning, Text: "thinking"})
	if got := m.transcriptBusyStatus(); got != "Streaming thoughts ..." {
		t.Fatalf("expected reasoning status, got %q", got)
	}

	m.applyEvent(domain.Event{Kind: domain.EventKindMessageDelta, Text: "answer"})
	if got := m.transcriptBusyStatus(); got != "Streaming LLM response ..." {
		t.Fatalf("expected response status, got %q", got)
	}

	m.applyEvent(domain.Event{Kind: domain.EventKindMessageDone})
	if got := m.status; got != "Idle" {
		t.Fatalf("expected idle status after message done, got %q", got)
	}
}

func TestTranscriptBusyPhaseTracksParallelTools(t *testing.T) {
	m := App{}
	m.startWaitingForLLM()

	m.applyEvent(domain.Event{Kind: domain.EventKindToolStart, Tool: domain.ToolKindBash})
	if got := m.transcriptBusyStatus(); got != "Running tool" {
		t.Fatalf("expected single tool status, got %q", got)
	}

	m.applyEvent(domain.Event{Kind: domain.EventKindToolStart, Tool: domain.ToolKindRead})
	if got := m.transcriptBusyStatus(); got != "Running 2 tools" {
		t.Fatalf("expected multi-tool status, got %q", got)
	}

	m.applyEvent(domain.Event{Kind: domain.EventKindToolResult, Tool: domain.ToolKindBash})
	if got := m.transcriptBusyStatus(); got != "Running tool" {
		t.Fatalf("expected tool countdown status, got %q", got)
	}

	m.applyEvent(domain.Event{Kind: domain.EventKindToolResult, Tool: domain.ToolKindRead})
	if got := m.transcriptBusyStatus(); got != "Waiting for LLM response" {
		t.Fatalf("expected waiting status after tools finish, got %q", got)
	}
}

func TestLoadMsgPreserveBusyKeepsSpinnerActive(t *testing.T) {
	m := App{
		cfg:      testConfig(t),
		viewport: newTranscriptViewport(40, 6),
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
			status: "Working ...",
			spinner: spinnerModel{
				active: true,
				frame:  2,
			},
		},
		currentSession: domain.Session{ID: 1, Title: "Test"},
	}

	updated, cmd := m.Update(loadMsg{
		current:      domain.Session{ID: 1, Title: "Test"},
		parts:        map[int64][]domain.Part{},
		workspace:    workspace.Status{},
		preserveBusy: true,
	})
	next := updated.(App)
	if cmd == nil {
		t.Fatal("expected sync title command after load update")
	}
	if !next.busy.spinner.active {
		t.Fatal("expected spinner to remain active during preserved busy reload")
	}
	if got := next.workingIndicator(); got == "" {
		t.Fatal("expected working indicator to remain visible")
	}
	if !strings.Contains(next.windowTitle(), ui.SpinnerFrame(next.cfg.UI.Spinner, 2)) {
		t.Fatalf("expected spinner in window title, got %q", next.windowTitle())
	}
}

func TestLoadMsgPreserveBusySameChatKeepsPendingAssistantStream(t *testing.T) {
	m := App{
		cfg: func() config.Config {
			cfg := testConfig(t)
			cfg.DefaultProvider = "openai"
			cfg.DefaultModel = "gpt-5.4"
			cfg.Providers = map[string]config.Provider{
				"openai": {
					Kind:         "openai-compatible",
					AuthMethod:   "api_key",
					BaseURL:      "https://api.openai.com/v1",
					APIKey:       "secret",
					DefaultModel: "gpt-5.4",
				},
			}
			return cfg
		}(),
		viewport:       newTranscriptViewport(60, 8),
		currentSession: domain.Session{ID: 33, Title: "Refactor coordinates to use float tiles", ProviderID: "openai", ModelID: "gpt-5.4"},
		currentChat:    domain.Chat{ID: 24, SessionID: 33},
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
			status: "Working ...",
		},
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
	}

	// Captured from the live session 33 stream around 11:40:08, where
	// message deltas were interleaved with preserved chat reloads.
	m.applyEvent(domain.Event{Kind: domain.EventKindMessageDelta, Text: "The"})
	if got := m.currentSnapshot.PendingAssistant.Text; got != "The" {
		t.Fatalf("expected initial pending assistant text, got %q", got)
	}

	updated, _ := m.Update(loadMsg{
		current:      domain.Session{ID: 33, Title: "Refactor coordinates to use float tiles"},
		chat:         domain.Chat{ID: 24, SessionID: 33},
		parts:        map[int64][]domain.Part{},
		workspace:    workspace.Status{},
		preserveBusy: true,
	})
	m = updated.(App)
	if got := m.currentSnapshot.PendingAssistant.Text; got != "The" {
		t.Fatalf("expected preserved pending assistant text after preserved reload, got %q", got)
	}

	m.applyEvent(domain.Event{Kind: domain.EventKindMessageDelta, Text: " `ts` is"})
	m.applyEvent(domain.Event{Kind: domain.EventKindMessageDelta, Text: " now"})
	if got := m.currentSnapshot.PendingAssistant.Text; got != "The `ts` is now" {
		t.Fatalf("expected concatenated pending assistant text after reload interleaving, got %q", got)
	}

	m.prepareFrame()
	rendered := m.renderBody()
	if !strings.Contains(rendered, "The `ts` is now") {
		t.Fatalf("expected rendered transcript to retain pending assistant stream, got %q", rendered)
	}
}

func TestMessageDeltasRenderOnFrame(t *testing.T) {
	m := App{
		cfg:             testConfig(t),
		viewport:        newTranscriptViewport(60, 8),
		currentSession:  domain.Session{ID: 1, Title: "Test"},
		currentChat:     domain.Chat{ID: 2, SessionID: 1},
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
	}
	m.refreshViewport()

	m.applyEvent(domain.Event{Kind: domain.EventKindMessageDelta, Text: "The"})
	m.applyEvent(domain.Event{Kind: domain.EventKindMessageDelta, Text: " stream"})
	m.applyEvent(domain.Event{Kind: domain.EventKindMessageDelta, Text: " arrived"})

	if got := strings.Join(m.viewport.VisibleSurface().Lines(), "\n"); strings.Contains(got, "The stream arrived") {
		t.Fatalf("expected viewport not to refresh before frame, got %q", got)
	}
	if !m.pendingTranscriptFrameDirty {
		t.Fatal("expected message deltas to mark pending transcript frame dirty")
	}

	m.prepareFrame()

	if got := strings.Join(m.viewport.VisibleSurface().Lines(), "\n"); !strings.Contains(got, "The stream arrived") {
		t.Fatalf("expected frame to render accumulated pending text, got %q", got)
	}
	if m.pendingTranscriptFrameDirty {
		t.Fatal("expected frame to clear pending transcript dirty flag")
	}
}

func TestQueuedDispatchReloadKeepsPendingAssistantStream(t *testing.T) {
	m := App{
		cfg:            testConfig(t),
		viewport:       newTranscriptViewport(60, 8),
		currentSession: domain.Session{ID: 33, Title: "Refactor coordinates to use float tiles"},
		currentChat: domain.Chat{
			ID:        24,
			SessionID: 33,
			QueuedInputs: []domain.QueuedInput{{
				ID:   1,
				Kind: domain.QueuedInputKindQueued,
				Text: "after this, tighten the draw loop",
			}},
		},
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
			status: "Working ...",
		},
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
	}

	m.applyEvent(domain.Event{Kind: domain.EventKindMessageDelta, Text: "The"})
	m.setQueuedInputs(nil)
	m.appendLocalUserPrompt("after this, tighten the draw loop", nil, nil)
	if got := m.currentSnapshot.PendingAssistant.Text; got != "The" {
		t.Fatalf("expected pending assistant stream to survive queued dispatch setup, got %q", got)
	}

	updated, _ := m.Update(loadMsg{
		current:      domain.Session{ID: 33, Title: "Refactor coordinates to use float tiles"},
		chat:         domain.Chat{ID: 24, SessionID: 33},
		parts:        map[int64][]domain.Part{},
		workspace:    workspace.Status{},
		preserveBusy: true,
	})
	m = updated.(App)

	m.applyEvent(domain.Event{Kind: domain.EventKindMessageDelta, Text: " `ts` is"})
	m.applyEvent(domain.Event{Kind: domain.EventKindMessageDelta, Text: " now"})
	if got := m.currentSnapshot.PendingAssistant.Text; got != "The `ts` is now" {
		t.Fatalf("expected pending assistant stream to survive queued reload interleave, got %q", got)
	}
}

func TestSpinnerTickPreservesViewportOffsetWhenScrolledBack(t *testing.T) {
	m := App{
		cfg:            testConfig(t),
		currentSession: domain.Session{ID: 1, Title: "Test"},
		viewport:       newTranscriptViewport(40, 4),
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
			status: "Working ...",
			spinner: spinnerModel{
				active: true,
			},
		},
		currentSnapshot: chatpkg.Snapshot{Messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleAssistant},
		}, Parts: map[int64][]domain.Part{
			1: {{
				Kind: domain.PartKindText,
				Body: strings.Join([]string{
					"line 1",
					"line 2",
					"line 3",
					"line 4",
					"line 5",
					"line 6",
					"line 7",
					"line 8",
				}, "\n"),
			}},
		}},
	}

	m.refreshViewport()
	m.viewport.SetYOffset(1)
	beforeOffset := m.viewport.YOffset

	updated, cmd := m.Update(spinnerTickMsg{})
	next := updated.(App)

	if cmd == nil {
		t.Fatal("expected follow-up spinner tick command")
	}
	if next.viewport.YOffset != beforeOffset {
		t.Fatalf("expected spinner tick to preserve viewport offset %d, got %d", beforeOffset, next.viewport.YOffset)
	}
}

func TestSpinnerTickAnimatesSidebarBusyIndicator(t *testing.T) {
	cfg := testConfig(t)
	cfg.UI.Spinner = "circles"
	m := App{
		cfg: cfg,
		busy: busyModel{
			active: true,
			scope:  busyScopeSidebar,
			status: "Creating session…",
			spinner: spinnerModel{
				active: true,
			},
		},
		status:         "Ready",
		currentSession: domain.Session{Title: "Test"},
		viewport:       newTranscriptViewport(40, 6),
		composer:       textarea.New(),
	}

	before := m.renderSidebar()

	updated, cmd := m.Update(spinnerTickMsg{})
	next := updated.(App)
	after := next.renderSidebar()

	if before == after {
		t.Fatalf("expected sidebar busy indicator to animate, before=%q after=%q", before, after)
	}
	if cmd == nil {
		t.Fatal("expected follow-up spinner tick command")
	}
	if next.busy.spinner.frame != 1 {
		t.Fatalf("expected spinner frame to advance, got %d", next.busy.spinner.frame)
	}
}

func TestRenderMessagePartsShowsReasoningBeforeText(t *testing.T) {
	m := App{
		showReasoning: true,
	}

	got := newTranscriptRenderer(&m).renderMessageParts([]domain.Part{
		{Kind: domain.PartKindText, Body: "final answer"},
		{Kind: domain.PartKindReasoning, Body: "thinking first"},
	})

	if !strings.Contains(got, "thinking first") || !strings.Contains(got, "final answer") {
		t.Fatalf("expected both reasoning and text, got %q", got)
	}
	if strings.Index(got, "thinking first") > strings.Index(got, "final answer") {
		t.Fatalf("expected reasoning before text, got %q", got)
	}
	if strings.Contains(got, "\n\nthinking first") {
		t.Fatalf("expected no blank line before reasoning, got %q", got)
	}
	if !strings.Contains(got, "\n\nfinal answer") {
		t.Fatalf("expected blank line after reasoning before final answer, got %q", got)
	}
}

func TestRenderStyledMessagePartsShowsReasoningBeforeText(t *testing.T) {
	cfg := testConfig(t)
	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.showReasoning = true
	m.viewport.Width = 60

	got := ui.PlainStyledText(newTranscriptRenderer(&m).renderStyledMessageParts([]domain.Part{
		{Kind: domain.PartKindText, Body: "final answer"},
		{Kind: domain.PartKindReasoning, Body: "thinking first"},
	}))

	if !strings.Contains(got, "thinking first") || !strings.Contains(got, "final answer") {
		t.Fatalf("expected both reasoning and text, got %q", got)
	}
	if strings.Index(got, "thinking first") > strings.Index(got, "final answer") {
		t.Fatalf("expected reasoning before text, got %q", got)
	}
	if strings.Contains(got, "\n\nthinking first") {
		t.Fatalf("expected no blank line before reasoning, got %q", got)
	}
	if !strings.Contains(got, "\n\nfinal answer") {
		t.Fatalf("expected blank line after reasoning before final answer, got %q", got)
	}
}

func TestRenderMessagePartsReasoningOnlyShowsReasoningWhenHidden(t *testing.T) {
	m := App{
		showReasoning: false,
	}

	got := newTranscriptRenderer(&m).renderMessageParts([]domain.Part{
		{Kind: domain.PartKindReasoning, Body: "thinking first"},
	})

	if !strings.Contains(got, "thinking first") {
		t.Fatalf("expected reasoning-only turn to stay visible, got %q", got)
	}
	if !strings.Contains(got, reasoningOnlyPlaceholder) {
		t.Fatalf("expected reasoning-only placeholder, got %q", got)
	}
	if strings.Index(got, "thinking first") > strings.Index(got, reasoningOnlyPlaceholder) {
		t.Fatalf("expected reasoning before placeholder, got %q", got)
	}
}

func TestRenderMessagePartsHidesReasoningWhenTextExistsAndReasoningHidden(t *testing.T) {
	m := App{
		showReasoning: false,
	}

	got := newTranscriptRenderer(&m).renderMessageParts([]domain.Part{
		{Kind: domain.PartKindReasoning, Body: "thinking first"},
		{Kind: domain.PartKindText, Body: "final answer"},
	})

	if strings.Contains(got, "thinking first") {
		t.Fatalf("expected reasoning hidden when final text exists, got %q", got)
	}
	if strings.Contains(got, reasoningOnlyPlaceholder) {
		t.Fatalf("expected no reasoning-only placeholder when final text exists, got %q", got)
	}
	if !strings.Contains(got, "final answer") {
		t.Fatalf("expected final answer to remain visible, got %q", got)
	}
}

func TestRenderStyledMessagePartsReasoningOnlyShowsReasoningWhenHidden(t *testing.T) {
	cfg := testConfig(t)
	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.showReasoning = false
	m.viewport.Width = 60

	got := ui.PlainStyledText(newTranscriptRenderer(&m).renderStyledMessageParts([]domain.Part{
		{Kind: domain.PartKindReasoning, Body: "thinking first"},
	}))

	if !strings.Contains(got, "thinking first") {
		t.Fatalf("expected reasoning-only styled turn to stay visible, got %q", got)
	}
	if !strings.Contains(got, reasoningOnlyPlaceholder) {
		t.Fatalf("expected reasoning-only placeholder, got %q", got)
	}
}

func TestPendingAssistantReasoningOnlyShowsThinkingIndicator(t *testing.T) {
	cfg := testConfig(t)
	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.currentSession = domain.Session{ID: 1}
	m.viewport.Width = 120
	m.showReasoning = false
	m.startBusy(busyScopeTranscript, "Thinking ...")
	m.busy.setTranscriptPhase(transcriptBusyPhaseThoughts)
	m.currentSnapshot.PendingAssistant = chatpkg.PendingAssistantTurn{
		CreatedAt: time.Unix(1, 0).UTC(),
		Reasoning: "hidden chain of thought",
	}

	m.refreshViewport()
	got := m.renderBody()

	if !strings.Contains(got, "Thinking ...") {
		t.Fatalf("expected pending thinking indicator, got %q", got)
	}
	if strings.Contains(got, reasoningOnlyPlaceholder) {
		t.Fatalf("expected thinking indicator to replace reasoning placeholder while pending, got %q", got)
	}
}

func TestTranscriptBlocksIncludePendingAssistantTurn(t *testing.T) {
	cfg := testConfig(t)
	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.currentSession = domain.Session{ID: 1}
	m.showReasoning = true
	m.currentSnapshot.PendingAssistant = chatpkg.PendingAssistantTurn{
		Text:      "partial answer",
		Reasoning: "thinking first",
	}

	blocks := m.transcriptBlocks()
	if len(blocks) != 1 {
		t.Fatalf("expected one pending block, got %d", len(blocks))
	}
	if blocks[0].Message.Role != domain.MessageRoleAssistant {
		t.Fatalf("expected assistant pending block, got %#v", blocks[0].Message)
	}
	got := ui.PlainStyledText(newTranscriptRenderer(&m).renderStyledMessageParts(blocks[0].Parts))
	if !strings.Contains(got, "partial answer") || !strings.Contains(got, "thinking first") {
		t.Fatalf("expected pending assistant content, got %q", got)
	}
}

func TestRenderMessagePartsSkipsSystemNotice(t *testing.T) {
	m := App{}

	got := newTranscriptRenderer(&m).renderMessageParts([]domain.Part{
		{Kind: domain.PartKindText, Body: "final answer"},
		{Kind: domain.PartKindSystemNotice, Body: "usage", MetaJSON: `{"PromptTokens":1}`},
	})

	if !strings.Contains(got, "final answer") {
		t.Fatalf("expected text to remain visible, got %q", got)
	}
	if strings.Contains(got, "usage") || strings.Contains(got, "PromptTokens") {
		t.Fatalf("expected system notice to stay hidden, got %q", got)
	}
}

func TestRenderMessagePartsShowsSystemNoticeWhenEnabled(t *testing.T) {
	cfg := testConfig(t)
	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.showSystem = true

	got := newTranscriptRenderer(&m).renderMessageParts([]domain.Part{
		{Kind: domain.PartKindText, Body: "final answer"},
		{Kind: domain.PartKindSystemNotice, Body: "usage", MetaJSON: `{"PromptTokens":1}`},
	})

	if strings.Contains(got, "System") || strings.Contains(got, "usage") || strings.Contains(got, "PromptTokens") {
		t.Fatalf("expected usage notice to stay hidden, got %q", got)
	}
	if !strings.Contains(got, "final answer") {
		t.Fatalf("expected text to remain visible, got %q", got)
	}
}

func TestRenderMessagePartsShowsNonUsageSystemNoticeWhenEnabled(t *testing.T) {
	cfg := testConfig(t)
	m, err := New(cfg, nil, nil, StartupModeNew, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.showSystem = true

	got := newTranscriptRenderer(&m).renderMessageParts([]domain.Part{
		{Kind: domain.PartKindText, Body: "final answer"},
		{Kind: domain.PartKindSystemNotice, Body: "provider warning", MetaJSON: `{"detail":"retry suggested"}`},
	})

	if !strings.Contains(got, "System") || !strings.Contains(got, "provider warning") || !strings.Contains(got, "retry suggested") {
		t.Fatalf("expected visible non-usage system notice, got %q", got)
	}
	if !strings.Contains(got, "final answer") {
		t.Fatalf("expected text to remain visible, got %q", got)
	}
}

func TestRenderMessagePartsShowsEventNotice(t *testing.T) {
	m := App{}

	got := newTranscriptRenderer(&m).renderMessageParts([]domain.Part{
		{Kind: domain.PartKindText, Body: "final answer"},
		{Kind: domain.PartKindEventNotice, Body: "Error: chat status 429"},
	})

	if !strings.Contains(got, "Error") || !strings.Contains(got, "chat status 429") {
		t.Fatalf("expected visible event notice, got %q", got)
	}
	if !strings.Contains(got, "final answer") {
		t.Fatalf("expected text to remain visible, got %q", got)
	}
}

func TestRenderMessagePartsSkipsLoopPauseEventNotice(t *testing.T) {
	m := App{}

	got := newTranscriptRenderer(&m).renderMessageParts([]domain.Part{
		{Kind: domain.PartKindText, Body: "final answer"},
		{Kind: domain.PartKindEventNotice, Body: "Paused continuation after repeated identical read calls.", MetaJSON: `{"kind":"loop_pause","reason":"repeated_tool","tool":"read"}`},
	})

	if strings.Contains(got, "Paused continuation") {
		t.Fatalf("expected loop pause notice to render as a card instead of inline text, got %q", got)
	}
	if !strings.Contains(got, "final answer") {
		t.Fatalf("expected text to remain visible, got %q", got)
	}
}

func TestRenderMessagePartsShowsAssistantNarrationWithoutSystemPrefix(t *testing.T) {
	m := App{}

	got := newTranscriptRenderer(&m).renderMessageParts([]domain.Part{
		{Kind: domain.PartKindText, Body: "There are two main functions. Let me check and remove the duplicate:"},
		{Kind: domain.PartKindToolCall, Body: `{"path":"main.go","tool":"read","tool_call_id":"call_1"}`, MetaJSON: `{"path":"main.go","tool":"read","tool_call_id":"call_1"}`},
	})

	if !strings.Contains(got, "There are two main functions") {
		t.Fatalf("expected narration text to remain visible, got %q", got)
	}
	if strings.Contains(got, "System") {
		t.Fatalf("expected narration text not to render as a system block, got %q", got)
	}
}

func TestTranscriptRendersCompactionAsExpandableCard(t *testing.T) {
	m := App{
		currentSession:   domain.Session{ID: 1},
		viewport:         newTranscriptViewport(120, 8),
		currentSnapshot:  chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
		expandedToolRuns: map[string]bool{},
		palette:          theme.Resolve("tokyonight").Palette,
	}
	m.currentSnapshot.Messages = []domain.Message{
		{ID: 1, Role: domain.MessageRoleAssistant, Summary: "Compacted session summary"},
	}
	m.currentSnapshot.Parts[1] = []domain.Part{{
		Kind:    domain.PartKindCompaction,
		Payload: domain.CompactionPayload{Summary: "## Goal\n\n- first\n- second", Status: "completed", BeforeContextTokens: 1234, AfterContextTokens: 456},
	}}

	m.refreshViewport()
	got := m.viewport.View()
	if !strings.Contains(got, "Compacted from 1234 context to 456 context.") {
		t.Fatalf("expected compaction card title, got %q", got)
	}
	if !strings.Contains(got, "Expand") {
		t.Fatalf("expected compaction card to be collapsed by default, got %q", got)
	}
	if strings.Contains(got, "- second") {
		t.Fatalf("expected collapsed compaction card to hide part of the body, got %q", got)
	}

	m.expandedToolRuns["compaction:1"] = true
	m.refreshViewport()
	got = m.viewport.View()
	if !strings.Contains(got, "## Goal") || !strings.Contains(got, "- first") || !strings.Contains(got, "- second") {
		t.Fatalf("expected expanded compaction body, got %q", got)
	}
}

func TestTranscriptRendersPendingCompactionAsRunningCard(t *testing.T) {
	m := App{
		currentSession:   domain.Session{ID: 1},
		viewport:         newTranscriptViewport(120, 8),
		currentSnapshot:  chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
		expandedToolRuns: map[string]bool{},
		palette:          theme.Resolve("tokyonight").Palette,
	}
	m.currentSnapshot.Messages = []domain.Message{
		{ID: 1, Role: domain.MessageRoleAssistant, Summary: "Compacting..."},
	}
	m.currentSnapshot.Parts[1] = []domain.Part{{
		Kind:    domain.PartKindCompaction,
		Payload: domain.CompactionPayload{Status: "pending"},
	}}

	m.refreshViewport()
	got := m.viewport.View()
	if !strings.Contains(got, "Compacting ...") {
		t.Fatalf("expected pending compaction card title, got %q", got)
	}
	if strings.Count(got, "Compacting ...") != 1 {
		t.Fatalf("expected pending compaction to render once, got %q", got)
	}
}

func TestTranscriptRendersLoopPauseAsCard(t *testing.T) {
	m := App{
		currentSession:   domain.Session{ID: 1},
		viewport:         newTranscriptViewport(80, 8),
		currentSnapshot:  chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
		expandedToolRuns: map[string]bool{},
		palette:          theme.Resolve("tokyonight").Palette,
	}
	m.currentSnapshot.Messages = []domain.Message{
		{ID: 1, Role: domain.MessageRoleAssistant, Summary: "Paused continuation"},
	}
	m.currentSnapshot.Parts[1] = []domain.Part{{
		Kind:     domain.PartKindEventNotice,
		Body:     "Paused continuation after 3 identical read calls with the same input.",
		MetaJSON: `{"kind":"loop_pause","reason":"repeated_tool","title":"Continuation paused","subtitle":"Repeated identical read calls","tool":"read"}`,
	}}

	m.refreshViewport()
	got := m.viewport.View()
	if !strings.Contains(got, "Continuation paused") {
		t.Fatalf("expected loop pause card title, got %q", got)
	}
	if !strings.Contains(got, "Paused") {
		t.Fatalf("expected loop pause card status, got %q", got)
	}
	if !strings.Contains(got, "Repeated identical read calls") {
		t.Fatalf("expected loop pause card subtitle, got %q", got)
	}
}

func TestRenderReasoningBlockStartsWithoutBlankLine(t *testing.T) {
	m := App{}

	got := newTranscriptRenderer(&m).renderReasoningBlock("thinking first")
	lines := strings.Split(got, "\n")
	if len(lines) < 1 {
		t.Fatalf("expected reasoning output, got %q", got)
	}
	if lines[0] == "" {
		t.Fatalf("expected first line to contain reasoning text, got %q", got)
	}
	if !strings.Contains(lines[0], "thinking first") {
		t.Fatalf("expected reasoning text on first line, got %q", got)
	}
}

func TestMouseWheelScrollsViewport(t *testing.T) {
	composer := textarea.New()
	composer.SetHeight(composerInputHeight)
	composer.BlinkEnabled = false
	m := App{
		cfg:          testConfig(t),
		palette:      theme.Default().Palette,
		width:        40,
		height:       8,
		composer:     composer,
		mouseEnabled: true,
		viewport:     newTranscriptViewport(40, 4),
	}
	m.viewport.SetContent(strings.Join([]string{
		"line 1",
		"line 2",
		"line 3",
		"line 4",
		"line 5",
		"line 6",
		"line 7",
		"line 8",
	}, "\n"))

	updated, cmd := m.Update(ui.MouseMsg{
		Action: ui.MouseActionPress,
		Button: ui.MouseButtonWheelDown,
		X:      5,
		Y:      1,
	})
	next := updated.(App)
	if cmd != nil {
		t.Fatal("expected no command from mouse wheel scroll")
	}
	if next.viewport.YOffset == 0 {
		t.Fatalf("expected viewport to scroll, got y offset %d", next.viewport.YOffset)
	}
}

func TestMouseWheelOverComposerDoesNotScrollTranscript(t *testing.T) {
	composer := textarea.New()
	composer.SetHeight(composerInputHeight)
	composer.Focus()
	composer.BlinkEnabled = false
	m := App{
		cfg:          testConfig(t),
		palette:      theme.Default().Palette,
		width:        40,
		height:       8,
		composer:     composer,
		mouseEnabled: true,
		viewport:     newTranscriptViewport(40, 4),
	}
	m.viewport.SetContent(strings.Join([]string{
		"line 1",
		"line 2",
		"line 3",
		"line 4",
		"line 5",
		"line 6",
		"line 7",
		"line 8",
	}, "\n"))
	m.resize()
	m.refreshViewport()
	before := m.viewport.YOffset

	updated, _ := m.Update(ui.MouseMsg{
		Action: ui.MouseActionPress,
		Button: ui.MouseButtonWheelUp,
		X:      5,
		Y:      m.height - 1,
	})
	next := updated.(App)
	if next.viewport.YOffset != before {
		t.Fatalf("expected composer wheel to leave transcript offset %d, got %d", before, next.viewport.YOffset)
	}
}

func TestMouseWheelScrollRefreshesRetainedTranscriptSurface(t *testing.T) {
	composer := textarea.New()
	composer.Placeholder = "Ask koder or type / for commands"
	composer.SetHeight(composerInputHeight)
	composer.Focus()

	m := App{
		cfg:            testConfig(t),
		palette:        theme.Resolve("tokyonight").Palette,
		mouseEnabled:   true,
		width:          40,
		height:         12,
		composer:       composer,
		currentSession: domain.Session{ID: 1},
		currentSnapshot: chatpkg.Snapshot{Messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleAssistant},
		}, Parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindText, Body: strings.Join([]string{
				"line 1",
				"line 2",
				"line 3",
				"line 4",
				"line 5",
				"line 6",
				"line 7",
				"line 8",
				"line 9",
				"line 10",
				"line 11",
				"line 12",
				"line 13",
				"line 14",
				"line 15",
				"line 16",
			}, "\n")}},
		}},
		viewport: newTranscriptViewport(37, 8),
	}

	m.resize()
	m.refreshViewport()
	if m.viewport.YOffset == 0 {
		t.Fatalf("expected retained transcript to start below top for scroll test, got offset %d", m.viewport.YOffset)
	}
	before := strings.Join(m.viewSurface().Lines(), "\n")
	if !strings.Contains(before, "line 12") {
		t.Fatalf("expected initial view to show transcript tail, got:\n%s", before)
	}

	updated, _ := m.Update(ui.MouseMsg{
		Action: ui.MouseActionPress,
		Button: ui.MouseButtonWheelUp,
		X:      5,
		Y:      3,
	})
	next := updated.(App)
	after := strings.Join(next.viewSurface().Lines(), "\n")
	if !strings.Contains(after, "line 5") {
		t.Fatalf("expected scrolled retained transcript to show earlier lines, got:\n%s", after)
	}
	if strings.Contains(after, "line 16") {
		t.Fatalf("expected scrolled retained transcript surface to change, got:\n%s", after)
	}
}

func TestMouseWheelScrollCanReturnToTrueTranscriptBottom(t *testing.T) {
	composer := textarea.New()
	composer.Placeholder = "Ask koder or type / for commands"
	composer.SetHeight(composerInputHeight)
	composer.Focus()

	m := App{
		cfg:          testConfig(t),
		palette:      theme.Resolve("tokyonight").Palette,
		mouseEnabled: true,
		width:        40,
		height:       12,
		composer:     composer,
		currentSession: domain.Session{
			ID: 1,
		},
		currentSnapshot: chatpkg.Snapshot{Messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleAssistant},
		}, Parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindText, Body: strings.Join([]string{
				"line 1",
				"line 2",
				"line 3",
				"line 4",
				"line 5",
				"line 6",
				"line 7",
				"line 8",
				"line 9",
				"line 10",
				"line 11",
				"line 12",
				"line 13",
				"line 14",
				"line 15",
				"line 16",
			}, "\n")}},
		}},
		viewport: newTranscriptViewport(37, 8),
	}

	m.resize()
	m.refreshViewport()

	updated, _ := m.Update(ui.MouseMsg{
		Action: ui.MouseActionPress,
		Button: ui.MouseButtonWheelUp,
		X:      5,
		Y:      3,
	})
	scrolledUp := updated.(App)
	if scrolledUp.viewport.YOffset >= m.viewport.YOffset {
		t.Fatalf("expected upward wheel scroll to reduce offset from %d, got %d", m.viewport.YOffset, scrolledUp.viewport.YOffset)
	}

	current := scrolledUp
	for range 8 {
		updated, _ = current.Update(ui.MouseMsg{
			Action: ui.MouseActionPress,
			Button: ui.MouseButtonWheelDown,
			X:      5,
			Y:      3,
		})
		current = updated.(App)
	}

	got := strings.Join(current.viewSurface().Lines(), "\n")
	if !strings.Contains(got, "line 16") {
		t.Fatalf("expected scrolling back down to reach transcript tail, got:\n%s", got)
	}
	if current.viewport.YOffset != current.viewport.maxYOffset() {
		t.Fatalf("expected final offset %d at bottom, got %d", current.viewport.maxYOffset(), current.viewport.YOffset)
	}
}

func TestMouseClickTogglesToolRunExpansion(t *testing.T) {
	m := App{
		mouseEnabled:            true,
		currentSession:          domain.Session{ID: 1},
		width:                   100,
		height:                  20,
		viewport:                newTranscriptViewport(80, 8),
		currentSnapshot:         chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
		expandedToolRuns:        map[string]bool{},
		expandedToolRunCommands: map[string]bool{},
		palette:                 theme.Resolve("tokyonight").Palette,
	}
	m.currentSnapshot.Messages = []domain.Message{
		{ID: 1, Role: domain.MessageRoleTool, Summary: "bash"},
	}
	m.currentSnapshot.Parts[1] = []domain.Part{{
		Kind:     domain.PartKindToolOutput,
		Body:     "line one\nline two",
		MetaJSON: `{"tool":"bash","command":"echo hi","tool_call_id":"call_bash_1"}`,
	}}

	m.refreshViewport()
	if strings.Contains(m.viewport.View(), "line one\nline two") {
		t.Fatalf("expected collapsed tool output, got %q", m.viewport.View())
	}
	if !strings.Contains(m.viewport.View(), "Expand ou") {
		t.Fatalf("expected expand indicator, got %q", m.viewport.View())
	}

	_ = m.renderBody()
	clickX := -1
	clickY := -1
	controlWidth := -1
	for _, control := range m.ensureMainScreenView().TranscriptControls() {
		if control.ID != "toolrun:call_bash_1:output" {
			continue
		}
		clickX = control.Rect.X + 1
		clickY = control.Rect.Y
		controlWidth = control.Rect.W
		break
	}
	if clickX < 0 || clickY < 0 {
		t.Fatalf("expected toolrun control to be registered, got %#v", m.ensureMainScreenView().TranscriptControls())
	}
	if controlWidth != ui.PlainWidth("Expand output (1 line)") {
		t.Fatalf("expected expand control width %d, got %d", ui.PlainWidth("Expand output (1 line)"), controlWidth)
	}

	updated, cmd := m.Update(ui.MouseMsg{
		Action: ui.MouseActionPress,
		Button: ui.MouseButtonLeft,
		X:      clickX,
		Y:      clickY,
	})
	var next App
	switch typed := updated.(type) {
	case App:
		next = typed
	case *App:
		next = *typed
	default:
		t.Fatalf("unexpected model type %T", updated)
	}
	if cmd != nil {
		t.Fatal("expected no command from tool run mouse toggle")
	}
	if !strings.Contains(next.viewport.View(), "line one") {
		t.Fatalf("expected expanded tool output, got %q", next.viewport.View())
	}
	if !strings.Contains(next.viewport.View(), "Collapse") {
		t.Fatalf("expected collapse indicator, got %q", next.viewport.View())
	}

	updated, _ = next.Update(ui.MouseMsg{
		Action: ui.MouseActionPress,
		Button: ui.MouseButtonLeft,
		X:      clickX,
		Y:      clickY,
	})
	var final App
	switch typed := updated.(type) {
	case App:
		final = typed
	case *App:
		final = *typed
	default:
		t.Fatalf("unexpected model type %T", updated)
	}
	if strings.Contains(final.viewport.View(), "line one\nline two") {
		t.Fatalf("expected collapsed tool output after second click, got %q", final.viewport.View())
	}
}

func TestMouseClickTogglesToolRunExpansionWhileBusy(t *testing.T) {
	m := App{
		mouseEnabled:   true,
		currentSession: domain.Session{ID: 1},
		width:          100,
		height:         20,
		viewport:       newTranscriptViewport(80, 8),
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{}, PendingAssistant: chatpkg.PendingAssistantTurn{
			Text: "partial answer",
		}},
	}
	m.currentSnapshot.Messages = []domain.Message{
		{ID: 1, Role: domain.MessageRoleTool, Summary: "bash"},
	}
	m.currentSnapshot.Parts[1] = []domain.Part{{
		Kind:     domain.PartKindToolOutput,
		Body:     "line one\nline two",
		MetaJSON: `{"tool":"bash","command":"echo hi","tool_call_id":"call_bash_1"}`,
	}}

	m.refreshViewport()
	beforeRendered := m.renderBody()

	clickX := -1
	clickY := -1
	for _, control := range m.ensureMainScreenView().TranscriptControls() {
		if control.ID != "toolrun:call_bash_1:output" {
			continue
		}
		clickX = control.Rect.X + 1
		clickY = control.Rect.Y
		break
	}
	if clickX < 0 || clickY < 0 {
		t.Fatalf("expected toolrun control to be registered, got %#v", m.ensureMainScreenView().TranscriptControls())
	}

	before := m.renderBody()
	updated, cmd := m.Update(ui.MouseMsg{
		Action: ui.MouseActionPress,
		Button: ui.MouseButtonLeft,
		X:      clickX,
		Y:      clickY,
	})
	var next App
	switch typed := updated.(type) {
	case App:
		next = typed
	case *App:
		next = *typed
	default:
		t.Fatalf("unexpected model type %T", updated)
	}
	if cmd != nil {
		t.Fatal("expected no command from tool run mouse toggle")
	}
	after := next.renderBody()
	if before == after {
		t.Fatalf("expected click while busy to refresh viewport, before=%q after=%q", before, after)
	}
	if !strings.Contains(after, "line one") {
		t.Fatalf("expected expanded tool output while busy, got %q", after)
	}
	afterRendered := next.renderBody()
	if beforeRendered == afterRendered {
		t.Fatalf("expected busy click to change rendered surface, before=%q after=%q", beforeRendered, afterRendered)
	}

	updated, cmd = next.Update(spinnerTickMsg{})
	if cmd != nil {
		t.Fatal("expected no spinner follow-up when busy state is not animating")
	}
	switch typed := updated.(type) {
	case App:
		next = typed
	case *App:
		next = *typed
	default:
		t.Fatalf("unexpected model type after spinner tick %T", updated)
	}
	if !strings.Contains(next.viewport.View(), "line one") {
		t.Fatalf("expected expanded tool output to persist across busy refresh, got %q", next.viewport.View())
	}
}

func TestMouseClickResyncsTranscriptControlsDuringBusy(t *testing.T) {
	m := App{
		mouseEnabled:            true,
		currentSession:          domain.Session{ID: 1},
		width:                   100,
		height:                  20,
		viewport:                newTranscriptViewport(80, 8),
		currentSnapshot:         chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
		expandedToolRuns:        map[string]bool{},
		expandedToolRunCommands: map[string]bool{},
		palette:                 theme.Resolve("tokyonight").Palette,
		loading:                 true,
		busy: busyModel{
			active: true,
			scope:  busyScopeTranscript,
			status: "Working ...",
			spinner: spinnerModel{
				active: true,
			},
		},
	}
	m.currentSnapshot.Messages = []domain.Message{
		{ID: 1, Role: domain.MessageRoleTool, Summary: "bash"},
	}
	m.currentSnapshot.Parts[1] = []domain.Part{{
		Kind:     domain.PartKindToolOutput,
		Body:     "line one\nline two",
		MetaJSON: `{"tool":"bash","command":"echo hi","tool_call_id":"call_bash_1"}`,
	}}

	m.refreshViewport()
	_ = m.renderBody()
	if len(m.ensureMainScreenView().TranscriptControls()) == 0 {
		t.Fatal("expected initial transcript controls")
	}

	m.currentSnapshot.Messages = append([]domain.Message{{ID: 2, Role: domain.MessageRoleAssistant, Summary: "lead in"}}, m.currentSnapshot.Messages...)
	m.currentSnapshot.Parts[2] = []domain.Part{{
		Kind: domain.PartKindText,
		Body: "prepended line\nsecond line",
	}}
	m.invalidateTranscript()
	rendered := m.renderBody()
	lines := strings.Split(rendered, "\n")
	clickX := -1
	clickY := -1
	for y, line := range lines {
		idx := strings.Index(line, "Expand output (1 line)")
		if idx < 0 {
			continue
		}
		clickX = idx + 1
		clickY = y
		break
	}
	if clickX < 0 || clickY < 0 {
		t.Fatalf("expected rendered body to include expand control, got %q", rendered)
	}

	updated, cmd := m.Update(ui.MouseMsg{
		Action: ui.MouseActionPress,
		Button: ui.MouseButtonLeft,
		X:      clickX,
		Y:      clickY,
	})
	var next App
	switch typed := updated.(type) {
	case App:
		next = typed
	case *App:
		next = *typed
	default:
		t.Fatalf("unexpected model type %T", updated)
	}
	if cmd != nil {
		t.Fatal("expected no command from busy tool run mouse toggle")
	}
	if !strings.Contains(next.viewport.View(), "line one") {
		t.Fatalf("expected resynced busy click to expand tool output, got %q", next.viewport.View())
	}
}

func TestMouseClickTogglesEditToolRunExpansion(t *testing.T) {
	m := App{
		mouseEnabled:     true,
		currentSession:   domain.Session{ID: 1},
		viewport:         newTranscriptViewport(80, 8),
		currentSnapshot:  chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
		expandedToolRuns: map[string]bool{},
		palette:          theme.Resolve("tokyonight").Palette,
	}

	meta := mustMarshalMeta(t, tools.MetaWithStoredResult(map[string]string{
		"tool":         "edit",
		"path":         "game/sim_test.go",
		"tool_call_id": "call_edit_1",
	}, domain.PartKindToolOutput, domain.ToolKindEdit, tools.StoredResultStatusOK, tools.EditStoredResult{
		Path:    "game/sim_test.go",
		Summary: "Edited game/sim_test.go (replaced 1 occurrence)",
		Diff:    "--- game/sim_test.go\n+++ game/sim_test.go\n@@ -1,3 +1,3 @@\n before\n-old\n+new\n after",
	}))

	m.currentSnapshot.Messages = []domain.Message{
		{ID: 1, Role: domain.MessageRoleTool, Summary: "edit"},
	}
	m.currentSnapshot.Parts[1] = []domain.Part{{
		Kind:     domain.PartKindToolOutput,
		Body:     "Edited game/sim_test.go (replaced 1 occurrence)",
		MetaJSON: meta,
	}}

	m.refreshViewport()
	if !strings.Contains(m.viewport.View(), "(-1 +1)") {
		t.Fatalf("expected collapsed edit summary, got %q", m.viewport.View())
	}
	for _, want := range []string{"1 | before", "2 | old", "2 | new", "3 | after"} {
		if !strings.Contains(m.viewport.View(), want) {
			t.Fatalf("expected short edit diff inline detail %q, got %q", want, m.viewport.View())
		}
	}
	if strings.Contains(m.viewport.View(), "--- game/sim_test.go") || strings.Contains(m.viewport.View(), "@@ -1,3 +1,3 @@") {
		t.Fatalf("expected short edit diff inline, got %q", m.viewport.View())
	}
	if strings.Contains(m.viewport.View(), "Expand (") {
		t.Fatalf("expected short edit to avoid expand control, got %q", m.viewport.View())
	}

	_ = m.renderBody()
	clickX := -1
	clickY := -1
	for _, control := range m.ensureMainScreenView().TranscriptControls() {
		if control.ID != "toolrun:call_edit_1:output" {
			continue
		}
		clickX = control.Rect.X + 1
		clickY = control.Rect.Y
		break
	}
	if clickX >= 0 || clickY >= 0 {
		t.Fatalf("expected no expand control for short edit diff, got %#v", m.ensureMainScreenView().TranscriptControls())
	}
}

func TestMouseClickTogglesWrappedEditToolRunExpansion(t *testing.T) {
	m := App{
		mouseEnabled:     true,
		currentSession:   domain.Session{ID: 1},
		viewport:         newTranscriptViewport(28, 30),
		currentSnapshot:  chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
		expandedToolRuns: map[string]bool{},
		palette:          theme.Resolve("tokyonight").Palette,
	}

	meta := mustMarshalMeta(t, tools.MetaWithStoredResult(map[string]string{
		"tool":         "edit",
		"path":         "game/sim_test.go",
		"tool_call_id": "call_edit_wrap_1",
	}, domain.PartKindToolOutput, domain.ToolKindEdit, tools.StoredResultStatusOK, tools.EditStoredResult{
		Path:    "game/sim_test.go",
		Summary: "Edited game/sim_test.go (replaced 1 occurrence)",
		Diff:    "--- game/sim_test.go\n+++ game/sim_test.go\n@@ -1,22 +1,22 @@\n before\n-old1\n-old2\n-old3\n-old4\n-old5\n-old6\n-old7\n-old8\n-old9\n-old10\n+new1\n+new2\n+new3\n+new4\n+new5\n+new6\n+new7\n+new8\n+new9\n+new10\n after",
	}))

	m.currentSnapshot.Messages = []domain.Message{
		{ID: 1, Role: domain.MessageRoleTool, Summary: "edit"},
	}
	m.currentSnapshot.Parts[1] = []domain.Part{{
		Kind:     domain.PartKindToolOutput,
		Body:     "Edited game/sim_test.go (replaced 1 occurrence)",
		MetaJSON: meta,
	}}

	m.refreshViewport()
	_ = m.renderBody()
	var wrappedControl ui.Control
	wrappedControlOK := false
	for _, control := range m.ensureMainScreenView().TranscriptControls() {
		if control.ID == "toolrun:call_edit_wrap_1:output" {
			wrappedControl = control
			wrappedControlOK = true
			break
		}
	}
	if !wrappedControlOK {
		t.Fatalf("expected wrapped edit toolrun control to be registered, got %#v", m.ensureMainScreenView().TranscriptControls())
	}

	updated, cmd := m.Update(ui.MouseMsg{
		Action: ui.MouseActionPress,
		Button: ui.MouseButtonLeft,
		X:      wrappedControl.Rect.X + 1,
		Y:      wrappedControl.Rect.Y,
	})
	var next App
	switch typed := updated.(type) {
	case App:
		next = typed
	case *App:
		next = *typed
	default:
		t.Fatalf("unexpected model type %T", updated)
	}
	if cmd != nil {
		t.Fatal("expected no command from wrapped edit tool run mouse toggle")
	}
	got := next.renderBody()
	if !strings.Contains(got, "1 | before") || !strings.Contains(got, "2 | old1") || !strings.Contains(got, "11 | new10") {
		t.Fatalf("expected expanded wrapped edit output, got %q", got)
	}
}

func TestWriteToolRunUsesStoredContentForExpansion(t *testing.T) {
	m := App{
		currentSession:   domain.Session{ID: 1},
		viewport:         newTranscriptViewport(80, 8),
		currentSnapshot:  chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
		expandedToolRuns: map[string]bool{},
		palette:          theme.Resolve("tokyonight").Palette,
	}

	meta, err := json.Marshal(tools.MetaWithStoredResult(map[string]string{
		"tool":         "write",
		"path":         "note.txt",
		"tool_call_id": "call_write_1",
	}, domain.PartKindToolOutput, domain.ToolKindWrite, tools.StoredResultStatusOK, tools.WriteStoredResult{
		Path:    "note.txt",
		Action:  "created",
		Summary: "Created note.txt",
		Content: "line one\nline two",
	}))
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}

	m.currentSnapshot.Messages = []domain.Message{
		{ID: 1, Role: domain.MessageRoleTool, Summary: "write"},
	}
	m.currentSnapshot.Parts[1] = []domain.Part{{
		Kind:     domain.PartKindToolOutput,
		Body:     "Created note.txt",
		MetaJSON: string(meta),
	}}

	m.refreshViewport()
	if strings.Contains(m.viewport.View(), " line one\n line two") {
		t.Fatalf("expected collapsed write output, got %q", m.viewport.View())
	}
	if !strings.Contains(m.viewport.View(), "Expand (1 line)") {
		t.Fatalf("expected expand indicator, got %q", m.viewport.View())
	}

	m.expandedToolRuns["call_write_1"] = true
	m.refreshViewport()
	if !strings.Contains(m.viewport.View(), " line one") || !strings.Contains(m.viewport.View(), " line two") {
		t.Fatalf("expected expanded write content, got %q", m.viewport.View())
	}
}

func TestEditToolRunShowsStoredHunkDetails(t *testing.T) {
	m := App{
		currentSession:   domain.Session{ID: 1},
		viewport:         newTranscriptViewport(80, 10),
		currentSnapshot:  chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
		expandedToolRuns: map[string]bool{"call_edit_1": true},
		palette:          theme.Resolve("tokyonight").Palette,
	}

	meta := mustMarshalMeta(t, tools.MetaWithStoredResult(map[string]string{
		"tool":         "edit",
		"path":         "game/map.go",
		"tool_call_id": "call_edit_1",
	}, domain.PartKindToolOutput, domain.ToolKindEdit, tools.StoredResultStatusOK, tools.EditStoredResult{
		Path:    "game/map.go",
		Summary: "Edited game/map.go (replaced 1 occurrence)",
		Diff:    "--- game/map.go\n+++ game/map.go\n@@ -11,3 +11,3 @@\n before\n-if oldCondition {\n+if newCondition {\n after",
	}))

	m.currentSnapshot.Messages = []domain.Message{
		{ID: 1, Role: domain.MessageRoleTool, Summary: "edit"},
	}
	m.currentSnapshot.Parts[1] = []domain.Part{{
		Kind:     domain.PartKindToolOutput,
		Body:     "Edited game/map.go (replaced 1 occurrence)",
		MetaJSON: meta,
	}}

	m.refreshViewport()
	got := m.viewport.View()
	for _, want := range []string{
		"Edited game/map.go  (-1 +1)",
		"11 | before",
		"12 | if oldCondition {",
		"12 | if newCondition {",
		"13 | after",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in %q", want, got)
		}
	}
	if strings.Contains(got, "--- game/map.go") || strings.Contains(got, "@@ -11,3 +11,3 @@") {
		t.Fatalf("expected edit detail to omit diff headers, got %q", got)
	}
}

func TestBashToolRunUsesRanCommandTitleAndCollapsedFirstOutputLine(t *testing.T) {
	m := App{
		currentSession:          domain.Session{ID: 1},
		viewport:                newTranscriptViewport(100, 8),
		currentSnapshot:         chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
		expandedToolRuns:        map[string]bool{},
		expandedToolRunCommands: map[string]bool{},
		palette:                 theme.Resolve("tokyonight").Palette,
	}

	meta := mustMarshalMeta(t, tools.MetaWithStoredResult(map[string]string{
		"tool":         "bash",
		"command":      "printf 'hello\\nworld\\n'",
		"tool_call_id": "call_bash_1",
	}, domain.PartKindToolOutput, domain.ToolKindBash, tools.StoredResultStatusOK, tools.BashStoredResult{
		Command: "printf 'hello\\nworld\\n'",
		Output:  "line one\nline two\n",
	}))

	m.currentSnapshot.Messages = []domain.Message{
		{ID: 1, Role: domain.MessageRoleTool, Summary: "bash"},
	}
	m.currentSnapshot.Parts[1] = []domain.Part{{
		Kind:     domain.PartKindToolOutput,
		Body:     "line one\nline two\n",
		MetaJSON: meta,
	}}

	m.refreshViewport()
	got := m.viewport.View()
	if !strings.Contains(got, "Ran command printf 'hello\\nworld\\n'") {
		t.Fatalf("expected bash title to include command, got %q", got)
	}
	if strings.Contains(got, "world\\n'  Expand command") {
		t.Fatalf("expected multiline command to stay out of collapsed header, got %q", got)
	}
	if !strings.Contains(got, "line one") {
		t.Fatalf("expected collapsed bash card to show first output line, got %q", got)
	}
	if strings.Contains(got, "line two") {
		t.Fatalf("expected collapsed bash card to hide later output lines, got %q", got)
	}
	if !strings.Contains(got, "Expand output (1 line)") {
		t.Fatalf("expected output expand label, got %q", got)
	}
}

func TestResumedBashToolRunReplacesRequestTitleWithCompletedTitle(t *testing.T) {
	m := App{
		currentSession:          domain.Session{ID: 1},
		viewport:                newTranscriptViewport(100, 8),
		currentSnapshot:         chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
		expandedToolRuns:        map[string]bool{},
		expandedToolRunCommands: map[string]bool{},
		palette:                 theme.Resolve("tokyonight").Palette,
	}

	callMeta := mustMarshalMeta(t, map[string]string{
		"tool":         "bash",
		"command":      "printf 'line one\\nline two\\n'",
		"tool_call_id": "call_bash_1",
	})
	outputMeta := mustMarshalMeta(t, tools.MetaWithStoredResult(map[string]string{
		"tool":         "bash",
		"command":      "printf 'line one\\nline two\\n'",
		"tool_call_id": "call_bash_1",
	}, domain.PartKindToolOutput, domain.ToolKindBash, tools.StoredResultStatusOK, tools.BashStoredResult{
		Command: "printf 'line one\\nline two\\n'",
		Output:  "line one\nline two\n",
	}))

	m.currentSnapshot.Messages = []domain.Message{
		{ID: 1, Role: domain.MessageRoleAssistant, Summary: "tool:bash"},
		{ID: 2, Role: domain.MessageRoleTool, Summary: "bash"},
	}
	m.currentSnapshot.Parts[1] = []domain.Part{{
		Kind:     domain.PartKindToolCall,
		Body:     `{"command":"printf 'line one\\nline two\\n'","tool":"bash","tool_call_id":"call_bash_1"}`,
		MetaJSON: callMeta,
	}}
	m.currentSnapshot.Parts[2] = []domain.Part{{
		Kind:     domain.PartKindToolOutput,
		Body:     "line one\nline two\n",
		MetaJSON: outputMeta,
	}}

	m.refreshViewport()
	got := m.viewport.View()
	if !strings.Contains(got, "Ran command printf 'line one\\nline two\\n'") {
		t.Fatalf("expected resumed bash title to include command, got %q", got)
	}
	if strings.Contains(got, "Run command") {
		t.Fatalf("expected resumed bash title to replace request title, got %q", got)
	}
}

func TestResumedEditToolRunReplacesRequestTitleWithCompletedTitle(t *testing.T) {
	m := App{
		currentSession:   domain.Session{ID: 1},
		viewport:         newTranscriptViewport(100, 8),
		currentSnapshot:  chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
		expandedToolRuns: map[string]bool{},
		palette:          theme.Resolve("tokyonight").Palette,
	}

	callMeta := mustMarshalMeta(t, map[string]string{
		"tool":         "edit",
		"path":         "game/map.go",
		"tool_call_id": "call_edit_1",
	})
	outputMeta := mustMarshalMeta(t, tools.MetaWithStoredResult(map[string]string{
		"tool":         "edit",
		"path":         "game/map.go",
		"tool_call_id": "call_edit_1",
	}, domain.PartKindToolOutput, domain.ToolKindEdit, tools.StoredResultStatusOK, tools.EditStoredResult{
		Path:    "game/map.go",
		Summary: "Edited game/map.go (replaced 1 occurrence)",
	}))

	m.currentSnapshot.Messages = []domain.Message{
		{ID: 1, Role: domain.MessageRoleAssistant, Summary: "tool:edit"},
		{ID: 2, Role: domain.MessageRoleTool, Summary: "edit"},
	}
	m.currentSnapshot.Parts[1] = []domain.Part{{
		Kind:     domain.PartKindToolCall,
		Body:     `{"path":"game/map.go","tool":"edit","tool_call_id":"call_edit_1"}`,
		MetaJSON: callMeta,
	}}
	m.currentSnapshot.Parts[2] = []domain.Part{{
		Kind:     domain.PartKindToolOutput,
		Body:     "Edited game/map.go (replaced 1 occurrence)",
		MetaJSON: outputMeta,
	}}

	m.refreshViewport()
	got := m.viewport.View()
	if !strings.Contains(got, "Edited game/map.go") {
		t.Fatalf("expected resumed edit title to include path, got %q", got)
	}
	if strings.Contains(got, "Edit file") {
		t.Fatalf("expected resumed edit title to replace request title, got %q", got)
	}
}

func TestMouseClickOnApprovalPromptPermissionsOpensPicker(t *testing.T) {
	m := App{
		mouseEnabled: true,
		width:        160,
		height:       28,
		palette:      theme.Resolve("tokyonight").Palette,
		composer:     textarea.New(),
		currentSnapshot: chatpkg.Snapshot{Approvals: []store.Approval{{
			ID:      7,
			Tool:    domain.ToolKindRead,
			Command: `{"path":"README.md"}`,
		}}},
	}

	prompt := m.renderApprovalPrompt()
	lines := strings.Split(prompt, "\n")
	buttonLine := -1
	buttonX := -1
	for idx, line := range lines {
		stripped := ansi.Strip(line)
		if !strings.Contains(stripped, "Switch permissions") {
			continue
		}
		buttonLine = idx
		buttonX = strings.Index(stripped, "Switch permissions") + 1
		break
	}
	if buttonLine < 0 || buttonX < 0 {
		t.Fatalf("failed to find approval dialog buttons in view: %q", prompt)
	}

	bounds := m.centeredWindowBounds(m.renderApprovalDialogElement())
	updated, cmd := m.Update(ui.MouseMsg{
		Action: ui.MouseActionPress,
		Button: ui.MouseButtonLeft,
		X:      bounds.X + buttonX,
		Y:      bounds.Y + buttonLine,
	})
	next := updated.(*App)
	if cmd == nil {
		t.Fatal("expected title sync command when opening permissions picker")
	}
	if !next.hasPicker() {
		t.Fatal("expected permissions picker to open from approval dialog mouse click")
	}
	if next.picker.mode != pickerModePermissions {
		t.Fatalf("expected permissions picker mode, got %v", next.picker.mode)
	}
	if next.picker.approvalID != 7 {
		t.Fatalf("expected approval picker to target approval 7, got %d", next.picker.approvalID)
	}
}

func TestEventMsgAppendsToolResultBeforeTurnCompletes(t *testing.T) {
	now := time.Now().UTC()
	msg := domain.Message{
		ID:        20,
		SessionID: 1,
		ChatID:    2,
		Role:      domain.MessageRoleTool,
		Summary:   "bash",
		CreatedAt: now,
	}
	parts := []domain.Part{{
		ID:        21,
		MessageID: msg.ID,
		Kind:      domain.PartKindToolOutput,
		Payload: domain.ToolOutputPayload{
			Tool:       domain.ToolKindBash,
			ToolCallID: "call_1",
			Status:     domain.ToolResultStatusOK,
			Text:       "file-a\nfile-b",
			Result:     tools.BashStoredResult{Command: "pwd", Output: "file-a\nfile-b"},
		},
		CreatedAt: now,
	}}
	m := App{
		currentSession:  domain.Session{ID: 1},
		currentChat:     domain.Chat{ID: 2},
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
	}
	events := make(chan domain.Event)
	defer close(events)

	updated, cmd := m.Update(eventMsg{
		chatID: 2,
		event: domain.Event{
			Kind:       domain.EventKindToolResult,
			Tool:       domain.ToolKindBash,
			ToolCallID: "call_1",
			Text:       "file-a\nfile-b",
			Message:    msg,
			Parts:      parts,
		},
		events: events,
	})
	next := updated.(App)
	if next.status != "" {
		t.Fatalf("unexpected status: %q", next.status)
	}
	if cmd == nil {
		t.Fatal("expected follow-up command")
	}
	if len(next.currentSnapshot.Messages) != 1 {
		t.Fatalf("expected one appended message, got %d", len(next.currentSnapshot.Messages))
	}
	if got := next.currentSnapshot.Parts[next.currentSnapshot.Messages[0].ID][0].Text(); got != "file-a\nfile-b" {
		t.Fatalf("unexpected in-memory tool output: %q", got)
	}
}

func TestRenderTranscriptMessageUsesUserStyleWithoutRoleLabel(t *testing.T) {
	cfg := testConfig(t)
	m := App{
		cfg: cfg,
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindText, Body: "hello world"}},
		}},
	}

	got := m.renderTranscriptMessage(domain.Message{
		ID:   1,
		Role: domain.MessageRoleUser,
	})

	if !strings.Contains(got, "hello world") {
		t.Fatalf("expected user body in transcript, got %q", got)
	}
	if strings.Contains(got, "[user]") || strings.Contains(got, "[assistant]") {
		t.Fatalf("expected no bracketed role labels, got %q", got)
	}
}

func TestRenderTranscriptMessageUserBubbleHasBlankPaddingLines(t *testing.T) {
	cfg := testConfig(t)
	m := App{
		cfg: cfg,
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindText, Body: "hello world"}},
		}},
		viewport: newTranscriptViewport(24, 6),
	}

	got := m.renderTranscriptMessage(domain.Message{
		ID:   1,
		Role: domain.MessageRoleUser,
	})

	lines := strings.Split(got, "\n")
	if len(lines) < 3 {
		t.Fatalf("expected padded user bubble, got %q", got)
	}
	if !strings.Contains(lines[0], "▄") {
		t.Fatalf("expected half-block top line, got %q", lines[0])
	}
	if !strings.Contains(lines[1], "█") || strings.TrimSpace(strings.ReplaceAll(lines[1], "█", "")) != "hello world" {
		t.Fatalf("expected padded body line, got %q", lines[1])
	}
	if !strings.Contains(lines[len(lines)-1], "▀") {
		t.Fatalf("expected half-block bottom line, got %q", lines[len(lines)-1])
	}
	wantWidth := ui.TextWidth(lines[1])
	if wantWidth <= 2 {
		t.Fatalf("expected padded width, got %d from %q", wantWidth, lines[1])
	}
	if got := ui.TextWidth(lines[0]); got != wantWidth {
		t.Fatalf("expected blank top line width %d, got %d", wantWidth, got)
	}
	if got := ui.TextWidth(lines[len(lines)-1]); got != wantWidth {
		t.Fatalf("expected blank bottom line width %d, got %d", wantWidth, got)
	}
}

func TestRenderTranscriptMessageUserBubbleUsesConsistentWidthForMultilineInput(t *testing.T) {
	cfg := testConfig(t)
	m := App{
		cfg: cfg,
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindText, Body: "short\nthis is a much longer line"}},
		}},
		viewport: newTranscriptViewport(30, 6),
	}

	got := m.renderTranscriptMessage(domain.Message{
		ID:   1,
		Role: domain.MessageRoleUser,
	})

	lines := strings.Split(got, "\n")
	if len(lines) < 4 {
		t.Fatalf("expected multiline bubble, got %q", got)
	}
	wantWidth := ui.TextWidth(lines[1])
	for idx, line := range lines {
		if gotWidth := ui.TextWidth(line); gotWidth != wantWidth {
			t.Fatalf("expected consistent line width %d at line %d, got %d from %q", wantWidth, idx, gotWidth, line)
		}
	}
}

func TestRenderTranscriptMessageUserBubbleWrapsToViewportWidth(t *testing.T) {
	cfg := testConfig(t)
	m := App{
		cfg: cfg,
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindText, Body: "this line is intentionally longer than the viewport width"}},
		}},
		viewport: newTranscriptViewport(18, 6),
	}

	got := m.renderTranscriptMessage(domain.Message{
		ID:   1,
		Role: domain.MessageRoleUser,
	})

	lines := strings.Split(got, "\n")
	if len(lines) < 4 {
		t.Fatalf("expected wrapped bubble lines, got %q", got)
	}
	wantWidth := ui.TextWidth(lines[0])
	for idx, line := range lines {
		if gotWidth := ui.TextWidth(line); gotWidth != wantWidth {
			t.Fatalf("expected wrapped line width %d at line %d, got %d from %q", wantWidth, idx, gotWidth, line)
		}
	}
}

func TestRenderTranscriptMessageUsesAssistantStyleWithoutRoleLabel(t *testing.T) {
	m := App{
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{
			2: {{Kind: domain.PartKindText, Body: "final answer"}},
		}},
	}

	got := m.renderTranscriptMessage(domain.Message{
		ID:   2,
		Role: domain.MessageRoleAssistant,
	})

	if !strings.Contains(got, "final answer") {
		t.Fatalf("expected assistant body in transcript, got %q", got)
	}
	if strings.Contains(got, "[user]") || strings.Contains(got, "[assistant]") {
		t.Fatalf("expected no bracketed role labels, got %q", got)
	}
}

func TestRenderTranscriptMessageAssistantWrapsToViewportWidth(t *testing.T) {
	m := App{
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{
			2: {{Kind: domain.PartKindText, Body: "this assistant line is intentionally longer than the viewport width"}},
		}},
		viewport: newTranscriptViewport(18, 6),
	}

	got := m.renderTranscriptMessage(domain.Message{
		ID:   2,
		Role: domain.MessageRoleAssistant,
	})

	lines := strings.Split(got, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected wrapped assistant lines, got %q", got)
	}
	for idx, line := range lines {
		if gotWidth := ui.TextWidth(line); gotWidth > m.viewport.Width {
			t.Fatalf("expected line width <= %d at line %d, got %d from %q", m.viewport.Width, idx, gotWidth, line)
		}
	}
}

func TestRenderTranscriptMessageAssistantPreservesPlainTextContent(t *testing.T) {
	m := App{
		palette: theme.Default().Palette,
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{
			2: {{Kind: domain.PartKindText, Body: "plain assistant text"}},
		}},
	}

	got := m.renderTranscriptMessage(domain.Message{
		ID:   2,
		Role: domain.MessageRoleAssistant,
	})

	if !strings.Contains(got, "plain assistant text") {
		t.Fatalf("expected assistant text to remain visible, got %q", got)
	}
}

func TestRefreshViewportUsesSingleNewlineBetweenBlocksWithHalfBlocks(t *testing.T) {
	cfg := testConfig(t)
	m := App{
		cfg: cfg,
		currentSnapshot: chatpkg.Snapshot{Messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleUser},
			{ID: 2, Role: domain.MessageRoleAssistant},
		}, Parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindText, Body: "hello"}},
			2: {{Kind: domain.PartKindText, Body: "reply"}},
		}},
		viewport: newTranscriptViewport(24, 8),
	}

	m.refreshViewport()
	got := m.viewport.View()
	if strings.Contains(got, "▀\n\nreply") {
		t.Fatalf("expected no extra blank line between user bubble and assistant reply, got %q", got)
	}
	if !strings.Contains(got, "▀\nreply") {
		t.Fatalf("expected single newline between user bubble and assistant reply, got %q", got)
	}
}

func TestRefreshViewportUsesBlankLineBetweenAssistantMessagesWithHalfBlocks(t *testing.T) {
	cfg := testConfig(t)
	m := App{
		cfg: cfg,
		currentSnapshot: chatpkg.Snapshot{Messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleAssistant},
			{ID: 2, Role: domain.MessageRoleAssistant},
		}, Parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindText, Body: "first reply"}},
			2: {{Kind: domain.PartKindText, Body: "second reply"}},
		}},
		viewport: newTranscriptViewport(24, 8),
	}

	m.refreshViewport()
	got := m.viewport.View()
	lines := strings.Split(got, "\n")
	secondLine := -1
	for i, line := range lines {
		if strings.Contains(line, "second reply") {
			secondLine = i
			break
		}
	}
	if secondLine < 2 {
		t.Fatalf("expected second assistant message after a spacer row, got %q", got)
	}
	if !strings.Contains(lines[secondLine-2], "first reply") {
		t.Fatalf("expected first assistant message two rows before second, got %q", got)
	}
	if strings.TrimSpace(lines[secondLine-1]) != "" {
		t.Fatalf("expected blank line between assistant messages in half-block mode, got %q", got)
	}
}

func TestRefreshViewportUsesBlankLineBetweenAssistantTextAndToolRunWithHalfBlocks(t *testing.T) {
	cfg := testConfig(t)
	m := App{
		cfg: cfg,
		currentSnapshot: chatpkg.Snapshot{Messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleAssistant},
			{ID: 2, Role: domain.MessageRoleTool},
		}, Parts: map[int64][]domain.Part{
			1: {{Kind: domain.PartKindText, Body: "plain assistant text"}},
			2: {{
				Kind: domain.PartKindToolOutput,
				Body: "first line\nsecond line",
				MetaJSON: mustMarshalMeta(t, map[string]string{
					"tool":         string(domain.ToolKindRead),
					"path":         "README.md",
					"preview":      "README.md",
					"tool_call_id": "call_1",
				}),
			}},
		}},
		viewport: newTranscriptViewport(60, 12),
	}

	m.refreshViewport()
	got := m.viewport.View()
	if !strings.Contains(got, "Read file") {
		t.Fatalf("expected grouped tool run card to render, got %q", got)
	}
	lines := strings.Split(got, "\n")
	var toolLine int = -1
	for i, line := range lines {
		if strings.Contains(line, "Read file") {
			toolLine = i
			break
		}
	}
	if toolLine < 2 {
		t.Fatalf("expected tool run to appear after assistant text and a blank spacer row, got %q", got)
	}
	if !strings.Contains(lines[toolLine-2], "plain assistant text") {
		t.Fatalf("expected assistant text two rows before tool run, got %q", got)
	}
	if strings.TrimSpace(lines[toolLine-1]) != "" {
		t.Fatalf("expected blank spacer row between assistant text and tool run, got %q", got)
	}
}

func TestRefreshViewportUsesBlankLineBetweenConsecutiveToolRunsWithHalfBlocks(t *testing.T) {
	cfg := testConfig(t)
	m := App{
		cfg: cfg,
		currentSnapshot: chatpkg.Snapshot{Messages: []domain.Message{
			{ID: 1, Role: domain.MessageRoleAssistant},
			{ID: 2, Role: domain.MessageRoleTool},
			{ID: 3, Role: domain.MessageRoleAssistant},
			{ID: 4, Role: domain.MessageRoleTool},
		}, Parts: map[int64][]domain.Part{
			1: {{
				Kind: domain.PartKindToolCall,
				MetaJSON: mustMarshalMeta(t, map[string]string{
					"tool":         string(domain.ToolKindRead),
					"path":         "README.md",
					"preview":      "README.md",
					"tool_call_id": "call_1",
				}),
			}},
			2: {{
				Kind: domain.PartKindToolOutput,
				Body: "first line",
				MetaJSON: mustMarshalMeta(t, map[string]string{
					"tool":         string(domain.ToolKindRead),
					"path":         "README.md",
					"preview":      "README.md",
					"tool_call_id": "call_1",
				}),
			}},
			3: {{
				Kind: domain.PartKindToolCall,
				MetaJSON: mustMarshalMeta(t, map[string]string{
					"tool":         string(domain.ToolKindRead),
					"path":         "go.mod",
					"preview":      "go.mod",
					"tool_call_id": "call_2",
				}),
			}},
			4: {{
				Kind: domain.PartKindToolOutput,
				Body: "second line",
				MetaJSON: mustMarshalMeta(t, map[string]string{
					"tool":         string(domain.ToolKindRead),
					"path":         "go.mod",
					"preview":      "go.mod",
					"tool_call_id": "call_2",
				}),
			}},
		}},
		viewport: newTranscriptViewport(60, 12),
	}

	m.refreshViewport()
	got := m.viewport.View()
	lines := strings.Split(got, "\n")
	firstTitleLine, secondTitleLine := -1, -1
	for i, line := range lines {
		switch {
		case firstTitleLine == -1 && strings.Contains(line, "Read file"):
			firstTitleLine = i
		case firstTitleLine != -1 && secondTitleLine == -1 && strings.Contains(line, "Read file"):
			secondTitleLine = i
		}
	}
	if firstTitleLine == -1 || secondTitleLine == -1 {
		t.Fatalf("expected both grouped tool runs to render, got %q", got)
	}
	if secondTitleLine <= firstTitleLine+1 {
		t.Fatalf("expected second tool run to appear after a blank spacer row, got %q", got)
	}
	if strings.TrimSpace(lines[secondTitleLine-1]) != "" {
		t.Fatalf("expected blank spacer row between consecutive tool runs, got %q", got)
	}
}

func TestTranscriptBlocksKeepsRepeatedFailedToolRunsSeparate(t *testing.T) {
	m := App{
		currentSession:  domain.Session{ID: 1},
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
	}
	m.currentSnapshot.Messages = []domain.Message{
		{ID: 1, Role: domain.MessageRoleAssistant, Summary: "tool:bash"},
		{ID: 2, Role: domain.MessageRoleTool, Summary: "approval:bash"},
		{ID: 3, Role: domain.MessageRoleTool, Summary: "approval:bash:approved"},
		{ID: 4, Role: domain.MessageRoleTool, Summary: "bash"},
		{ID: 5, Role: domain.MessageRoleUser, Summary: "continue"},
		{ID: 6, Role: domain.MessageRoleAssistant, Summary: "tool:bash"},
		{ID: 7, Role: domain.MessageRoleTool, Summary: "approval:bash"},
		{ID: 8, Role: domain.MessageRoleTool, Summary: "approval:bash:approved"},
		{ID: 9, Role: domain.MessageRoleTool, Summary: "bash"},
	}
	m.currentSnapshot.Parts[1] = []domain.Part{{
		Kind: domain.PartKindToolCall,
		MetaJSON: mustMarshalMeta(t, map[string]string{
			"tool":         string(domain.ToolKindBash),
			"command":      `go build -v . 2>&1; echo "EXIT_CODE=$?"`,
			"tool_call_id": "call_1",
		}),
	}}
	m.currentSnapshot.Parts[2] = []domain.Part{{
		Kind:     domain.PartKindApprovalRequest,
		Body:     `Approval required for bash: go build -v . 2>&1; echo "EXIT_CODE=$?"`,
		MetaJSON: mustMarshalMeta(t, map[string]string{"approval_id": "1", "command": `go build -v . 2>&1; echo "EXIT_CODE=$?"`, "status": "pending", "tool": string(domain.ToolKindBash), "tool_call_id": "call_1"}),
	}}
	m.currentSnapshot.Parts[3] = []domain.Part{{
		Kind:     domain.PartKindSystemNotice,
		Body:     `Approval 1 approved for bash: go build -v . 2>&1; echo "EXIT_CODE=$?"`,
		MetaJSON: mustMarshalMeta(t, map[string]string{"approval_id": "1", "command": `go build -v . 2>&1; echo "EXIT_CODE=$?"`, "status": "approved", "tool": string(domain.ToolKindBash), "tool_call_id": "call_1"}),
	}}
	m.currentSnapshot.Parts[4] = []domain.Part{{
		Kind:     domain.PartKindToolOutput,
		Body:     "bash failed: timeout_ms must be a positive integer",
		MetaJSON: mustMarshalMeta(t, map[string]string{"tool": string(domain.ToolKindBash), "command": `go build -v . 2>&1; echo "EXIT_CODE=$?"`, "tool_call_id": "call_1"}),
	}}
	m.currentSnapshot.Parts[5] = []domain.Part{{Kind: domain.PartKindText, Body: "continue"}}
	m.currentSnapshot.Parts[6] = []domain.Part{{
		Kind: domain.PartKindToolCall,
		MetaJSON: mustMarshalMeta(t, map[string]string{
			"tool":         string(domain.ToolKindBash),
			"command":      `go build -v . 2>&1; echo "EXIT_CODE=$?"`,
			"tool_call_id": "call_2",
		}),
	}}
	m.currentSnapshot.Parts[7] = []domain.Part{{
		Kind:     domain.PartKindApprovalRequest,
		Body:     `Approval required for bash: go build -v . 2>&1; echo "EXIT_CODE=$?"`,
		MetaJSON: mustMarshalMeta(t, map[string]string{"approval_id": "2", "command": `go build -v . 2>&1; echo "EXIT_CODE=$?"`, "status": "pending", "tool": string(domain.ToolKindBash), "tool_call_id": "call_2"}),
	}}
	m.currentSnapshot.Parts[8] = []domain.Part{{
		Kind:     domain.PartKindSystemNotice,
		Body:     `Approval 2 approved for bash: go build -v . 2>&1; echo "EXIT_CODE=$?"`,
		MetaJSON: mustMarshalMeta(t, map[string]string{"approval_id": "2", "command": `go build -v . 2>&1; echo "EXIT_CODE=$?"`, "status": "approved", "tool": string(domain.ToolKindBash), "tool_call_id": "call_2"}),
	}}
	m.currentSnapshot.Parts[9] = []domain.Part{{
		Kind:     domain.PartKindToolOutput,
		Body:     "bash failed: timeout_ms must be a positive integer",
		MetaJSON: mustMarshalMeta(t, map[string]string{"tool": string(domain.ToolKindBash), "command": `go build -v . 2>&1; echo "EXIT_CODE=$?"`, "tool_call_id": "call_2"}),
	}}

	blocks := m.transcriptBlocks()
	runCount := 0
	lastKind := transcriptBlockKind(-1)
	for _, block := range blocks {
		if block.Kind == transcriptBlockToolRun {
			runCount++
		}
		lastKind = block.Kind
	}
	if runCount != 2 {
		t.Fatalf("expected two separate tool runs, got %d from %#v", runCount, blocks)
	}
	if lastKind != transcriptBlockToolRun {
		t.Fatalf("expected tail block to remain the second tool run, got %#v", blocks[len(blocks)-1])
	}
}

func TestTranscriptBlocksIncludeLiveExecRuns(t *testing.T) {
	mgr := execruntime.NewManager()
	snap, err := mgr.Start(context.Background(), execruntime.StartRequest{
		SessionID: 1,
		ChatID:    2,
		Command:   "sleep 1",
	})
	if err != nil {
		t.Fatalf("start exec session: %v", err)
	}

	m := App{
		exec:            mgr,
		currentSession:  domain.Session{ID: 1},
		currentChat:     domain.Chat{ID: 2, SessionID: 1},
		currentSnapshot: chatpkg.Snapshot{Parts: map[int64][]domain.Part{}},
	}
	blocks := m.transcriptBlocks()
	if len(blocks) != 1 || blocks[0].Kind != transcriptBlockToolRun {
		t.Fatalf("expected one live tool run block, got %#v", blocks)
	}
	run := blocks[0].ToolRun
	if run.ID != snap.ProcessID || run.Tool != domain.ToolKindExecCommand {
		t.Fatalf("unexpected live exec tool run: %#v", run)
	}
	if run.Status != ui.ToolRunStatusRunning {
		t.Fatalf("expected running live exec status, got %#v", run)
	}
	if !strings.Contains(run.Title, "Running command sleep 1") {
		t.Fatalf("expected running live exec title, got %#v", run)
	}
	if run.ProcessID != snap.ProcessID {
		t.Fatalf("expected process id on live exec run, got %#v", run)
	}
	if !strings.Contains(run.Subtitle, snap.ProcessID) {
		t.Fatalf("expected live exec subtitle to include process id, got %#v", run)
	}
}

func TestToolRunOutputUsesPastTenseForCompletedExec(t *testing.T) {
	exitCode := 0
	msg := domain.Message{ID: 1, Role: domain.MessageRoleTool, Summary: "exec_command"}
	part := domain.Part{
		Kind: domain.PartKindToolOutput,
		Body: "done",
		MetaJSON: mustMarshalMeta(t, tools.MetaWithStoredResult(map[string]string{
			"tool":       string(domain.ToolKindExecCommand),
			"process_id": "exec_42",
			"command":    "npm test",
			"state":      "completed",
			"exit_code":  "0",
		}, domain.PartKindToolOutput, domain.ToolKindExecCommand, tools.StoredResultStatusOK, tools.ExecStoredResult{
			ProcessID: "exec_42",
			Command:   "npm test",
			State:     "completed",
			ExitCode:  &exitCode,
			Output:    "done",
		})),
	}

	run := toolRunOutput(part, nil, msg)
	if run.Status != ui.ToolRunStatusCompleted {
		t.Fatalf("expected completed exec status, got %#v", run)
	}
	if run.Title != "Ran command npm test" {
		t.Fatalf("expected completed exec title, got %#v", run)
	}
}

func TestRefreshExecSubscriptionCmdReceivesExecEventsForCurrentChat(t *testing.T) {
	mgr := execruntime.NewManager()
	m := App{
		exec:           mgr,
		currentSession: domain.Session{ID: 1},
		currentChat:    domain.Chat{ID: 2, SessionID: 1},
	}

	cmd := m.refreshExecSubscriptionCmd()
	if cmd == nil {
		t.Fatal("expected subscription command")
	}

	if _, err := mgr.Start(context.Background(), execruntime.StartRequest{
		SessionID: 1,
		ChatID:    2,
		Command:   "printf hi",
	}); err != nil {
		t.Fatalf("start exec session: %v", err)
	}

	raw := cmd()
	msg, ok := raw.(execEventMsg)
	if !ok {
		t.Fatalf("expected execEventMsg, got %T", raw)
	}
	if !msg.ok {
		t.Fatal("expected live exec event")
	}
	if msg.chatID != 2 {
		t.Fatalf("expected chat 2 event, got %d", msg.chatID)
	}
	if msg.seq != m.execSubscriptionSeq {
		t.Fatalf("expected seq %d, got %d", m.execSubscriptionSeq, msg.seq)
	}
}

func TestExecEventMsgIgnoresStaleSubscription(t *testing.T) {
	mgr := execruntime.NewManager()
	m := App{
		exec:           mgr,
		currentSession: domain.Session{ID: 1},
		currentChat:    domain.Chat{ID: 2, SessionID: 1},
	}

	oldCmd := m.refreshExecSubscriptionCmd()
	if oldCmd == nil {
		t.Fatal("expected initial subscription command")
	}
	oldSeq := m.execSubscriptionSeq

	m.currentChat = domain.Chat{ID: 3, SessionID: 1}
	if cmd := m.refreshExecSubscriptionCmd(); cmd == nil {
		t.Fatal("expected refreshed subscription command")
	}
	if m.execSubscriptionSeq == oldSeq {
		t.Fatal("expected subscription sequence to advance")
	}

	raw := oldCmd()
	msg, ok := raw.(execEventMsg)
	if !ok {
		t.Fatalf("expected execEventMsg, got %T", raw)
	}
	if msg.ok {
		t.Fatal("expected closed stale subscription")
	}

	nextModel, cmd := m.Update(msg)
	updated, ok := nextModel.(App)
	if !ok {
		t.Fatalf("expected Model, got %T", nextModel)
	}
	if cmd != nil {
		t.Fatal("expected stale exec event to be ignored without scheduling follow-up work")
	}
	if updated.execSubscriptionSeq != m.execSubscriptionSeq {
		t.Fatalf("expected seq %d to remain active, got %d", m.execSubscriptionSeq, updated.execSubscriptionSeq)
	}
}

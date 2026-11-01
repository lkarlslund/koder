package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/app"
	"github.com/lkarlslund/koder/internal/domain"
)

func TestWorktreeFileBrowserAndComposerUseSelectedCheckout(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-b", "parent")
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("parent"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "base")
	ctrl := newTestControllerWithWorkdir(t, repo)
	state := selectedTestState(t, ctrl)
	selection := app.Selection{SessionID: state.Session.ID, ChatID: state.ActiveChatID}
	child, err := ctrl.CreateChatForSelection(ctx, selection, domain.ChatCreateSpec{Isolation: "worktree"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child.Worktree.Path, "file.txt"), []byte("child"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := &Server{controller: ctrl}
	read := func(query string) fileContentResponse {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/files/read?path=file.txt"+query, nil)
		response := httptest.NewRecorder()
		server.handleSessionFileRead(response, req, state.Session.ID)
		if response.Code != http.StatusOK {
			t.Fatalf("read: %d %s", response.Code, response.Body.String())
		}
		var content fileContentResponse
		if err := json.Unmarshal(response.Body.Bytes(), &content); err != nil {
			t.Fatal(err)
		}
		return content
	}
	if got := read("&chat=" + string(child.ID)); got.Content != "child" || got.ProjectRoot != child.Worktree.Path {
		t.Fatalf("child read=%#v", got)
	}
	if got := read(""); got.Content != "parent" {
		t.Fatalf("parent read=%#v", got)
	}
	req := httptest.NewRequest(http.MethodPost, "/files/upload?path=child-only.txt&chat="+string(child.ID), strings.NewReader("child upload"))
	req.Header.Set("X-Koder-File-Action", "1")
	response := httptest.NewRecorder()
	server.handleSessionFileMutation(response, req, state.Session.ID, "upload")
	if response.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(filepath.Join(repo, "child-only.txt")); !os.IsNotExist(err) {
		t.Fatal("file browser wrote to parent")
	}
	selection.ChatID = child.ID
	completions, err := ctrl.CompleteComposerForSelection(ctx, selection, "@child-only", len("@child-only"))
	if err != nil {
		t.Fatal(err)
	}
	if len(completions.Items) == 0 || !strings.Contains(completions.Items[0].InsertText, "child-only") {
		t.Fatalf("child completion=%#v", completions)
	}
	diff, err := ctrl.WorkspaceDiffForSelection(ctx, selection)
	if err != nil || diff.ProjectRoot != child.Worktree.Path || len(diff.Files) == 0 {
		t.Fatalf("child diff=%#v err=%v", diff, err)
	}
}

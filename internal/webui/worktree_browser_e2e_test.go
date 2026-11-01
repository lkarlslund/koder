package webui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

func TestWorktreeChatCreationAndWorkspaceView(t *testing.T) {
	chromium := chromiumForTest(t)
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-b", "parent")
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "base")
	ctrl := newTestControllerWithWorkdir(t, repo)
	state := selectedTestState(t, ctrl)
	serverCtx, stopServer := context.WithCancel(context.Background())
	server := startBrowserTestServer(t, serverCtx, ctrl)
	t.Cleanup(func() { stopServer(); _ = server.server.Close() })
	options := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(chromium), chromedp.Flag("no-sandbox", true), chromedp.Flag("disable-dev-shm-usage", true))
	allocator, stopAllocator := chromedp.NewExecAllocator(context.Background(), options...)
	defer stopAllocator()
	browser, stopBrowser := chromedp.NewContext(allocator)
	defer stopBrowser()
	ctx, cancel := context.WithTimeout(browser, 45*time.Second)
	defer cancel()
	var result struct {
		Branch, Path, Root, ManagerBranch string
		Filtered                          bool
	}
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL()+"/s/"+string(state.Session.ID)+"/c/"+string(state.ActiveChatID)),
		chromedp.Poll(`document.documentElement._x_dataStack?.[0]?.connected === true`, nil),
		chromedp.Evaluate(`document.documentElement._x_dataStack[0].openChatCreator()`, nil),
		chromedp.Poll(`document.documentElement._x_dataStack[0].chatCreator.loading === false`, nil),
		chromedp.SetValue("#new-chat-isolation", "worktree", chromedp.ByQuery),
		chromedp.Evaluate(`(() => { const app=document.documentElement._x_dataStack[0]; document.querySelector('#new-chat-isolation').dispatchEvent(new Event('change',{bubbles:true})); app.chatCreator.draft.title='Isolated worker'; app.createChatFromDraft(); })()`, nil),
		chromedp.Poll(`!!document.documentElement._x_dataStack[0].activeWorktree()`, nil),
		chromedp.Evaluate(`document.documentElement._x_dataStack[0].openWorktrees()`, nil),
		chromedp.Poll(`document.documentElement._x_dataStack[0].worktreeManager.busy === false`, nil),
		chromedp.Evaluate(`(() => {
   const app=document.documentElement._x_dataStack[0], wt=app.activeWorktree();
   const branch=app.gitStatus().branch;
   app.applyWorkspaceDelta({workspace_status:{project_root:app.state.session.ProjectRoot,branch:'WRONG-PARENT'}});
   return {Branch:branch,Path:wt.path,Root:app.state.project_root,ManagerBranch:app.worktreeManager.items[0]?.worktree.branch || '',Filtered:app.gitStatus().branch===branch};
  })()`, &result),
	)
	if err != nil {
		t.Fatalf("worktree UI: %v", err)
	}
	if !strings.HasPrefix(result.Branch, "koder/") || result.ManagerBranch != result.Branch || result.Path != result.Root || !result.Filtered {
		t.Fatalf("worktree view=%#v", result)
	}
	if _, err := os.Stat(filepath.Join(result.Path, "file.txt")); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", repo, "branch", "--show-current").Output()
	if err != nil || strings.TrimSpace(string(out)) != "parent" {
		t.Fatal("parent branch changed")
	}
}

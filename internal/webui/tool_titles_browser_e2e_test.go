package webui

import (
	"context"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// Each tool call's row says in plain words what the call did, and to what,
// instead of the tool's internal name. IDs resolve to the names the user
// knows: a process to its description, a chat to its title, a cron job to
// its description.
func TestToolTitlesSayWhatTheCallDid(t *testing.T) {
	chromium := chromiumForTest(t)
	ctrl, _, _ := newTestControllerAndEngine(t, t.TempDir())
	state := selectedTestState(t, ctrl)
	serverCtx, stopServer := context.WithCancel(context.Background())
	server := startBrowserTestServer(t, serverCtx, ctrl)
	t.Cleanup(func() {
		stopServer()
		_ = server.server.Close()
	})
	allocatorCtx, stopAllocator := chromedp.NewExecAllocator(context.Background(), append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chromium),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)...)
	t.Cleanup(stopAllocator)
	browserCtx, stopBrowser := chromedp.NewContext(allocatorCtx)
	t.Cleanup(stopBrowser)
	browserCtx, cancel := context.WithTimeout(browserCtx, 60*time.Second)
	t.Cleanup(cancel)

	cases := []struct{ call, want string }{
		{`{tool: 'browser_interact', args: {action: 'click', role: 'button', target: 'Only sold'}}`, "Click “Only sold”"},
		{`{tool: 'browser_interact', args: {action: 'select', target: 'Sold items', value: 'only'}}`, "Choose “only” in “Sold items”"},
		{`{tool: 'browser_navigation', args: {action: 'goto', url: 'https://login.e-conomic.com/'}}`, "Go to login.e-conomic.com"},
		{`{tool: 'browser_page', args: {action: 'snapshot'}}`, "Read page"},
		{`{tool: 'browser_evaluate', args: {expression: '1'}}`, "Run script in page"},
		{`{tool: 'browser_capture', args: {action: 'screenshot'}}`, "Take screenshot"},
		{`{tool: 'exec_session', args: {action: 'wait', process_id: 'exec_7'}}`, "Wait for \u201cServe the site\u201d"},
		{`{tool: 'exec_session', args: {action: 'terminate', process_id: 'exec_9'}, result: {data: {process_id: 'exec_9', description: 'Download the extract'}}}`, "Stop \u201cDownload the extract\u201d"},
		{`{tool: 'exec_session', args: {action: 'list'}}`, "List running commands"},
		{`{tool: 'cron', args: {action: 'create', schedule: '7 9 * * *', description: 'Check the queue every morning', prompt: 'Long prompt'}}`, "Schedule “Check the queue every morning” (7 9 * * *)"},
		{`{tool: 'cron', args: {action: 'delete', id: 'ab12cd34'}}`, "Cancel scheduled job “Watch the service”"},
		{`{tool: 'cron', args: {action: 'list'}}`, "List scheduled jobs"},
		{`{tool: 'memory', args: {action: 'get', name: 'calendar-time-tracking'}}`, "Recall calendar-time-tracking"},
		{`{tool: 'memory', args: {action: 'list'}}`, "List memories"},
		{`{tool: 'milestones', args: {action: 'create', title: 'Treat removed listings as sold'}}`, "Add milestone “Treat removed listings as sold”"},
		{`{tool: 'tasks', args: {action: 'create', items: '[{"content":"a"},{"content":"b"}]'}}`, "Add 2 tasks"},
		{`{tool: 'tasks', args: {action: 'update', task_key: 'M004T002', status: 'completed'}}`, "Mark M004T002 completed"},
		{`{tool: 'chats', args: {action: 'steer', chat_id: CHAT, message: 'stop'}}`, "Steer “Main”"},
		{`{tool: 'request_user_input', args: {questions: '[{"question":"Which company?"}]'}}`, "Ask you: Which company?"},
		{`{tool: 'mcp', args: {server: 'exchange', tool: 'exchange_events', arguments_raw: '{"action":"update"}'}}`, "Update exchange events"},
		{`{tool: 'web_search', args: {query: 'dmr udtræk'}}`, "Search web for “dmr udtræk”"},
		{`{tool: 'web_fetch', args: {url: 'https://bil-portalen.dk/biler'}}`, "Fetch bil-portalen.dk/biler"},
		{`{tool: 'view_pdf', args: {path: 'tmp/invoice.pdf', page: '1'}}`, "View invoice.pdf, page 1"},
		{`{tool: 'skill', args: {name: 'danish-public-data'}}`, "Load skill danish-public-data"},
		{`{tool: 'code_search', args: {action: 'workspace_symbol', query: 'Estimate'}}`, "Find symbol “Estimate”"},
		{`{tool: 'some_new_tool', args: {action: 'do_thing'}}`, "Do thing Some new tool"},
	}
	calls := "["
	for _, c := range cases {
		calls += c.call + ","
	}
	calls += "]"
	var got []string
	if err := chromedp.Run(browserCtx,
		chromedp.EmulateViewport(1200, 900),
		chromedp.Navigate(server.URL()+"/s/"+string(state.Session.ID)+"/c/"+string(state.ActiveChatID)),
		chromedp.WaitReady(`.transcript`, chromedp.ByQuery),
		chromedp.Poll(`document.documentElement._x_dataStack?.[0]?.connected === true && String(document.documentElement._x_dataStack[0].activeChatID() || '').length > 10`, nil),
		chromedp.Evaluate(`(() => {
			const app = document.documentElement._x_dataStack[0];
			const CHAT = String(app.activeChatID());
			const history = [{id: 'h', kind: 'assistant', content: {tools: [
				{tool: 'exec_command', args: {cmd: 'serve', description: 'Serve the site'}, result: {data: {process_id: 'exec_7', description: 'Serve the site'}}},
				{tool: 'cron', args: {action: 'create', schedule: '*/5 * * * *', description: 'Watch the service', prompt: 'x'}, result: {text: 'Scheduled recurring job ab12cd34 (*/5 * * * *). Next run: soon.'}},
			]}}];
			app.timeline = () => history;
			return (`+calls+`).map(call => app.toolTitle(call));
		})()`, &got),
	); err != nil {
		t.Fatalf("titles: %v", err)
	}
	var badges []string
	if err := chromedp.Run(browserCtx, chromedp.Evaluate(`(() => {
		const app = document.documentElement._x_dataStack[0];
		return [
			{tool: 'exec_session', status: 'done', args: {action: 'terminate', process_id: 'exec_9'}, result: {data: {exit_code: -1}}},
			{tool: 'exec_command', status: 'done', args: {cmd: 'false', description: 'Fail'}, result: {data: {exit_code: 1}}},
		].map(call => app.toolLineBadge(call));
	})()`, &badges)); err != nil {
		t.Fatal(err)
	}
	if len(badges) != 2 || badges[0] != "" || badges[1] != "exit 1" {
		t.Errorf("badges = %q; a stopped command needs none, a failed one shows its exit code", badges)
	}
	for i, c := range cases {
		if i >= len(got) || got[i] != c.want {
			g := ""
			if i < len(got) {
				g = got[i]
			}
			t.Errorf("%s\n  title = %q\n  want    %q", c.call, g, c.want)
		}
	}
}

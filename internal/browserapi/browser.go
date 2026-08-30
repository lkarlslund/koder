package browserapi

import (
	"context"
	"time"

	"github.com/lkarlslund/koder/internal/id"
)

type Chat struct {
	SessionID id.ID
	ChatID    id.ID
}

type Status struct {
	State      string `json:"state"`
	Executable string `json:"executable,omitempty"`
	Version    string `json:"version,omitempty"`
	Error      string `json:"error,omitempty"`
	OwnedTabs  int    `json:"owned_tabs"`
}

type Tab struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Owned    bool   `json:"owned"`
	Unowned  bool   `json:"unowned"`
	Selected bool   `json:"selected"`
}

type Snapshot struct {
	TabID     string `json:"tab_id"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
}

type ElementState struct {
	Found   bool   `json:"found"`
	Name    string `json:"name,omitempty"`
	Role    string `json:"role,omitempty"`
	Value   string `json:"value,omitempty"`
	Focused bool   `json:"focused,omitempty"`
	Enabled bool   `json:"enabled"`
	Checked *bool  `json:"checked,omitempty"`
}

type InteractionOutcome struct {
	Action          string        `json:"action"`
	Locator         Locator       `json:"locator,omitempty"`
	Tab             Tab           `json:"page"`
	LoadState       string        `json:"load_state,omitempty"`
	Changed         bool          `json:"page_changed"`
	Changes         []string      `json:"changes,omitempty"`
	TargetState     *ElementState `json:"target_state,omitempty"`
	Observation     string        `json:"observation,omitempty"`
	PendingRequests int           `json:"pending_requests,omitempty"`
}

type WaitOptions struct {
	Condition string
	Text      string
	URL       string
	State     string
	Locator   Locator
	Timeout   time.Duration
	Idle      time.Duration
}

type WaitOutcome struct {
	Condition       string        `json:"condition"`
	State           string        `json:"state,omitempty"`
	Matched         bool          `json:"matched"`
	ElapsedMS       int64         `json:"elapsed_ms"`
	Tab             Tab           `json:"page"`
	LoadState       string        `json:"load_state,omitempty"`
	TargetState     *ElementState `json:"target_state,omitempty"`
	Observation     string        `json:"observation,omitempty"`
	PendingRequests int           `json:"pending_requests,omitempty"`
}

type Locator struct {
	Target     string `json:"target,omitempty"`
	Role       string `json:"role,omitempty"`
	Within     string `json:"within,omitempty"`
	Exact      bool   `json:"exact"`
	Occurrence int    `json:"occurrence,omitempty"`
	Selector   string `json:"selector,omitempty"`
}

func (l Locator) Empty() bool {
	return l.Target == "" && l.Selector == ""
}

type Binary struct {
	Name string
	MIME string
	Data []byte
}

type RequestRecord struct {
	ID       string            `json:"id"`
	Method   string            `json:"method"`
	URL      string            `json:"url"`
	Status   int64             `json:"status,omitempty"`
	MIME     string            `json:"mime,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Finished bool              `json:"finished"`
}

type ConsoleRecord struct {
	Level string    `json:"level"`
	Text  string    `json:"text"`
	Time  time.Time `json:"time"`
}

type DownloadRecord struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	State    string `json:"state"`
	Received int64  `json:"received_bytes"`
	Total    int64  `json:"total_bytes,omitempty"`
}

type Service interface {
	Status(context.Context, Chat) Status
	Start(context.Context) error
	Stop(context.Context) error
	Restart(context.Context) error
	ResetProfile(context.Context) error
	Show(context.Context, Chat) error
	Tabs(context.Context, Chat) ([]Tab, error)
	NewTab(context.Context, Chat, string) (Tab, error)
	ClaimTab(context.Context, Chat, string) (Tab, error)
	SelectTab(context.Context, Chat, string) (Tab, error)
	CloseTab(context.Context, Chat, string) error
	Navigate(context.Context, Chat, string, string) (Tab, error)
	History(context.Context, Chat, string) (Tab, error)
	Snapshot(context.Context, Chat, string, int, int) (Snapshot, error)
	Find(context.Context, Chat, string, string, int) (Snapshot, error)
	InteractOutcome(context.Context, Chat, string, Locator, string) (InteractionOutcome, error)
	Wait(context.Context, Chat, WaitOptions) (WaitOutcome, error)
	Drag(context.Context, Chat, Locator, Locator) error
	Scroll(context.Context, Chat, Locator, int, int) error
	Upload(context.Context, Chat, Locator, []string) error
	Evaluate(context.Context, Chat, string) (string, error)
	Screenshot(context.Context, Chat, Locator, bool, string, int) (Binary, error)
	Image(context.Context, Chat, Locator) (Binary, error)
	PDF(context.Context, Chat) (Binary, error)
	Console(context.Context, Chat, string, int) ([]ConsoleRecord, error)
	Requests(context.Context, Chat, int) ([]RequestRecord, error)
	ResponseBody(context.Context, Chat, string) (Binary, error)
	Downloads(context.Context, Chat) ([]DownloadRecord, error)
	Download(context.Context, Chat, string) (Binary, error)
	CleanupChat(context.Context, id.ID)
	CleanupSession(context.Context, id.ID)
}

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
	TabID      string `json:"tab_id"`
	Generation uint64 `json:"generation"`
	Text       string `json:"text"`
	Truncated  bool   `json:"truncated"`
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
	Interact(context.Context, Chat, string, string, string) error
	Evaluate(context.Context, Chat, string) (string, error)
	Screenshot(context.Context, Chat, string, bool, string, int) (Binary, error)
	PDF(context.Context, Chat) (Binary, error)
	Console(context.Context, Chat, string, int) ([]ConsoleRecord, error)
	Requests(context.Context, Chat, int) ([]RequestRecord, error)
	ResponseBody(context.Context, Chat, string) (Binary, error)
	CleanupChat(context.Context, id.ID)
	CleanupSession(context.Context, id.ID)
}

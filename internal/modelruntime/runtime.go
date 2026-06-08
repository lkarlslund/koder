package modelruntime

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/agents"
	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/debugsrv"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/mcp"
	"github.com/lkarlslund/koder/internal/provider"
	sessionpkg "github.com/lkarlslund/koder/internal/session"
	"github.com/lkarlslund/koder/internal/settings"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/toolruntime"
)

type SessionSource interface {
	LoadSession(context.Context, id.ID) (*sessionpkg.Session, error)
}

type Config struct {
	Config   config.Config
	Store    *store.Store
	Debug    *debugsrv.Recorder
	Files    *attachment.Manager
	Caps     *provider.CapabilityStore
	Agents   *agents.Manager
	Settings *settings.Store
	Tools    *toolruntime.Runtime
	MCP      *mcp.Manager
	Sessions SessionSource
}

type Runtime struct {
	cfg         config.Config
	store       *store.Store
	debug       *debugsrv.Recorder
	files       *attachment.Manager
	caps        *provider.CapabilityStore
	agents      *agents.Manager
	settings    *settings.Store
	tools       *toolruntime.Runtime
	mcp         *mcp.Manager
	sessions    SessionSource
	caveman     *cavemanService
	cavemanMu   sync.Mutex
	cavemanJobs map[id.ID]cavemanJob
	retryPause  func(context.Context, time.Duration, func(time.Duration)) error
	envMu       sync.Mutex
	envCache    map[string]string
}

const (
	maxRateLimitRetries       = 3
	maxTransientChatRetries   = 3
	defaultRateLimitRetryWait = 5 * time.Second
	defaultTransientRetryWait = 250 * time.Millisecond
	cavemanThinkingMaxBytes   = 4 * 1024
	cavemanThinkingMaxTokens  = 256
)

func New(cfg Config) *Runtime {
	files := cfg.Files
	if files == nil {
		files = attachment.NewManager(cfg.Config.StateDir())
	}
	caps := cfg.Caps
	if caps == nil {
		caps = provider.NewCapabilityStore(cfg.Config.StateDir())
	}
	agentManager := cfg.Agents
	if agentManager == nil {
		agentManager = agents.NewManager(cfg.Config.StateDir(), filepath.Join(filepath.Dir(cfg.Config.Path()), "AGENTS.md"))
	}
	settingsStore := cfg.Settings
	if settingsStore == nil {
		settingsStore = settings.New(cfg.Config)
	}
	return &Runtime{
		cfg:         cfg.Config,
		store:       cfg.Store,
		debug:       cfg.Debug,
		files:       files,
		caps:        caps,
		agents:      agentManager,
		settings:    settingsStore,
		tools:       cfg.Tools,
		mcp:         cfg.MCP,
		sessions:    cfg.Sessions,
		caveman:     newCavemanService(cfg.Config.Thinking.CavemanParallelism),
		cavemanJobs: map[id.ID]cavemanJob{},
		retryPause:  waitForRetry,
	}
}

func (r *Runtime) UpdateConfig(cfg config.Config) {
	if r == nil {
		return
	}
	r.cfg = cfg
	if r.settings != nil {
		r.settings.Update(cfg)
	} else {
		r.settings = settings.New(cfg)
	}
	r.files = attachment.NewManager(cfg.StateDir())
	r.caps = provider.NewCapabilityStore(cfg.StateDir())
	r.agents = agents.NewManager(cfg.StateDir(), filepath.Join(filepath.Dir(cfg.Path()), "AGENTS.md"))
	r.caveman = newCavemanService(cfg.Thinking.CavemanParallelism)
}

func (r *Runtime) SetToolsRuntime(runtime *toolruntime.Runtime) {
	if r == nil {
		return
	}
	r.tools = runtime
}

func (r *Runtime) SetMCP(manager *mcp.Manager) {
	if r == nil {
		return
	}
	r.mcp = manager
}

func (r *Runtime) SetSessionSource(source SessionSource) {
	if r == nil {
		return
	}
	r.sessions = source
}

func (r *Runtime) SetRetryPause(fn func(context.Context, time.Duration, func(time.Duration)) error) {
	if r == nil {
		return
	}
	if fn == nil {
		r.retryPause = waitForRetry
		return
	}
	r.retryPause = fn
}

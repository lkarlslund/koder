package modelruntime

import (
	"path/filepath"
	"sync"

	"github.com/lkarlslund/koder/internal/agents"
	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/debugsrv"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/settings"
	"github.com/lkarlslund/koder/internal/store"
)

type Config struct {
	Config   config.Config
	Store    *store.Store
	Debug    *debugsrv.Recorder
	Files    *attachment.Manager
	Caps     *provider.CapabilityStore
	Agents   *agents.Manager
	Settings *settings.Store
}

type Runtime struct {
	cfg      config.Config
	store    *store.Store
	debug    *debugsrv.Recorder
	files    *attachment.Manager
	caps     *provider.CapabilityStore
	agents   *agents.Manager
	settings *settings.Store
	envMu    sync.Mutex
	envCache map[string]string
}

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
		cfg:      cfg.Config,
		store:    cfg.Store,
		debug:    cfg.Debug,
		files:    files,
		caps:     caps,
		agents:   agentManager,
		settings: settingsStore,
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
}

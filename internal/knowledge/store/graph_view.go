package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
)

const (
	MaxGraphViewHiddenNodes = 1000
	MaxGraphViewHiddenEdges = 2000
	MaxGraphViewPinnedNodes = 500
	MaxGraphViewFrontier    = 50
)

type GraphViewBrowserState struct {
	Query      string `json:"query,omitempty"`
	Kind       string `json:"kind,omitempty"`
	ScopeKind  string `json:"scope_kind,omitempty"`
	State      string `json:"state,omitempty"`
	Tag        string `json:"tag,omitempty"`
	ObjectKind string `json:"object_kind,omitempty"`
	ID         string `json:"id,omitempty"`
}

type GraphViewPin struct {
	Key string  `json:"key"`
	X   float64 `json:"x"`
	Y   float64 `json:"y"`
}

type GraphViewExpansion struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	Direction string `json:"direction"`
}

type GraphViewState struct {
	Browser     GraphViewBrowserState `json:"browser"`
	MobilePane  string                `json:"mobile_pane,omitempty"`
	Root        *knowledge.ObjectRef  `json:"root,omitempty"`
	HiddenNodes []string              `json:"hidden_nodes,omitempty"`
	HiddenEdges []string              `json:"hidden_edges,omitempty"`
	PinnedNodes []GraphViewPin        `json:"pinned_nodes,omitempty"`
	Frontier    []GraphViewExpansion  `json:"frontier,omitempty"`
	Layout      string                `json:"layout,omitempty"`
}

// SavedGraphView is user-owned explorer state. It is deliberately not canonical
// Knowledge and never participates in chunk/entry revisions or search indexes.
type SavedGraphView struct {
	ID        string          `json:"id"`
	Owner     knowledge.Actor `json:"owner"`
	Name      string          `json:"name"`
	State     GraphViewState  `json:"state"`
	Revision  uint64          `json:"revision"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

func GraphViewOwnerKey(owner knowledge.Actor) string {
	return owner.Kind.String() + ":" + strings.TrimSpace(owner.ID)
}

func (view SavedGraphView) Validate() error {
	if strings.TrimSpace(view.ID) == "" || len(view.ID) > 160 || strings.ContainsAny(view.ID, "/\\\x00") {
		return fmt.Errorf("invalid graph view ID")
	}
	if err := view.Owner.Validate(); err != nil {
		return fmt.Errorf("invalid graph view owner: %w", err)
	}
	name := strings.TrimSpace(view.Name)
	if name == "" || len(name) > 120 {
		return fmt.Errorf("invalid graph view name")
	}
	if view.Revision == 0 || view.CreatedAt.IsZero() || view.UpdatedAt.IsZero() || view.UpdatedAt.Before(view.CreatedAt) {
		return fmt.Errorf("invalid graph view revision or timestamps")
	}
	if len(view.State.HiddenNodes) > MaxGraphViewHiddenNodes || len(view.State.HiddenEdges) > MaxGraphViewHiddenEdges ||
		len(view.State.PinnedNodes) > MaxGraphViewPinnedNodes || len(view.State.Frontier) > MaxGraphViewFrontier {
		return fmt.Errorf("graph view state exceeds limits")
	}
	return nil
}

// GraphViewStore is an optional persistence extension. Saved views are kept out of
// Store so canonical Knowledge backends and test doubles do not gain UI concerns.
type GraphViewStore interface {
	ListGraphViews(context.Context, knowledge.Actor) ([]SavedGraphView, error)
	GraphView(context.Context, knowledge.Actor, string) (SavedGraphView, error)
	PutGraphView(context.Context, SavedGraphView, uint64) error
	DeleteGraphView(context.Context, knowledge.Actor, string, uint64) error
}

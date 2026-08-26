package service

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

var graphViewKeyPattern = regexp.MustCompile(`^(?:chunk|entry|evidence):[0-9a-f-]{36}$`)
var graphViewIDPattern = regexp.MustCompile(`^[0-9a-f-]{36}$`)

type SaveGraphViewRequest struct {
	Name             string
	State            memoryStoreAPI.GraphViewState
	ExpectedRevision uint64
}

func (s *Service) graphViewStore() (memoryStoreAPI.GraphViewStore, error) {
	views, ok := s.store.(memoryStoreAPI.GraphViewStore)
	if !ok {
		return nil, memoryStoreAPI.ErrUnsupported
	}
	return views, nil
}

func (s *Service) ListGraphViews(ctx context.Context) ([]memoryStoreAPI.SavedGraphView, error) {
	views, err := s.graphViewStore()
	if err != nil {
		return nil, err
	}
	owner, err := s.actor(ctx)
	if err != nil {
		return nil, err
	}
	return views.ListGraphViews(ctx, owner)
}

func (s *Service) GetGraphView(ctx context.Context, id string) (memoryStoreAPI.SavedGraphView, error) {
	views, err := s.graphViewStore()
	if err != nil {
		return memoryStoreAPI.SavedGraphView{}, err
	}
	owner, err := s.actor(ctx)
	if err != nil {
		return memoryStoreAPI.SavedGraphView{}, err
	}
	return views.GraphView(ctx, owner, strings.TrimSpace(id))
}

func (s *Service) CreateGraphView(ctx context.Context, request SaveGraphViewRequest) (memoryStoreAPI.SavedGraphView, error) {
	views, err := s.graphViewStore()
	if err != nil {
		return memoryStoreAPI.SavedGraphView{}, err
	}
	owner, err := s.actor(ctx)
	if err != nil {
		return memoryStoreAPI.SavedGraphView{}, err
	}
	name, state, err := normalizeGraphViewInput(request.Name, request.State)
	if err != nil || request.ExpectedRevision != 0 {
		if err != nil {
			return memoryStoreAPI.SavedGraphView{}, err
		}
		return memoryStoreAPI.SavedGraphView{}, fmt.Errorf("%w: create graph view revision must be zero", memory.ErrInvalidRecord)
	}
	now := s.now().UTC()
	view := memoryStoreAPI.SavedGraphView{
		ID: s.newID(), Owner: owner, Name: name, State: state, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := views.PutGraphView(ctx, view, 0); err != nil {
		return memoryStoreAPI.SavedGraphView{}, err
	}
	return view, nil
}

func (s *Service) UpdateGraphView(ctx context.Context, id string, request SaveGraphViewRequest) (memoryStoreAPI.SavedGraphView, error) {
	views, err := s.graphViewStore()
	if err != nil {
		return memoryStoreAPI.SavedGraphView{}, err
	}
	owner, err := s.actor(ctx)
	if err != nil {
		return memoryStoreAPI.SavedGraphView{}, err
	}
	name, state, err := normalizeGraphViewInput(request.Name, request.State)
	if err != nil || request.ExpectedRevision == 0 {
		if err != nil {
			return memoryStoreAPI.SavedGraphView{}, err
		}
		return memoryStoreAPI.SavedGraphView{}, fmt.Errorf("%w: update graph view revision is required", memory.ErrInvalidRecord)
	}
	current, err := views.GraphView(ctx, owner, strings.TrimSpace(id))
	if err != nil {
		return memoryStoreAPI.SavedGraphView{}, err
	}
	if current.Revision != request.ExpectedRevision {
		return memoryStoreAPI.SavedGraphView{}, fmt.Errorf("%w: graph view", memoryStoreAPI.ErrConflict)
	}
	next := current
	next.Name = name
	next.State = state
	next.Revision++
	next.UpdatedAt = s.now().UTC()
	if err := views.PutGraphView(ctx, next, request.ExpectedRevision); err != nil {
		return memoryStoreAPI.SavedGraphView{}, err
	}
	return next, nil
}

func (s *Service) DeleteGraphView(ctx context.Context, id string, expectedRevision uint64) error {
	views, err := s.graphViewStore()
	if err != nil {
		return err
	}
	if expectedRevision == 0 {
		return fmt.Errorf("%w: delete graph view revision is required", memory.ErrInvalidRecord)
	}
	owner, err := s.actor(ctx)
	if err != nil {
		return err
	}
	return views.DeleteGraphView(ctx, owner, strings.TrimSpace(id), expectedRevision)
}

func normalizeGraphViewInput(name string, state memoryStoreAPI.GraphViewState) (string, memoryStoreAPI.GraphViewState, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 120 {
		return "", state, fmt.Errorf("%w: graph view name is required and must be at most 120 bytes", memory.ErrInvalidRecord)
	}
	state.Browser.Query = strings.TrimSpace(state.Browser.Query)
	state.Browser.Tag = strings.TrimSpace(state.Browser.Tag)
	state.Browser.ID = strings.TrimSpace(state.Browser.ID)
	if len(state.Browser.Query) > 500 || len(state.Browser.Tag) > 80 || len(state.Browser.ID) > 160 {
		return "", state, fmt.Errorf("%w: graph view browser state exceeds limits", memory.ErrInvalidRecord)
	}
	if !oneOf(state.Browser.Kind, "", "reference", "personal", "project", "environment") ||
		!oneOf(state.Browser.ScopeKind, "", "global", "personal", "project", "session", "environment") ||
		!oneOf(state.Browser.State, "", "draft", "archived") ||
		!oneOf(state.Browser.ObjectKind, "", "chunk", "entry", "link", "evidence") ||
		!oneOf(state.MobilePane, "", "sources", "graph", "inspector") || !oneOf(state.Presentation, "", "canvas", "table") || !oneOf(state.Layout, "", "force_atlas2") {
		return "", state, fmt.Errorf("%w: graph view contains an unsupported option", memory.ErrInvalidRecord)
	}
	if (state.Browser.ObjectKind == "") != (state.Browser.ID == "") {
		return "", state, fmt.Errorf("%w: graph view selection is incomplete", memory.ErrInvalidRecord)
	}
	if state.Browser.ID != "" && !graphViewIDPattern.MatchString(state.Browser.ID) {
		return "", state, fmt.Errorf("%w: graph view selection ID is invalid", memory.ErrInvalidRecord)
	}
	if state.Root != nil {
		if !oneOf(state.Root.Kind.String(), "chunk", "entry") || state.Root.Validate() != nil {
			return "", state, fmt.Errorf("%w: graph view root is invalid", memory.ErrInvalidRecord)
		}
		root := *state.Root
		state.Root = &root
	}
	if len(state.HiddenNodes) > memoryStoreAPI.MaxGraphViewHiddenNodes || len(state.HiddenEdges) > memoryStoreAPI.MaxGraphViewHiddenEdges ||
		len(state.PinnedNodes) > memoryStoreAPI.MaxGraphViewPinnedNodes || len(state.Frontier) > memoryStoreAPI.MaxGraphViewFrontier {
		return "", state, fmt.Errorf("%w: graph view state exceeds limits", memory.ErrInvalidRecord)
	}
	state.HiddenNodes = slices.Clone(state.HiddenNodes)
	state.HiddenEdges = slices.Clone(state.HiddenEdges)
	state.PinnedNodes = slices.Clone(state.PinnedNodes)
	state.Frontier = slices.Clone(state.Frontier)
	if !uniqueValidStrings(state.HiddenNodes, graphViewKeyPattern) || !uniqueValidStrings(state.HiddenEdges, graphViewIDPattern) {
		return "", state, fmt.Errorf("%w: graph view hidden object IDs are invalid", memory.ErrInvalidRecord)
	}
	seenPins := make(map[string]struct{}, len(state.PinnedNodes))
	for _, pin := range state.PinnedNodes {
		if !graphViewKeyPattern.MatchString(pin.Key) || math.IsNaN(pin.X) || math.IsInf(pin.X, 0) || math.IsNaN(pin.Y) || math.IsInf(pin.Y, 0) ||
			math.Abs(pin.X) > 1000000 || math.Abs(pin.Y) > 1000000 {
			return "", state, fmt.Errorf("%w: graph view pin is invalid", memory.ErrInvalidRecord)
		}
		if _, exists := seenPins[pin.Key]; exists {
			return "", state, fmt.Errorf("%w: graph view contains duplicate pins", memory.ErrInvalidRecord)
		}
		seenPins[pin.Key] = struct{}{}
	}
	seenExpansions := make(map[string]struct{}, len(state.Frontier))
	for _, expansion := range state.Frontier {
		key := expansion.Kind + ":" + expansion.ID + ":" + expansion.Direction
		if !oneOf(expansion.Kind, "chunk", "entry") || !graphViewIDPattern.MatchString(expansion.ID) || !oneOf(expansion.Direction, "incoming", "outgoing") {
			return "", state, fmt.Errorf("%w: graph view expansion is invalid", memory.ErrInvalidRecord)
		}
		if _, exists := seenExpansions[key]; exists {
			return "", state, fmt.Errorf("%w: graph view contains duplicate expansions", memory.ErrInvalidRecord)
		}
		seenExpansions[key] = struct{}{}
	}
	if state.Layout == "" {
		state.Layout = "force_atlas2"
	}
	if state.MobilePane == "" {
		state.MobilePane = "graph"
	}
	if state.Presentation == "" {
		state.Presentation = "canvas"
	}
	return name, state, nil
}

func oneOf(value string, allowed ...string) bool {
	return slices.Contains(allowed, strings.TrimSpace(value))
}

func uniqueValidStrings(values []string, pattern *regexp.Regexp) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !pattern.MatchString(value) {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

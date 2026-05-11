package ui

import (
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/theme"
)

// SpinnerTimerOwner identifies timers owned by retained spinner nodes.
const SpinnerTimerOwner = "ui.spinner"

type SpinnerStyle struct {
	ID          string
	Label       string
	Description string
	Frames      []string
}

var spinnerStyles = []SpinnerStyle{
	{ID: "dots", Label: "Braille Dots", Description: "Smooth braille dot spinner", Frames: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}},
	{ID: "blocks", Label: "Braille Blocks", Description: "Heavier braille block spinner", Frames: []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}},
	{ID: "quadrants", Label: "Quadrants", Description: "Simple quadrant motion", Frames: []string{"◴", "◷", "◶", "◵"}},
	{ID: "corners", Label: "Corners", Description: "Corner quadrant rotation", Frames: []string{"◰", "◳", "◲", "◱"}},
	{ID: "circles", Label: "Half Circles", Description: "Readable half-circle spinner", Frames: []string{"◐", "◓", "◑", "◒"}},
	{ID: "arrows", Label: "Arrows", Description: "Directional arrow cycle", Frames: []string{"←", "↖", "↑", "↗", "→", "↘", "↓", "↙"}},
	{ID: "quarters", Label: "Quarter Blocks", Description: "Minimal quarter-block spinner", Frames: []string{"▖", "▘", "▝", "▗"}},
}

func SpinnerStyles() []SpinnerStyle {
	out := make([]SpinnerStyle, len(spinnerStyles))
	copy(out, spinnerStyles)
	return out
}

func SpinnerNames() []string {
	out := make([]string, 0, len(spinnerStyles))
	for _, item := range spinnerStyles {
		out = append(out, item.ID)
	}
	return out
}

func SpinnerStyleByID(id string) SpinnerStyle {
	for _, item := range spinnerStyles {
		if item.ID == id {
			return item
		}
	}
	return spinnerStyles[0]
}

func NormalizeSpinner(id string) string {
	return SpinnerStyleByID(id).ID
}

func SpinnerFrame(id string, frame int) string {
	style := SpinnerStyleByID(id)
	if len(style.Frames) == 0 {
		return ""
	}
	if frame < 0 {
		frame = 0
	}
	return style.Frames[frame%len(style.Frames)]
}

func SpinnerIndex(id string) int {
	names := SpinnerNames()
	return slices.Index(names, NormalizeSpinner(id))
}

// SyncNodeTimers starts or stops timers owned by retained nodes in a subtree.
func SyncNodeTimers(root *Root, node Node) {
	SyncSpinnerTimers(root, node)
}

// HandleNodeTimer routes a timer event to retained nodes in a subtree.
func HandleNodeTimer(node Node, event TimerEvent) bool {
	return HandleSpinnerTimer(node, event)
}

// Spinner is a retained activity indicator that owns its animation frame.
type Spinner struct {
	BaseNode
	Style   string
	Label   string
	Active  bool
	Palette theme.Palette
	frame   int
}

// Set updates spinner display state.
func (s *Spinner) Set(style, label string, active bool, palette theme.Palette) {
	if s == nil {
		return
	}
	style = NormalizeSpinner(style)
	label = strings.TrimSpace(label)
	if s.Style == style && s.Label == label && s.Active == active && s.Palette == palette {
		return
	}
	needsLayout := s.Label != label || s.Active != active
	s.Style = style
	s.Label = label
	s.Active = active
	s.Palette = palette
	if !active {
		s.frame = 0
	}
	if needsLayout {
		s.MarkLayoutDirty()
		return
	}
	s.MarkDirtyLocal(Rect{W: s.Rect().W, H: s.Rect().H})
}

// Measure returns the spinner line size.
func (s *Spinner) Measure(_ *Context, constraints Constraints) Size {
	if s == nil || !s.Active {
		return constraints.Clamp(Size{})
	}
	return constraints.Clamp(Size{W: PlainWidth(s.line()), H: 1})
}

// Paint renders the current spinner frame.
func (s *Spinner) Paint(_ *Context, canvas Canvas) {
	if s == nil || !s.Active || canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	canvas.WriteText(0, 0, PlainTruncate(s.line(), canvas.Width(), ""), CellStyle{FG: cellColor(s.Palette.ActivityText)}.WithBold(true))
}

func (s *Spinner) line() string {
	if s == nil || !s.Active {
		return ""
	}
	frame := SpinnerFrame(s.Style, s.frame)
	if strings.TrimSpace(frame) == "" {
		return ""
	}
	label := strings.TrimSpace(s.Label)
	if label == "" {
		label = "Working ..."
	}
	return frame + "  " + label
}

func (s *Spinner) handleTimer(event TimerEvent) bool {
	if s == nil || event.Owner != SpinnerTimerOwner || !s.Active {
		return false
	}
	s.frame++
	s.MarkDirtyLocal(Rect{W: s.Rect().W, H: s.Rect().H})
	return true
}

// SyncSpinnerTimers starts or stops the shared retained spinner timer.
func SyncSpinnerTimers(root *Root, node Node) {
	if root == nil {
		return
	}
	if nodeHasActiveSpinner(node) {
		if len(root.ActiveTimers(SpinnerTimerOwner)) == 0 {
			root.StartTimer(SpinnerTimerOwner, TimerSpec{Interval: 250 * time.Millisecond, Repeat: true})
		}
		return
	}
	root.StopOwnerTimers(SpinnerTimerOwner)
}

// HandleSpinnerTimer routes a spinner timer event through a retained subtree.
func HandleSpinnerTimer(node Node, event TimerEvent) bool {
	if event.Owner != SpinnerTimerOwner {
		return false
	}
	return handleSpinnerTimer(node, event)
}

func nodeHasActiveSpinner(node Node) bool {
	if node == nil {
		return false
	}
	if spinner, ok := node.(*Spinner); ok && spinner.Active {
		return true
	}
	for _, child := range NodeChildren(node) {
		if nodeHasActiveSpinner(child) {
			return true
		}
	}
	return false
}

func handleSpinnerTimer(node Node, event TimerEvent) bool {
	if node == nil {
		return false
	}
	handled := false
	if spinner, ok := node.(*Spinner); ok && spinner.handleTimer(event) {
		handled = true
	}
	for _, child := range NodeChildren(node) {
		if handleSpinnerTimer(child, event) {
			handled = true
		}
	}
	return handled
}

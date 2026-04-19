package markdown

import (
	"strings"

	"github.com/charmbracelet/glamour"
)

type Renderer struct {
	renderer *glamour.TermRenderer
}

func New() (*Renderer, error) {
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(0),
	)
	if err != nil {
		return nil, err
	}
	return &Renderer{renderer: r}, nil
}

func (r *Renderer) Render(input string) string {
	if r == nil || r.renderer == nil {
		return input
	}
	out, err := r.renderer.Render(strings.TrimSpace(input))
	if err != nil {
		return input
	}
	return strings.TrimSpace(out)
}

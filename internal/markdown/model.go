package markdown

import (
	"net/url"
	"strings"

	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/ui"
)

type ImageRenderMode int

const (
	ImageRenderTextOnly ImageRenderMode = iota
	ImageRenderReserved
)

type ImageSourceKind int

const (
	ImageSourceUnknown ImageSourceKind = iota
	ImageSourceRemote
	ImageSourceLocal
)

type ImageDescriptor struct {
	Alt               string
	Destination       string
	Title             string
	LinkedDestination string
	Inline            bool
	SourceKind        ImageSourceKind
}

func classifyImageSource(destination string) ImageSourceKind {
	parsed, err := url.Parse(strings.TrimSpace(destination))
	if err == nil && parsed != nil && parsed.Scheme != "" {
		switch strings.ToLower(parsed.Scheme) {
		case "http", "https":
			return ImageSourceRemote
		}
	}
	if strings.TrimSpace(destination) != "" {
		return ImageSourceLocal
	}
	return ImageSourceUnknown
}

func renderTextOnlyImage(desc ImageDescriptor, palette theme.Palette) []ui.StyledSpan {
	if desc.Inline {
		return renderInlineImageText(desc, palette)
	}
	return renderBlockImageText(desc, palette)
}

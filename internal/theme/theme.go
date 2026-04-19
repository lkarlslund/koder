package theme

import (
	"slices"

	"github.com/charmbracelet/lipgloss"
)

type Palette struct {
	ActivityText                 lipgloss.Color
	AssistantTimestampText       lipgloss.Color
	ComposerMutedText            lipgloss.Color
	DiffAddedText                lipgloss.Color
	DiffDeletedText              lipgloss.Color
	MarkdownCodeBlockBorder      lipgloss.Color
	MarkdownCodeBlockText        lipgloss.Color
	MarkdownEmphasisText         lipgloss.Color
	MarkdownHeadingPrimary       lipgloss.Color
	MarkdownHeadingSecondary     lipgloss.Color
	MarkdownHeadingTertiary      lipgloss.Color
	MarkdownInlineCodeBackground lipgloss.Color
	MarkdownInlineCodeText       lipgloss.Color
	MarkdownLinkTargetText       lipgloss.Color
	MarkdownLinkText             lipgloss.Color
	MarkdownListEnumeration      lipgloss.Color
	MarkdownListMarker           lipgloss.Color
	MarkdownQuoteBorder          lipgloss.Color
	MarkdownQuoteText            lipgloss.Color
	MarkdownRule                 lipgloss.Color
	MarkdownStrongText           lipgloss.Color
	MarkdownTableBorder          lipgloss.Color
	MarkdownText                 lipgloss.Color
	ReasoningBackground          lipgloss.Color
	ReasoningText                lipgloss.Color
	UserTextBackground           lipgloss.Color
	UserTextForeground           lipgloss.Color
	UserTimestampForeground      lipgloss.Color
}

type Theme struct {
	Name    string
	Palette Palette
}

func Names() []string {
	names := []string{"tokyonight", "gruvbox", "flexoki", "rosepine"}
	slices.Sort(names)
	return names
}

func Resolve(name string) Theme {
	switch name {
	case "", "default", "tokyonight":
		return tokyonight()
	case "gruvbox":
		return gruvbox()
	case "flexoki":
		return flexoki()
	case "rosepine":
		return rosepine()
	default:
		return tokyonight()
	}
}

func Default() Theme {
	return tokyonight()
}

func tokyonight() Theme {
	return Theme{
		Name: "tokyonight",
		Palette: Palette{
			ActivityText:                 color("#7aa2f7"),
			AssistantTimestampText:       color("#565f89"),
			ComposerMutedText:            color("#565f89"),
			DiffAddedText:                color("#41a6b5"),
			DiffDeletedText:              color("#c34043"),
			MarkdownCodeBlockBorder:      color("#565f89"),
			MarkdownCodeBlockText:        color("#c0caf5"),
			MarkdownEmphasisText:         color("#e0af68"),
			MarkdownHeadingPrimary:       color("#bb9af7"),
			MarkdownHeadingSecondary:     color("#7aa2f7"),
			MarkdownHeadingTertiary:      color("#c0caf5"),
			MarkdownInlineCodeBackground: color("#24283b"),
			MarkdownInlineCodeText:       color("#ff9e64"),
			MarkdownLinkTargetText:       color("#7dcfff"),
			MarkdownLinkText:             color("#7aa2f7"),
			MarkdownListEnumeration:      color("#ff9e64"),
			MarkdownListMarker:           color("#7aa2f7"),
			MarkdownQuoteBorder:          color("#565f89"),
			MarkdownQuoteText:            color("#c0caf5"),
			MarkdownRule:                 color("#565f89"),
			MarkdownStrongText:           color("#ff9e64"),
			MarkdownTableBorder:          color("#565f89"),
			MarkdownText:                 color("#c0caf5"),
			ReasoningBackground:          color("#24283b"),
			ReasoningText:                color("#c0caf5"),
			UserTextBackground:           color("#1f2335"),
			UserTextForeground:           color("#c0caf5"),
			UserTimestampForeground:      color("#7dcfff"),
		},
	}
}

func gruvbox() Theme {
	return Theme{
		Name: "gruvbox",
		Palette: Palette{
			ActivityText:                 color("#83a598"),
			AssistantTimestampText:       color("#928374"),
			ComposerMutedText:            color("#928374"),
			DiffAddedText:                color("#b8bb26"),
			DiffDeletedText:              color("#fb4934"),
			MarkdownCodeBlockBorder:      color("#928374"),
			MarkdownCodeBlockText:        color("#ebdbb2"),
			MarkdownEmphasisText:         color("#fabd2f"),
			MarkdownHeadingPrimary:       color("#d3869b"),
			MarkdownHeadingSecondary:     color("#83a598"),
			MarkdownHeadingTertiary:      color("#ebdbb2"),
			MarkdownInlineCodeBackground: color("#3c3836"),
			MarkdownInlineCodeText:       color("#d3869b"),
			MarkdownLinkTargetText:       color("#d3869b"),
			MarkdownLinkText:             color("#83a598"),
			MarkdownListEnumeration:      color("#d3869b"),
			MarkdownListMarker:           color("#fb4934"),
			MarkdownQuoteBorder:          color("#928374"),
			MarkdownQuoteText:            color("#ebdbb2"),
			MarkdownRule:                 color("#928374"),
			MarkdownStrongText:           color("#fb4934"),
			MarkdownTableBorder:          color("#928374"),
			MarkdownText:                 color("#ebdbb2"),
			ReasoningBackground:          color("#3c3836"),
			ReasoningText:                color("#ebdbb2"),
			UserTextBackground:           color("#32302f"),
			UserTextForeground:           color("#ebdbb2"),
			UserTimestampForeground:      color("#d3869b"),
		},
	}
}

func flexoki() Theme {
	return Theme{
		Name: "flexoki",
		Palette: Palette{
			ActivityText:                 color("#4385BE"),
			AssistantTimestampText:       color("#6F6E69"),
			ComposerMutedText:            color("#6F6E69"),
			DiffAddedText:                color("#879A39"),
			DiffDeletedText:              color("#D14D41"),
			MarkdownCodeBlockBorder:      color("#6F6E69"),
			MarkdownCodeBlockText:        color("#CECDC3"),
			MarkdownEmphasisText:         color("#D0A215"),
			MarkdownHeadingPrimary:       color("#8B7EC8"),
			MarkdownHeadingSecondary:     color("#DA702C"),
			MarkdownHeadingTertiary:      color("#CECDC3"),
			MarkdownInlineCodeBackground: color("#1c1b1a"),
			MarkdownInlineCodeText:       color("#3AA99F"),
			MarkdownLinkTargetText:       color("#3AA99F"),
			MarkdownLinkText:             color("#4385BE"),
			MarkdownListEnumeration:      color("#3AA99F"),
			MarkdownListMarker:           color("#DA702C"),
			MarkdownQuoteBorder:          color("#6F6E69"),
			MarkdownQuoteText:            color("#CECDC3"),
			MarkdownRule:                 color("#6F6E69"),
			MarkdownStrongText:           color("#DA702C"),
			MarkdownTableBorder:          color("#6F6E69"),
			MarkdownText:                 color("#CECDC3"),
			ReasoningBackground:          color("#1c1b1a"),
			ReasoningText:                color("#CECDC3"),
			UserTextBackground:           color("#181716"),
			UserTextForeground:           color("#CECDC3"),
			UserTimestampForeground:      color("#3AA99F"),
		},
	}
}

func rosepine() Theme {
	return Theme{
		Name: "rosepine",
		Palette: Palette{
			ActivityText:                 color("#9ccfd8"),
			AssistantTimestampText:       color("#6e6a86"),
			ComposerMutedText:            color("#6e6a86"),
			DiffAddedText:                color("#31748f"),
			DiffDeletedText:              color("#eb6f92"),
			MarkdownCodeBlockBorder:      color("#403d52"),
			MarkdownCodeBlockText:        color("#e0def4"),
			MarkdownEmphasisText:         color("#f6c177"),
			MarkdownHeadingPrimary:       color("#c4a7e7"),
			MarkdownHeadingSecondary:     color("#9ccfd8"),
			MarkdownHeadingTertiary:      color("#e0def4"),
			MarkdownInlineCodeBackground: color("#26233a"),
			MarkdownInlineCodeText:       color("#31748f"),
			MarkdownLinkTargetText:       color("#ebbcba"),
			MarkdownLinkText:             color("#9ccfd8"),
			MarkdownListEnumeration:      color("#ebbcba"),
			MarkdownListMarker:           color("#9ccfd8"),
			MarkdownQuoteBorder:          color("#6e6a86"),
			MarkdownQuoteText:            color("#e0def4"),
			MarkdownRule:                 color("#403d52"),
			MarkdownStrongText:           color("#eb6f92"),
			MarkdownTableBorder:          color("#403d52"),
			MarkdownText:                 color("#e0def4"),
			ReasoningBackground:          color("#26233a"),
			ReasoningText:                color("#e0def4"),
			UserTextBackground:           color("#1f1d2e"),
			UserTextForeground:           color("#e0def4"),
			UserTimestampForeground:      color("#9ccfd8"),
		},
	}
}

func color(value string) lipgloss.Color {
	return lipgloss.Color(value)
}

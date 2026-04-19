package theme

import "github.com/charmbracelet/lipgloss"

type Palette struct {
	ActivityText                 lipgloss.Color
	AssistantTimestampText       lipgloss.Color
	ComposerMutedText            lipgloss.Color
	DiffAddedText                lipgloss.Color
	DiffDeletedText              lipgloss.Color
	MarkdownCodeBlockBorder      lipgloss.Color
	MarkdownCodeBlockText        lipgloss.Color
	MarkdownHeadingPrimary       lipgloss.Color
	MarkdownHeadingSecondary     lipgloss.Color
	MarkdownHeadingTertiary      lipgloss.Color
	MarkdownInlineCodeBackground lipgloss.Color
	MarkdownInlineCodeText       lipgloss.Color
	MarkdownLinkText             lipgloss.Color
	MarkdownListMarker           lipgloss.Color
	MarkdownQuoteBorder          lipgloss.Color
	MarkdownQuoteText            lipgloss.Color
	MarkdownRule                 lipgloss.Color
	MarkdownTableBorder          lipgloss.Color
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

func Resolve(name string) Theme {
	switch name {
	case "default", "":
		fallthrough
	default:
		return Default()
	}
}

func Default() Theme {
	return Theme{
		Name: "default",
		Palette: Palette{
			ActivityText:                 lipgloss.Color("45"),
			AssistantTimestampText:       lipgloss.Color("245"),
			ComposerMutedText:            lipgloss.Color("251"),
			DiffAddedText:                lipgloss.Color("2"),
			DiffDeletedText:              lipgloss.Color("1"),
			MarkdownCodeBlockBorder:      lipgloss.Color("240"),
			MarkdownCodeBlockText:        lipgloss.Color("252"),
			MarkdownHeadingPrimary:       lipgloss.Color("230"),
			MarkdownHeadingSecondary:     lipgloss.Color("223"),
			MarkdownHeadingTertiary:      lipgloss.Color("252"),
			MarkdownInlineCodeBackground: lipgloss.Color("237"),
			MarkdownInlineCodeText:       lipgloss.Color("228"),
			MarkdownLinkText:             lipgloss.Color("117"),
			MarkdownListMarker:           lipgloss.Color("111"),
			MarkdownQuoteBorder:          lipgloss.Color("243"),
			MarkdownQuoteText:            lipgloss.Color("250"),
			MarkdownRule:                 lipgloss.Color("240"),
			MarkdownTableBorder:          lipgloss.Color("240"),
			ReasoningBackground:          lipgloss.Color("236"),
			ReasoningText:                lipgloss.Color("252"),
			UserTextBackground:           lipgloss.Color("238"),
			UserTextForeground:           lipgloss.Color("255"),
			UserTimestampForeground:      lipgloss.Color("251"),
		},
	}
}

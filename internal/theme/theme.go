package theme

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"sync"

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
	ScreenBackground             lipgloss.Color
	SelectionBackground          lipgloss.Color
	SelectionForeground          lipgloss.Color
	SidebarBackground            lipgloss.Color
	SidebarBorder                lipgloss.Color
	SidebarForeground            lipgloss.Color
	UserAccentBar                lipgloss.Color
	UserTextBackground           lipgloss.Color
	UserTextForeground           lipgloss.Color
	UserTimestampForeground      lipgloss.Color
}

type Theme struct {
	Name    string
	Palette Palette
}

//go:embed opencode/*.json
var opencodeThemesFS embed.FS

type opencodeThemeFile struct {
	Defs  map[string]string        `json:"defs"`
	Theme map[string]opencodeColor `json:"theme"`
}

type opencodeVariant struct {
	Dark  string `json:"dark"`
	Light string `json:"light"`
}

type opencodeColor struct {
	value   string
	variant *opencodeVariant
}

func (c *opencodeColor) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		c.value = single
		c.variant = nil
		return nil
	}
	var variant opencodeVariant
	if err := json.Unmarshal(data, &variant); err == nil {
		c.variant = &variant
		c.value = ""
		return nil
	}
	return fmt.Errorf("unsupported color value: %s", string(data))
}

func (c opencodeColor) dark() string {
	if c.variant != nil && strings.TrimSpace(c.variant.Dark) != "" {
		return c.variant.Dark
	}
	return c.value
}

var (
	registryOnce sync.Once
	registry     map[string]Theme
)

func Names() []string {
	names := make([]string, 0, len(themes()))
	for name := range themes() {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func Resolve(name string) Theme {
	if name == "" || name == "default" {
		return Default()
	}
	if resolved, ok := themes()[name]; ok {
		return resolved
	}
	return Default()
}

func Default() Theme {
	return themes()["tokyonight"]
}

func themes() map[string]Theme {
	registryOnce.Do(func() {
		registry = make(map[string]Theme)
		for name, theme := range loadOpenCodeThemes() {
			registry[name] = theme
		}
		for name, theme := range claudeThemes() {
			registry[name] = theme
		}
	})
	return registry
}

func loadOpenCodeThemes() map[string]Theme {
	entries, err := fs.ReadDir(opencodeThemesFS, "opencode")
	if err != nil {
		panic(fmt.Errorf("read embedded opencode themes: %w", err))
	}

	themes := make(map[string]Theme, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		data, err := opencodeThemesFS.ReadFile(filepath.Join("opencode", entry.Name()))
		if err != nil {
			panic(fmt.Errorf("read embedded theme %s: %w", name, err))
		}
		var raw opencodeThemeFile
		if err := json.Unmarshal(data, &raw); err != nil {
			panic(fmt.Errorf("decode embedded theme %s: %w", name, err))
		}
		themes[name] = Theme{
			Name:    name,
			Palette: buildOpenCodePalette(raw),
		}
	}
	return themes
}

func buildOpenCodePalette(src opencodeThemeFile) Palette {
	resolve := func(key string) lipgloss.Color {
		return color(resolveOpenCodeValue(src, key))
	}

	return Palette{
		ActivityText:                 firstNonEmpty(resolve("primary"), resolve("info")),
		AssistantTimestampText:       resolve("textMuted"),
		ComposerMutedText:            resolve("textMuted"),
		DiffAddedText:                resolve("diffAdded"),
		DiffDeletedText:              resolve("diffRemoved"),
		MarkdownCodeBlockBorder:      firstNonEmpty(resolve("border"), resolve("borderSubtle")),
		MarkdownCodeBlockText:        firstNonEmpty(resolve("markdownCodeBlock"), resolve("markdownText")),
		MarkdownEmphasisText:         resolve("markdownEmph"),
		MarkdownHeadingPrimary:       resolve("markdownHeading"),
		MarkdownHeadingSecondary:     resolve("markdownLink"),
		MarkdownHeadingTertiary:      resolve("markdownText"),
		MarkdownInlineCodeBackground: resolve("backgroundElement"),
		MarkdownInlineCodeText:       resolve("markdownCode"),
		MarkdownLinkTargetText:       resolve("markdownLinkText"),
		MarkdownLinkText:             resolve("markdownLink"),
		MarkdownListEnumeration:      resolve("markdownListEnumeration"),
		MarkdownListMarker:           resolve("markdownListItem"),
		MarkdownQuoteBorder:          resolve("borderSubtle"),
		MarkdownQuoteText:            resolve("markdownBlockQuote"),
		MarkdownRule:                 resolve("markdownHorizontalRule"),
		MarkdownStrongText:           resolve("markdownStrong"),
		MarkdownTableBorder:          resolve("border"),
		MarkdownText:                 resolve("markdownText"),
		ReasoningBackground:          firstNonEmpty(resolve("backgroundElement"), resolve("backgroundPanel")),
		ReasoningText:                resolve("text"),
		ScreenBackground:             firstNonEmpty(resolve("background"), resolve("backgroundPanel"), resolve("backgroundElement")),
		SelectionBackground:          firstNonEmpty(resolve("secondary"), resolve("primary"), resolve("info"), resolve("backgroundElement")),
		SelectionForeground:          firstNonEmpty(resolve("background"), resolve("backgroundPanel"), resolve("backgroundElement"), resolve("text")),
		SidebarBackground:            resolve("backgroundPanel"),
		SidebarBorder:                resolve("border"),
		SidebarForeground:            resolve("text"),
		UserAccentBar:                firstNonEmpty(resolve("primary"), resolve("info"), resolve("text")),
		UserTextBackground:           resolve("backgroundElement"),
		UserTextForeground:           resolve("text"),
		UserTimestampForeground:      firstNonEmpty(resolve("info"), resolve("secondary"), resolve("primary")),
	}
}

func resolveOpenCodeValue(src opencodeThemeFile, key string) string {
	seen := map[string]bool{}
	var resolveRef func(string) string
	resolveRef = func(value string) string {
		value = strings.TrimSpace(value)
		if value == "" {
			return ""
		}
		if strings.HasPrefix(value, "#") {
			return value
		}
		if value == "transparent" || value == "none" {
			return "#000000"
		}
		if seen[value] {
			return ""
		}
		seen[value] = true
		if def, ok := src.Defs[value]; ok {
			return resolveRef(def)
		}
		if nested, ok := src.Theme[value]; ok {
			return resolveRef(nested.dark())
		}
		return ""
	}

	if colorValue, ok := src.Theme[key]; ok {
		return resolveRef(colorValue.dark())
	}
	return ""
}

func claudeThemes() map[string]Theme {
	return map[string]Theme{
		"claude-dark": {
			Name: "claude-dark",
			Palette: Palette{
				ActivityText:                 color("#b1b9f9"),
				AssistantTimestampText:       color("#999999"),
				ComposerMutedText:            color("#505050"),
				DiffAddedText:                color("#4eba65"),
				DiffDeletedText:              color("#ff6b80"),
				MarkdownCodeBlockBorder:      color("#888888"),
				MarkdownCodeBlockText:        color("#ffffff"),
				MarkdownEmphasisText:         color("#ffc107"),
				MarkdownHeadingPrimary:       color("#af87ff"),
				MarkdownHeadingSecondary:     color("#b1b9f9"),
				MarkdownHeadingTertiary:      color("#ffffff"),
				MarkdownInlineCodeBackground: color("#373737"),
				MarkdownInlineCodeText:       color("#4eba65"),
				MarkdownLinkTargetText:       color("#b1b9f9"),
				MarkdownLinkText:             color("#b1b9f9"),
				MarkdownListEnumeration:      color("#7ab4e8"),
				MarkdownListMarker:           color("#7ab4e8"),
				MarkdownQuoteBorder:          color("#505050"),
				MarkdownQuoteText:            color("#999999"),
				MarkdownRule:                 color("#505050"),
				MarkdownStrongText:           color("#d77757"),
				MarkdownTableBorder:          color("#888888"),
				MarkdownText:                 color("#ffffff"),
				ReasoningBackground:          color("#373737"),
				ReasoningText:                color("#ffffff"),
				ScreenBackground:             color("#141413"),
				SelectionBackground:          color("#3b4261"),
				SelectionForeground:          color("#ffffff"),
				SidebarBackground:            color("#1f1f1f"),
				SidebarBorder:                color("#505050"),
				SidebarForeground:            color("#ffffff"),
				UserAccentBar:                color("#b1b9f9"),
				UserTextBackground:           color("#373737"),
				UserTextForeground:           color("#ffffff"),
				UserTimestampForeground:      color("#7ab4e8"),
			},
		},
		"claude-light": {
			Name: "claude-light",
			Palette: Palette{
				ActivityText:                 color("#5769f7"),
				AssistantTimestampText:       color("#666666"),
				ComposerMutedText:            color("#afafaf"),
				DiffAddedText:                color("#2c7a39"),
				DiffDeletedText:              color("#ab2b3f"),
				MarkdownCodeBlockBorder:      color("#999999"),
				MarkdownCodeBlockText:        color("#000000"),
				MarkdownEmphasisText:         color("#966c1e"),
				MarkdownHeadingPrimary:       color("#8700ff"),
				MarkdownHeadingSecondary:     color("#5769f7"),
				MarkdownHeadingTertiary:      color("#000000"),
				MarkdownInlineCodeBackground: color("#f0f0f0"),
				MarkdownInlineCodeText:       color("#2c7a39"),
				MarkdownLinkTargetText:       color("#5769f7"),
				MarkdownLinkText:             color("#5769f7"),
				MarkdownListEnumeration:      color("#4782c8"),
				MarkdownListMarker:           color("#4782c8"),
				MarkdownQuoteBorder:          color("#afafaf"),
				MarkdownQuoteText:            color("#666666"),
				MarkdownRule:                 color("#afafaf"),
				MarkdownStrongText:           color("#d77757"),
				MarkdownTableBorder:          color("#999999"),
				MarkdownText:                 color("#000000"),
				ReasoningBackground:          color("#f0f0f0"),
				ReasoningText:                color("#000000"),
				ScreenBackground:             color("#fdfdfd"),
				SelectionBackground:          color("#cfd8ff"),
				SelectionForeground:          color("#000000"),
				SidebarBackground:            color("#f5f5f5"),
				SidebarBorder:                color("#b7b7b7"),
				SidebarForeground:            color("#000000"),
				UserAccentBar:                color("#5769f7"),
				UserTextBackground:           color("#f0f0f0"),
				UserTextForeground:           color("#000000"),
				UserTimestampForeground:      color("#4782c8"),
			},
		},
		"claude-dark-daltonized": {
			Name: "claude-dark-daltonized",
			Palette: Palette{
				ActivityText:                 color("#99ccff"),
				AssistantTimestampText:       color("#999999"),
				ComposerMutedText:            color("#505050"),
				DiffAddedText:                color("#0077b3"),
				DiffDeletedText:              color("#ff6666"),
				MarkdownCodeBlockBorder:      color("#888888"),
				MarkdownCodeBlockText:        color("#ffffff"),
				MarkdownEmphasisText:         color("#ffcc00"),
				MarkdownHeadingPrimary:       color("#af87ff"),
				MarkdownHeadingSecondary:     color("#99ccff"),
				MarkdownHeadingTertiary:      color("#ffffff"),
				MarkdownInlineCodeBackground: color("#373737"),
				MarkdownInlineCodeText:       color("#3399ff"),
				MarkdownLinkTargetText:       color("#99ccff"),
				MarkdownLinkText:             color("#99ccff"),
				MarkdownListEnumeration:      color("#66b2ff"),
				MarkdownListMarker:           color("#66b2ff"),
				MarkdownQuoteBorder:          color("#505050"),
				MarkdownQuoteText:            color("#999999"),
				MarkdownRule:                 color("#505050"),
				MarkdownStrongText:           color("#ff9933"),
				MarkdownTableBorder:          color("#888888"),
				MarkdownText:                 color("#ffffff"),
				ReasoningBackground:          color("#373737"),
				ReasoningText:                color("#ffffff"),
				ScreenBackground:             color("#141413"),
				SelectionBackground:          color("#3d4f66"),
				SelectionForeground:          color("#ffffff"),
				SidebarBackground:            color("#1f1f1f"),
				SidebarBorder:                color("#505050"),
				SidebarForeground:            color("#ffffff"),
				UserAccentBar:                color("#99ccff"),
				UserTextBackground:           color("#373737"),
				UserTextForeground:           color("#ffffff"),
				UserTimestampForeground:      color("#66b2ff"),
			},
		},
		"claude-light-daltonized": {
			Name: "claude-light-daltonized",
			Palette: Palette{
				ActivityText:                 color("#3366ff"),
				AssistantTimestampText:       color("#666666"),
				ComposerMutedText:            color("#afafaf"),
				DiffAddedText:                color("#006699"),
				DiffDeletedText:              color("#cc0000"),
				MarkdownCodeBlockBorder:      color("#999999"),
				MarkdownCodeBlockText:        color("#000000"),
				MarkdownEmphasisText:         color("#ff9900"),
				MarkdownHeadingPrimary:       color("#8700ff"),
				MarkdownHeadingSecondary:     color("#3366ff"),
				MarkdownHeadingTertiary:      color("#000000"),
				MarkdownInlineCodeBackground: color("#ececec"),
				MarkdownInlineCodeText:       color("#3366cc"),
				MarkdownLinkTargetText:       color("#3366ff"),
				MarkdownLinkText:             color("#3366ff"),
				MarkdownListEnumeration:      color("#0066cc"),
				MarkdownListMarker:           color("#0066cc"),
				MarkdownQuoteBorder:          color("#afafaf"),
				MarkdownQuoteText:            color("#666666"),
				MarkdownRule:                 color("#afafaf"),
				MarkdownStrongText:           color("#ff9933"),
				MarkdownTableBorder:          color("#999999"),
				MarkdownText:                 color("#000000"),
				ReasoningBackground:          color("#ececec"),
				ReasoningText:                color("#000000"),
				ScreenBackground:             color("#fdfdfd"),
				SelectionBackground:          color("#c5d9ea"),
				SelectionForeground:          color("#000000"),
				SidebarBackground:            color("#f5f5f5"),
				SidebarBorder:                color("#b7b7b7"),
				SidebarForeground:            color("#000000"),
				UserAccentBar:                color("#3366ff"),
				UserTextBackground:           color("#dcdcdc"),
				UserTextForeground:           color("#000000"),
				UserTimestampForeground:      color("#0066cc"),
			},
		},
	}
}

func firstNonEmpty(values ...lipgloss.Color) lipgloss.Color {
	for _, value := range values {
		if strings.TrimSpace(string(value)) != "" {
			return value
		}
	}
	return lipgloss.Color("")
}

func color(value string) lipgloss.Color {
	return lipgloss.Color(value)
}

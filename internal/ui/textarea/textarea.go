package textarea

import (
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const blinkInterval = 530 * time.Millisecond

type blinkMsg struct{}

type Cursor struct {
	char      string
	TextStyle lipgloss.Style
}

func (c *Cursor) SetChar(char string) {
	c.char = char
}

func (c Cursor) View() string {
	if c.char == "" {
		return c.TextStyle.Render(" ")
	}
	return c.TextStyle.Render(c.char)
}

type Style struct {
	Base        lipgloss.Style
	CursorLine  lipgloss.Style
	Text        lipgloss.Style
	Prompt      lipgloss.Style
	Placeholder lipgloss.Style
	EndOfBuffer lipgloss.Style
}

func DefaultStyles() (Style, Style) {
	return Style{}, Style{}
}

type Model struct {
	Prompt          string
	Placeholder     string
	ShowLineNumbers bool
	FocusedStyle    Style
	BlurredStyle    Style
	Cursor          Cursor

	width  int
	height int
	focus  bool
	value  []rune
	cursor int
	blink  bool
}

func New() Model {
	return Model{focus: true, height: 1, blink: true}
}

func (m *Model) Focus() {
	m.focus = true
	m.blink = true
}

func (m *Model) Blur() {
	m.focus = false
	m.blink = false
}

func (m *Model) SetWidth(width int) {
	m.width = width
}

func (m *Model) SetHeight(height int) {
	m.height = height
}

func (m Model) Value() string {
	return string(m.value)
}

func (m *Model) SetValue(value string) {
	m.value = []rune(value)
	m.cursor = len(m.value)
}

func (m *Model) Reset() {
	m.value = nil
	m.cursor = 0
}

func (m *Model) SetCursor(offset int) {
	if offset < 0 {
		offset = 0
	}
	m.cursor = byteOffsetToRuneIndex(string(m.value), offset)
	if m.cursor > len(m.value) {
		m.cursor = len(m.value)
	}
}

func (m *Model) InsertRune(r rune) {
	m.insertRunes([]rune{r})
}

func (m *Model) InsertString(s string) {
	m.insertRunes([]rune(s))
}

func (m Model) BlinkCmd() tea.Cmd {
	if !m.focus {
		return nil
	}
	return tea.Tick(blinkInterval, func(time.Time) tea.Msg {
		return blinkMsg{}
	})
}

func (m *Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case blinkMsg:
		if !m.focus {
			return *m, nil
		}
		m.blink = !m.blink
		return *m, m.BlinkCmd()
	case tea.KeyMsg:
		if !m.focus {
			return *m, nil
		}
		m.blink = true
		cmd := m.BlinkCmd()
		switch msg.Type {
		case tea.KeyLeft:
			if m.cursor > 0 {
				m.cursor--
			}
		case tea.KeyRight:
			if m.cursor < len(m.value) {
				m.cursor++
			}
		case tea.KeyHome:
			m.cursor = 0
		case tea.KeyEnd:
			m.cursor = len(m.value)
		case tea.KeyBackspace:
			if m.cursor > 0 {
				m.value = append(m.value[:m.cursor-1], m.value[m.cursor:]...)
				m.cursor--
			}
		case tea.KeyDelete:
			if m.cursor < len(m.value) {
				m.value = append(m.value[:m.cursor], m.value[m.cursor+1:]...)
			}
		case tea.KeySpace:
			m.insertRunes([]rune{' '})
		case tea.KeyRunes:
			m.insertRunes(msg.Runes)
		default:
			switch msg.String() {
			case "ctrl+a":
				m.cursor = 0
			case "ctrl+e":
				m.cursor = len(m.value)
			}
		}
		return *m, cmd
	default:
		return *m, nil
	}
}

func (m Model) View() string {
	line := m.visibleLine()
	style := m.BlurredStyle
	if m.focus {
		style = m.FocusedStyle
	}
	prompt := style.Prompt.Render(m.Prompt)
	if !m.focus || !m.blink {
		return prompt + style.Text.Render(line.plain)
	}
	text := line.before + m.Cursor.TextStyle.Render(line.cursor) + line.after
	return prompt + style.Text.Render(text)
}

type visibleLine struct {
	before string
	cursor string
	after  string
	plain  string
}

func (m Model) visibleLine() visibleLine {
	runes := m.value
	cursor := m.cursor
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(runes) {
		cursor = len(runes)
	}
	lineStart := 0
	for i := 0; i < cursor; i++ {
		if runes[i] == '\n' {
			lineStart = i + 1
		}
	}
	lineEnd := len(runes)
	for i := cursor; i < len(runes); i++ {
		if runes[i] == '\n' {
			lineEnd = i
			break
		}
	}
	lineRunes := runes[lineStart:lineEnd]
	localCursor := cursor - lineStart
	if localCursor < 0 {
		localCursor = 0
	}
	if localCursor > len(lineRunes) {
		localCursor = len(lineRunes)
	}
	contentWidth := max(1, m.width-ansi.StringWidth(m.Prompt))
	start, end := windowAroundCursor(lineRunes, localCursor, contentWidth)
	lineRunes = lineRunes[start:end]
	localCursor -= start
	before := string(lineRunes[:localCursor])
	char := " "
	if localCursor < len(lineRunes) {
		char = string(lineRunes[localCursor])
	}
	after := ""
	if localCursor < len(lineRunes) {
		after = string(lineRunes[localCursor+1:])
	}
	return visibleLine{before: before, cursor: char, after: after, plain: string(lineRunes)}
}

func (m *Model) insertRunes(runes []rune) {
	if len(runes) == 0 {
		return
	}
	head := append([]rune{}, m.value[:m.cursor]...)
	head = append(head, runes...)
	m.value = append(head, m.value[m.cursor:]...)
	m.cursor += len(runes)
}

func byteOffsetToRuneIndex(s string, offset int) int {
	if offset <= 0 {
		return 0
	}
	if offset >= len(s) {
		return utf8.RuneCountInString(s)
	}
	return utf8.RuneCountInString(s[:offset])
}

func windowAroundCursor(runes []rune, cursor int, width int) (int, int) {
	if width <= 0 || len(runes) <= width {
		return 0, len(runes)
	}
	start := cursor - width + 1
	if start < 0 {
		start = 0
	}
	end := start + width
	if end > len(runes) {
		end = len(runes)
		start = max(0, end-width)
	}
	return start, end
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

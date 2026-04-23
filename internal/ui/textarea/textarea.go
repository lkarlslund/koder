package textarea

import (
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	tea "github.com/lkarlslund/koder/internal/ui/tea"
	"github.com/mattn/go-runewidth"
)

const blinkInterval = 530 * time.Millisecond

type blinkMsg struct{}
type blinkTickMsg struct {
	generation int
}

type Cursor struct {
	char      string
	TextStyle lipgloss.Style
}

func (c *Cursor) SetChar(char string) {
	c.char = char
}

func (c Cursor) View() string {
	if c.char == "" {
		return "|"
	}
	return "|" + c.char
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
	BlinkEnabled    bool
	FocusedStyle    Style
	BlurredStyle    Style
	Cursor          Cursor

	width           int
	height          int
	focus           bool
	value           []rune
	cachedValue     string
	valueDirty      bool
	cursor          int
	blink           bool
	blinkGeneration int
	revision        uint64
}

func New() Model {
	return Model{focus: true, height: 1, blink: true, BlinkEnabled: true}
}

func (m *Model) Focus() {
	m.focus = true
	m.blink = true
	m.blinkGeneration++
}

func (m *Model) Blur() {
	m.focus = false
	m.blink = false
	m.blinkGeneration++
}

func (m *Model) SetWidth(width int) {
	m.width = width
}

func (m *Model) SetHeight(height int) {
	m.height = height
}

func (m *Model) Value() string {
	if !m.valueDirty {
		return m.cachedValue
	}
	m.cachedValue = string(m.value)
	m.valueDirty = false
	return m.cachedValue
}

func (m *Model) SetValue(value string) {
	m.value = []rune(value)
	m.valueDirty = true
	m.cachedValue = ""
	m.cursor = len(m.value)
	m.revision++
}

func (m *Model) Reset() {
	m.value = nil
	m.valueDirty = true
	m.cachedValue = ""
	m.cursor = 0
	m.revision++
}

func (m Model) Revision() uint64 {
	return m.revision
}

func (m Model) RuneCount() int {
	return len(m.value)
}

func (m Model) CursorIndex() int {
	return m.cursor
}

func (m Model) RuneAt(i int) (rune, bool) {
	if i < 0 || i >= len(m.value) {
		return 0, false
	}
	return m.value[i], true
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
	if !m.focus || !m.BlinkEnabled {
		return nil
	}
	generation := m.blinkGeneration
	return tea.Tick(blinkInterval, func(time.Time) tea.Msg {
		return blinkTickMsg{generation: generation}
	})
}

func (m *Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case blinkMsg:
		if !m.focus || !m.BlinkEnabled {
			return *m, nil
		}
		m.blink = !m.blink
		return *m, m.BlinkCmd()
	case blinkTickMsg:
		if !m.focus || !m.BlinkEnabled {
			return *m, nil
		}
		if msg.generation != m.blinkGeneration {
			return *m, nil
		}
		m.blink = !m.blink
		return *m, m.BlinkCmd()
	case tea.KeyMsg:
		if !m.focus {
			return *m, nil
		}
		m.blink = true
		m.blinkGeneration++
		var cmd tea.Cmd
		if m.BlinkEnabled {
			cmd = m.BlinkCmd()
		}
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
				m.valueDirty = true
				m.cachedValue = ""
				m.cursor--
				m.revision++
			}
		case tea.KeyDelete:
			if m.cursor < len(m.value) {
				m.value = append(m.value[:m.cursor], m.value[m.cursor+1:]...)
				m.valueDirty = true
				m.cachedValue = ""
				m.revision++
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
	if !m.focus || (!m.blink && m.BlinkEnabled) {
		return m.Prompt + line.plain
	}
	return m.Prompt + line.before + m.CursorView(line.cursor) + line.after
}

func (m Model) CursorView(char string) string {
	if char == "" {
		char = " "
	}
	if !m.focus || (!m.blink && m.BlinkEnabled) {
		return char
	}
	if char == " " {
		return "|"
	}
	return "|" + char
}

func (m Model) CursorVisible() bool {
	return m.focus && (!m.BlinkEnabled || m.blink)
}

func (m Model) Focused() bool {
	return m.focus
}

type VisibleLine struct {
	before string
	cursor string
	after  string
	plain  string
}

func (m Model) VisibleLine() VisibleLine {
	line := m.visibleLine()
	return VisibleLine{
		before: line.before,
		cursor: line.cursor,
		after:  line.after,
		plain:  line.plain,
	}
}

func (l VisibleLine) Before() string { return l.before }
func (l VisibleLine) Cursor() string { return l.cursor }
func (l VisibleLine) After() string  { return l.after }
func (l VisibleLine) Plain() string  { return l.plain }

func (m Model) visibleLine() VisibleLine {
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
	contentWidth := max(1, m.width-runewidth.StringWidth(m.Prompt))
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
	return VisibleLine{before: before, cursor: char, after: after, plain: string(lineRunes)}
}

func (m *Model) insertRunes(runes []rune) {
	if len(runes) == 0 {
		return
	}
	if m.cursor >= len(m.value) {
		m.value = append(m.value, runes...)
	} else {
		oldLen := len(m.value)
		insertLen := len(runes)
		m.value = append(m.value, make([]rune, insertLen)...)
		copy(m.value[m.cursor+insertLen:], m.value[m.cursor:oldLen])
		copy(m.value[m.cursor:], runes)
	}
	m.valueDirty = true
	m.cachedValue = ""
	m.cursor += len(runes)
	m.revision++
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

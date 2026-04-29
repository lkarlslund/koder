package textarea

import (
	"time"
	"unicode/utf8"

	"github.com/lkarlslund/koder/internal/ui"
	"github.com/mattn/go-runewidth"
)

const blinkInterval = 530 * time.Millisecond

func BlinkInterval() time.Duration {
	return blinkInterval
}

type blinkMsg struct{}
type blinkTickMsg struct {
	generation int
}

type Cursor struct {
	char      string
	TextStyle ui.Style
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
	Base        ui.Style
	CursorLine  ui.Style
	Text        ui.Style
	Prompt      ui.Style
	Placeholder ui.Style
	EndOfBuffer ui.Style
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
	tokens          []Token
}

type Token struct {
	Start int
	End   int
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

func (m *Model) ToggleBlink() bool {
	if !m.focus || !m.BlinkEnabled {
		return false
	}
	m.blink = !m.blink
	return true
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
	m.tokens = nil
	m.revision++
}

func (m *Model) Reset() {
	m.value = nil
	m.valueDirty = true
	m.cachedValue = ""
	m.cursor = 0
	m.tokens = nil
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

func (m Model) Runes() []rune {
	return m.value
}

func (m Model) Tokens() []Token {
	if len(m.tokens) == 0 {
		return nil
	}
	out := make([]Token, len(m.tokens))
	copy(out, m.tokens)
	return out
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

func (m *Model) InsertToken(text string) {
	m.insertTokenRunes([]rune(text))
}

func (m *Model) ReplaceRangeWithToken(start int, end int, text string) {
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	if end > len(m.value) {
		end = len(m.value)
	}
	m.removeRange(start, end)
	m.cursor = start
	m.insertTokenRunes([]rune(text))
}

func (m *Model) RegisterToken(start int, end int) {
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	if end > len(m.value) {
		end = len(m.value)
	}
	if start == end {
		return
	}
	m.tokens = append(m.tokens, Token{Start: start, End: end})
}

func (m Model) BlinkCmd() ui.Cmd {
	if !m.focus || !m.BlinkEnabled {
		return nil
	}
	generation := m.blinkGeneration
	return ui.Tick(blinkInterval, func(time.Time) ui.Msg {
		return blinkTickMsg{generation: generation}
	})
}

func (m *Model) Update(msg ui.Msg) (Model, ui.Cmd) {
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
	case ui.KeyMsg:
		if !m.focus {
			return *m, nil
		}
		m.blink = true
		m.blinkGeneration++
		var cmd ui.Cmd
		if m.BlinkEnabled {
			cmd = m.BlinkCmd()
		}
		switch msg.Type {
		case ui.KeyLeft:
			if msg.Alt {
				m.cursor = m.moveCursorWordLeft()
			} else {
				m.cursor = m.moveCursorLeft()
			}
		case ui.KeyRight:
			if msg.Alt {
				m.cursor = m.moveCursorWordRight()
			} else {
				m.cursor = m.moveCursorRight()
			}
		case ui.KeyHome:
			m.cursor = 0
		case ui.KeyEnd:
			m.cursor = len(m.value)
		case ui.KeyBackspace:
			if msg.Alt {
				if m.deleteWordForBackspace() {
					m.revision++
				}
			} else if m.deleteTokenForBackspace() {
				m.revision++
			} else if m.cursor > 0 {
				m.removeRange(m.cursor-1, m.cursor)
				m.cursor--
				m.revision++
			}
		case ui.KeyDelete:
			if msg.Alt {
				if m.deleteWordForDelete() {
					m.revision++
				}
			} else if m.deleteTokenForDelete() {
				m.revision++
			} else if m.cursor < len(m.value) {
				m.removeRange(m.cursor, m.cursor+1)
				m.revision++
			}
		case ui.KeySpace:
			m.insertRunes([]rune{' '})
		case ui.KeyRunes:
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
	tokens []Token
}

func (m Model) VisibleLine() VisibleLine {
	line := m.visibleLine()
	return VisibleLine{
		before: line.before,
		cursor: line.cursor,
		after:  line.after,
		plain:  line.plain,
		tokens: line.tokens,
	}
}

func (l VisibleLine) Before() string { return l.before }
func (l VisibleLine) Cursor() string { return l.cursor }
func (l VisibleLine) After() string  { return l.after }
func (l VisibleLine) Plain() string  { return l.plain }
func (l VisibleLine) Tokens() []Token {
	if len(l.tokens) == 0 {
		return nil
	}
	out := make([]Token, len(l.tokens))
	copy(out, l.tokens)
	return out
}

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
	return VisibleLine{
		before: before,
		cursor: char,
		after:  after,
		plain:  string(lineRunes),
		tokens: m.visibleTokens(lineStart+start, lineStart+end),
	}
}

func (m *Model) insertRunes(runes []rune) {
	if len(runes) == 0 {
		return
	}
	m.cursor = m.cursorForInsertion()
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
	m.shiftTokens(m.cursor-len(runes), len(runes))
	m.revision++
}

func (m *Model) insertTokenRunes(runes []rune) {
	if len(runes) == 0 {
		return
	}
	start := m.cursorForInsertion()
	m.cursor = start
	m.insertRunes(runes)
	m.tokens = append(m.tokens, Token{Start: start, End: start + len(runes)})
}

func (m Model) visibleTokens(start int, end int) []Token {
	if len(m.tokens) == 0 || end <= start {
		return nil
	}
	var out []Token
	for _, token := range m.tokens {
		if token.End <= start || token.Start >= end {
			continue
		}
		visible := Token{
			Start: max(start, token.Start) - start,
			End:   min(end, token.End) - start,
		}
		if visible.End > visible.Start {
			out = append(out, visible)
		}
	}
	return out
}

func (m *Model) moveCursorLeft() int {
	if m.cursor <= 0 {
		return 0
	}
	next := m.cursor - 1
	if token, ok := m.tokenContaining(next); ok {
		return token.Start
	}
	return next
}

func (m *Model) moveCursorRight() int {
	if m.cursor >= len(m.value) {
		return len(m.value)
	}
	next := m.cursor + 1
	if token, ok := m.tokenContaining(next); ok {
		return token.End
	}
	return next
}

func (m *Model) moveCursorWordLeft() int {
	if m.cursor <= 0 {
		return 0
	}
	if token, ok := m.tokenForBackspace(); ok {
		return token.Start
	}
	return ui.PrevWordBoundary(m.value, m.cursor)
}

func (m *Model) moveCursorWordRight() int {
	if m.cursor >= len(m.value) {
		return len(m.value)
	}
	if token, ok := m.tokenForDelete(); ok {
		return token.End
	}
	return ui.NextWordBoundary(m.value, m.cursor)
}

func (m *Model) normalizeCursor(cursor int) int {
	if token, ok := m.tokenContaining(cursor); ok {
		return token.End
	}
	return cursor
}

func (m *Model) cursorForInsertion() int {
	return m.normalizeCursor(m.cursor)
}

func (m *Model) tokenContaining(pos int) (Token, bool) {
	for _, token := range m.tokens {
		if pos > token.Start && pos < token.End {
			return token, true
		}
	}
	return Token{}, false
}

func (m *Model) tokenForBackspace() (Token, bool) {
	if m.cursor <= 0 {
		return Token{}, false
	}
	for _, token := range m.tokens {
		if m.cursor > token.Start && m.cursor <= token.End {
			return token, true
		}
	}
	return Token{}, false
}

func (m *Model) tokenForDelete() (Token, bool) {
	if m.cursor >= len(m.value) {
		return Token{}, false
	}
	for _, token := range m.tokens {
		if m.cursor >= token.Start && m.cursor < token.End {
			return token, true
		}
	}
	return Token{}, false
}

func (m *Model) deleteTokenForBackspace() bool {
	token, ok := m.tokenForBackspace()
	if !ok {
		return false
	}
	m.removeRange(token.Start, token.End)
	m.cursor = token.Start
	return true
}

func (m *Model) deleteTokenForDelete() bool {
	token, ok := m.tokenForDelete()
	if !ok {
		return false
	}
	m.removeRange(token.Start, token.End)
	m.cursor = token.Start
	return true
}

func (m *Model) deleteWordForBackspace() bool {
	if m.deleteTokenForBackspace() {
		return true
	}
	start := ui.PrevWordBoundary(m.value, m.cursor)
	if start >= m.cursor {
		return false
	}
	m.removeRange(start, m.cursor)
	m.cursor = start
	return true
}

func (m *Model) deleteWordForDelete() bool {
	if m.deleteTokenForDelete() {
		return true
	}
	end := ui.NextWordBoundary(m.value, m.cursor)
	if end <= m.cursor {
		return false
	}
	m.removeRange(m.cursor, end)
	return true
}

func (m *Model) removeRange(start int, end int) {
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	if end > len(m.value) {
		end = len(m.value)
	}
	if start == end {
		return
	}
	delta := end - start
	m.value = append(m.value[:start], m.value[end:]...)
	m.valueDirty = true
	m.cachedValue = ""
	m.rewriteTokensAfterRemoval(start, end, delta)
}

func (m *Model) shiftTokens(at int, delta int) {
	if delta == 0 {
		return
	}
	for i := range m.tokens {
		if m.tokens[i].Start >= at {
			m.tokens[i].Start += delta
			m.tokens[i].End += delta
		}
	}
}

func (m *Model) rewriteTokensAfterRemoval(start int, end int, delta int) {
	filtered := m.tokens[:0]
	for _, token := range m.tokens {
		switch {
		case token.End <= start:
			filtered = append(filtered, token)
		case token.Start >= end:
			token.Start -= delta
			token.End -= delta
			filtered = append(filtered, token)
		}
	}
	m.tokens = filtered
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

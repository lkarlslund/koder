package knowledge

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateMarkdownAllowsDurableDocumentation(t *testing.T) {
	t.Parallel()
	value := "# Procedure\n\nUse [the manual](https://example.invalid/docs).\n\n" +
		"`<span>example</span>`\n\n```html\n<script>alert('documented, not run')</script>\n```\n"
	if err := ValidateMarkdown("body", value); err != nil {
		t.Fatalf("ValidateMarkdown() error = %v", err)
	}
	if err := ValidateMarkdown("body", "Contact <mailto:user@example.invalid> or see <https://example.invalid>."); err != nil {
		t.Fatalf("ValidateMarkdown(autolinks) error = %v", err)
	}
	if err := ValidateMarkdown("body", "The comparison x < y > z is plain text."); err != nil {
		t.Fatalf("ValidateMarkdown(comparison) error = %v", err)
	}
}

func TestValidateMarkdownRejectsActiveAndUnboundedContent(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"raw HTML":         "<iframe src=\"https://example.invalid\"></iframe>",
		"HTML comment":     "<!-- hidden -->",
		"javascript link":  "[run](javascript:alert(1))",
		"data image":       "![pixel](data:image/gif;base64,AAAA)",
		"reference scheme": "[run]: vbscript:msgbox(1)",
		"control":          "hello\x00world",
		"invalid UTF-8":    string([]byte{0xff, 0xfe}),
		"too large":        strings.Repeat("a", MaxMarkdownBytes+1),
		"too many lines":   strings.Repeat("x\n", MaxMarkdownLines),
		"line too long":    strings.Repeat("x", MaxMarkdownLineBytes+1),
	}
	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			if err := ValidateMarkdown("body", value); !errors.Is(err, ErrInvalidRecord) {
				t.Fatalf("ValidateMarkdown() error = %v, want ErrInvalidRecord", err)
			}
		})
	}
}

func TestNormalizeMarkdownOnlyCanonicalizesLineEndings(t *testing.T) {
	t.Parallel()
	if got := NormalizeMarkdown("one\r\ntwo\rthree  "); got != "one\ntwo\nthree  " {
		t.Fatalf("NormalizeMarkdown() = %q", got)
	}
}

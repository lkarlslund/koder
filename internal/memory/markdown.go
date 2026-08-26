package memory

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	MaxMarkdownBytes      = 256 << 10
	MaxMarkdownLines      = 8192
	MaxMarkdownLineBytes  = 32 << 10
	MaxMarkdownReferences = 2048
)

var markdownDestinationPattern = regexp.MustCompile(`(?i)(?:\]\(\s*<?|^\s*\[[^]]+\]:\s*<?)([a-z][a-z0-9+.-]*):`)
var markdownReferenceDefinitionPattern = regexp.MustCompile(`^\s*\[[^]]+\]:`)

// ValidateMarkdown applies storage-time resource and active-content constraints. Browser
// sanitization remains required at rendering time; this validator does not depend on it.
func ValidateMarkdown(field, value string) error {
	if len(value) > MaxMarkdownBytes {
		return invalid(field, fmt.Sprintf("Markdown exceeds %d bytes", MaxMarkdownBytes))
	}
	if !utf8.ValidString(value) {
		return invalid(field, "Markdown must be valid UTF-8")
	}
	for _, character := range value {
		if (character < 0x20 && character != '\n' && character != '\r' && character != '\t') || character == 0x7f {
			return invalid(field, "Markdown contains a prohibited control character")
		}
	}
	lines := strings.Split(value, "\n")
	if len(lines) > MaxMarkdownLines {
		return invalid(field, fmt.Sprintf("Markdown exceeds %d lines", MaxMarkdownLines))
	}
	fenceCharacter, fenceLength := byte(0), 0
	references := 0
	for _, line := range lines {
		if len(line) > MaxMarkdownLineBytes {
			return invalid(field, fmt.Sprintf("Markdown line exceeds %d bytes", MaxMarkdownLineBytes))
		}
		if character, length, ok := markdownFence(line); ok {
			if fenceCharacter == 0 {
				fenceCharacter, fenceLength = character, length
			} else if character == fenceCharacter && length >= fenceLength {
				fenceCharacter, fenceLength = 0, 0
			}
			continue
		}
		if fenceCharacter != 0 {
			continue
		}
		visible := stripInlineCode(line)
		if containsRawHTML(visible) {
			return invalid(field, "raw HTML is not allowed in Markdown")
		}
		references += strings.Count(visible, "](")
		if markdownReferenceDefinitionPattern.MatchString(visible) {
			references++
		}
		if references > MaxMarkdownReferences {
			return invalid(field, fmt.Sprintf("Markdown exceeds %d links or references", MaxMarkdownReferences))
		}
		for _, match := range markdownDestinationPattern.FindAllStringSubmatch(visible, -1) {
			switch strings.ToLower(match[1]) {
			case "http", "https", "mailto":
			default:
				return invalid(field, "Markdown contains a prohibited link scheme")
			}
		}
	}
	return nil
}

// NormalizeMarkdown canonicalizes line endings without trimming whitespace that carries
// Markdown meaning.
func NormalizeMarkdown(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.ReplaceAll(value, "\r", "\n")
}

func markdownFence(line string) (byte, int, bool) {
	indent := 0
	for indent < len(line) && indent < 4 && line[indent] == ' ' {
		indent++
	}
	if indent > 3 || indent >= len(line) || (line[indent] != '`' && line[indent] != '~') {
		return 0, 0, false
	}
	character := line[indent]
	end := indent
	for end < len(line) && line[end] == character {
		end++
	}
	if end-indent < 3 {
		return 0, 0, false
	}
	return character, end - indent, true
}

func stripInlineCode(line string) string {
	output := []byte(line)
	openLength := 0
	for index := 0; index < len(line); {
		if line[index] != '`' {
			if openLength > 0 {
				output[index] = ' '
			}
			index++
			continue
		}
		end := index
		for end < len(line) && line[end] == '`' {
			output[end] = ' '
			end++
		}
		runLength := end - index
		if openLength == 0 {
			openLength = runLength
		} else if runLength == openLength {
			openLength = 0
		}
		index = end
	}
	return string(output)
}

func containsRawHTML(line string) bool {
	lower := strings.ToLower(line)
	if strings.Contains(lower, "<!--") || strings.Contains(lower, "<!doctype") || strings.Contains(lower, "<?xml") {
		return true
	}
	for start := strings.IndexByte(line, '<'); start >= 0; {
		rest := line[start+1:]
		end := strings.IndexByte(rest, '>')
		if end < 0 {
			return startsRawHTML(rest)
		}
		inside := rest[:end]
		trimmedInside := strings.TrimSpace(inside)
		lowerInside := strings.ToLower(trimmedInside)
		if !strings.ContainsAny(trimmedInside, " \t") &&
			(strings.HasPrefix(lowerInside, "http://") || strings.HasPrefix(lowerInside, "https://") || strings.HasPrefix(lowerInside, "mailto:")) {
			line = rest[end+1:]
			start = strings.IndexByte(line, '<')
			continue
		}
		if startsRawHTML(inside) {
			return true
		}
		line = rest[end+1:]
		start = strings.IndexByte(line, '<')
	}
	return false
}

func startsRawHTML(value string) bool {
	if value == "" {
		return false
	}
	return value[0] == '/' || value[0] == '!' || value[0] == '?' ||
		(value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')
}

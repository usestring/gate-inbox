package ui

import (
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/extension/textfmt"
)

type mdBlock uint8

const (
	mdText mdBlock = iota
	mdHeading
	mdQuote
	mdCode
	mdRule
)

type mdMark uint8

const (
	mdBold mdMark = 1 << iota
	mdItalic
	mdInline
	mdLink
	mdURL
)

type mdKey struct {
	block mdBlock
	marks mdMark
}

type mdPiece struct {
	text  string
	marks mdMark
}

var markdownStyleCache = map[mdKey]fastStyle{}

var (
	mdHeadingRe = regexp.MustCompile(`^\s{0,3}#{1,6}\s+(.*?)\s*#*\s*$`)
	mdRuleRe    = regexp.MustCompile(`^\s{0,3}(-(\s*-){2,}|\*(\s*\*){2,}|_(\s*_){2,})\s*$`)
	mdQuoteRe   = regexp.MustCompile(`^\s{0,3}>\s?(.*)$`)
	mdListRe    = regexp.MustCompile(`^(\s*)([-*+]|\d{1,9}[.)])\s+(.*)$`)
	mdFenceRe   = regexp.MustCompile("^\\s{0,3}(```+|~~~+)")
	mdLinkRe    = regexp.MustCompile(`^\[([^\]]+)\]\(([^)\s]+)\)`)
)

func markdownStyle(key mdKey) fastStyle {
	if style, ok := markdownStyleCache[key]; ok {
		return style
	}
	style := lipgloss.NewStyle().Foreground(colorText)
	switch key.block {
	case mdHeading:
		style = style.Foreground(colorAccent).Bold(true)
	case mdQuote:
		style = style.Foreground(colorDim).Italic(true)
	case mdRule:
		style = style.Foreground(colorDim)
	case mdCode:
		style = style.Foreground(colorAccent2)
	}
	if key.marks&mdBold != 0 {
		style = style.Bold(true)
		if key.block == mdText {
			style = style.Foreground(colorBright)
		}
	}
	if key.marks&mdItalic != 0 {
		style = style.Italic(true)
	}
	if key.marks&mdInline != 0 {
		style = style.Foreground(colorAccent2)
	}
	if key.marks&mdLink != 0 {
		style = style.Foreground(colorAccent)
	}
	if key.marks&mdURL != 0 {
		style = style.Foreground(colorDim)
	}
	out := newFastStyle(style)
	markdownStyleCache[key] = out
	return out
}

// markdownLines renders a message's markdown source as styled rows no wider
// than width. Every row closes its own styles, so a row can sit inside a box
// border without its colors reaching the border.
func markdownLines(text string, width int) []string {
	width = max(1, width)
	var out []string
	fence := ""
	for _, line := range strings.Split(text, "\n") {
		if marker := mdFenceRe.FindStringSubmatch(line); marker != nil {
			switch {
			case fence == "":
				fence = marker[1]
				continue
			case strings.HasPrefix(marker[1], fence) && strings.TrimSpace(line) == marker[1]:
				fence = ""
				continue
			}
		}
		if fence != "" {
			out = append(out, mdWrap(mdCode, []mdPiece{{text: line}}, "", "", width, false)...)
			continue
		}
		if mdRuleRe.MatchString(line) {
			out = append(out, markdownStyle(mdKey{block: mdRule}).Render(strings.Repeat("─", width)))
			continue
		}
		if m := mdHeadingRe.FindStringSubmatch(line); m != nil {
			out = append(out, mdWrap(mdHeading, mdInlinePieces(m[1]), "", "", width, true)...)
			continue
		}
		if m := mdQuoteRe.FindStringSubmatch(line); m != nil {
			bar := markdownStyle(mdKey{block: mdRule}).Render("│") + " "
			out = append(out, mdWrap(mdQuote, mdInlinePieces(m[1]), bar, bar, width, true)...)
			continue
		}
		if m := mdListRe.FindStringSubmatch(line); m != nil {
			bullet := m[2]
			if strings.ContainsAny(bullet, "-*+") {
				bullet = "•"
			}
			first := m[1] + bullet + " "
			if textfmt.Width(first) >= width {
				first = bullet + " "
			}
			rest := spaces(textfmt.Width(first))
			first = markdownStyle(mdKey{block: mdHeading}).Render(first)
			out = append(out, mdWrap(mdText, mdInlinePieces(m[3]), first, rest, width, true)...)
			continue
		}
		out = append(out, mdWrap(mdText, mdInlinePieces(line), "", "", width, true)...)
	}
	return out
}

func mdInlinePieces(line string) []mdPiece {
	var pieces []mdPiece
	var plain strings.Builder
	var marks mdMark
	emit := func(text string, extra mdMark) {
		if text == "" {
			return
		}
		if n := len(pieces); n > 0 && pieces[n-1].marks == marks|extra {
			pieces[n-1].text += text
			return
		}
		pieces = append(pieces, mdPiece{text: text, marks: marks | extra})
	}
	flush := func() {
		emit(plain.String(), 0)
		plain.Reset()
	}
	for i := 0; i < len(line); {
		rest := line[i:]
		switch {
		case rest[0] == '`':
			ticks := len(rest) - len(strings.TrimLeft(rest, "`"))
			fence := rest[:ticks]
			if end := strings.Index(rest[ticks:], fence); end >= 0 {
				flush()
				emit(rest[ticks:ticks+end], mdInline)
				i += ticks + end + ticks
				continue
			}
			plain.WriteString(fence)
			i += ticks
			continue
		case rest[0] == '[':
			if m := mdLinkRe.FindStringSubmatch(rest); m != nil {
				flush()
				emit(m[1], mdLink)
				if m[1] != m[2] {
					emit(" ("+m[2]+")", mdURL)
				}
				i += len(m[0])
				continue
			}
		case strings.HasPrefix(rest, "**"):
			if mdToggles(line, i, 2, marks&mdBold != 0) {
				flush()
				marks ^= mdBold
				i += 2
				continue
			}
		case rest[0] == '*':
			if mdToggles(line, i, 1, marks&mdItalic != 0) {
				flush()
				marks ^= mdItalic
				i++
				continue
			}
		}
		plain.WriteByte(line[i])
		i++
	}
	flush()
	return pieces
}

// mdToggles reports whether the n-star run at i opens or closes emphasis.
// An opener needs text right after it and a closer later on the line; a
// closer needs text right before it.
func mdToggles(line string, i, n int, open bool) bool {
	if open {
		return i > 0 && line[i-1] != ' '
	}
	after := i + n
	if after >= len(line) || line[after] == ' ' || line[after] == '*' {
		return false
	}
	return strings.Contains(line[after+1:], line[i:after])
}

func mdWrap(block mdBlock, pieces []mdPiece, first, rest string, width int, words bool) []string {
	var rows []string
	prefix := first
	room := max(1, width-textfmt.Width(prefix))
	var row []mdPiece
	used := 0
	push := func(text string, marks mdMark) {
		if n := len(row); n > 0 && row[n-1].marks == marks {
			row[n-1].text += text
		} else {
			row = append(row, mdPiece{text: text, marks: marks})
		}
		used += textfmt.Width(text)
	}
	finish := func() {
		if words && len(row) > 0 {
			last := &row[len(row)-1]
			last.text = strings.TrimRight(last.text, " ")
		}
		var b strings.Builder
		b.WriteString(prefix)
		for _, piece := range row {
			if piece.text != "" {
				b.WriteString(markdownStyle(mdKey{block: block, marks: piece.marks}).Render(piece.text))
			}
		}
		rows = append(rows, b.String())
		row, used = nil, 0
		prefix = rest
		room = max(1, width-textfmt.Width(prefix))
	}
	for _, piece := range pieces {
		for _, token := range mdTokens(piece.text, words) {
			w := textfmt.Width(token)
			if words && token[0] == ' ' {
				if used > 0 && used+w <= room {
					push(token, piece.marks)
				}
				continue
			}
			if used > 0 && used+w > room {
				finish()
			}
			for w > room {
				head := ansi.Truncate(token, room, "")
				// A wide rune cannot fit a one-cell room; taking it anyway
				// keeps the loop moving, and padRight trims the overflow.
				if head == "" {
					head = ansi.Truncate(token, 2, "")
				}
				push(head, piece.marks)
				finish()
				token = token[len(head):]
				w = textfmt.Width(token)
			}
			if token != "" {
				push(token, piece.marks)
			}
		}
	}
	if len(row) > 0 || len(rows) == 0 {
		finish()
	}
	return rows
}

// mdTokens splits text into runs of spaces and runs of everything else, or
// keeps it whole when the block is not word-wrapped.
func mdTokens(text string, words bool) []string {
	if text == "" {
		return nil
	}
	if !words {
		return []string{text}
	}
	var tokens []string
	start := 0
	for i := 1; i <= len(text); i++ {
		if i == len(text) || (text[i] == ' ') != (text[start] == ' ') {
			tokens = append(tokens, text[start:i])
			start = i
		}
	}
	return tokens
}

package dx

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const ansiReset = "\x1b[0m"

func VisibleWidth(text string) int {
	width := 0
	for index := 0; index < len(text); {
		next, tokenWidth, _ := nextToken(text, index)
		width += tokenWidth
		index = next
	}
	return width
}

func Truncate(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if VisibleWidth(text) <= width {
		return text
	}
	if width == 1 {
		return visiblePrefix(text, 1, "")
	}
	return visiblePrefix(text, width-1, ".")
}

func PadRight(text string, width int) string {
	missing := width - VisibleWidth(text)
	if missing <= 0 {
		return text
	}
	return text + strings.Repeat(" ", missing)
}

func Row(widths []int, values ...string) string {
	cells := make([]string, len(widths))
	for index, width := range widths {
		value := ""
		if index < len(values) {
			value = values[index]
		}
		cells[index] = PadRight(Truncate(value, width), width)
	}
	return strings.Join(cells, "  ")
}

func Table(headers []string, rows [][]string) string {
	widths := tableWidths(headers, rows)
	lines := []string{Row(widths, headers...)}
	for _, row := range rows {
		lines = append(lines, Row(widths, row...))
	}
	return strings.Join(lines, "\n")
}

func Progress(current, total, width int) string {
	if width < 1 {
		return ""
	}
	percent := progressPercent(current, total)
	filled := percent * width / 100
	bar := strings.Repeat("#", filled) + strings.Repeat("-", width-filled)
	progress := "[" + bar + "] " + progressLabel(percent)
	return progress
}

func tableWidths(headers []string, rows [][]string) []int {
	widths := make([]int, len(headers))
	for index, header := range headers {
		widths[index] = VisibleWidth(header)
	}
	for _, row := range rows {
		growWidths(widths, row)
	}
	return widths
}

func growWidths(widths []int, row []string) {
	for index := 0; index < len(row) && index < len(widths); index++ {
		width := VisibleWidth(row[index])
		if width > widths[index] {
			widths[index] = width
		}
	}
}

func progressPercent(current, total int) int {
	hasTotal := total > 0
	hasProgress := current > 0
	canCalculate := hasTotal && hasProgress
	if !canCalculate {
		return 0
	}
	if current >= total {
		return 100
	}
	percent := current * 100 / total
	return percent
}

func progressLabel(percent int) string {
	return strconv.Itoa(percent) + "%"
}

func visiblePrefix(text string, width int, suffix string) string {
	var result strings.Builder
	visible := 0
	hasANSI := false
	for index := 0; index < len(text) && visible < width; {
		next, tokenWidth, isANSI := nextToken(text, index)
		if tokenOverflows(tokenWidth, visible, width) {
			break
		}
		result.WriteString(text[index:next])
		hasANSI = hasANSI || isANSI
		visible += tokenWidth
		index = next
	}
	result.WriteString(suffix)
	appendANSIReset(&result, hasANSI)
	return result.String()
}

func tokenOverflows(tokenWidth, visible, width int) bool {
	hasVisibleToken := tokenWidth > 0
	exceedsWidth := visible+tokenWidth > width
	overflows := hasVisibleToken && exceedsWidth
	return overflows
}

func appendANSIReset(result *strings.Builder, hasANSI bool) {
	if hasANSI {
		result.WriteString(ansiReset)
	}
}

func nextToken(text string, index int) (int, int, bool) {
	if end, ok := ansiSequenceEnd(text, index); ok {
		return end, 0, true
	}
	r, size := utf8.DecodeRuneInString(text[index:])
	return index + size, runeWidth(r), false
}

func runeWidth(r rune) int {
	if isZeroWidthRune(r) {
		return 0
	}
	if isWideRune(r) {
		return 2
	}
	return 1
}

func isZeroWidthRune(r rune) bool {
	if isControlRune(r) {
		return true
	}
	if isMarkRune(r) {
		return true
	}
	return isJoinerOrVariationSelector(r)
}

func isControlRune(r rune) bool {
	isC0Control := r < 0x20
	isC1Control := r >= 0x7f && r < 0xa0
	isControl := isC0Control || isC1Control
	return isControl
}

func isMarkRune(r rune) bool {
	isNonspacingMark := unicode.Is(unicode.Mn, r)
	isEnclosingMark := unicode.Is(unicode.Me, r)
	isMark := isNonspacingMark || isEnclosingMark
	return isMark
}

func isJoinerOrVariationSelector(r rune) bool {
	isJoiner := r == '\u200d'
	isVariationSelector := r >= '\ufe00' && r <= '\ufe0f'
	zeroWidth := isJoiner || isVariationSelector
	return zeroWidth
}

func isWideRune(r rune) bool {
	for _, interval := range wideRuneIntervals {
		aboveStart := r >= interval.start
		belowEnd := r <= interval.end
		inInterval := aboveStart && belowEnd
		if inInterval {
			return true
		}
	}
	return false
}

type runeInterval struct {
	start rune
	end   rune
}

var wideRuneIntervals = [...]runeInterval{
	{start: 0x1100, end: 0x115f},
	{start: 0x2329, end: 0x232a},
	{start: 0x2e80, end: 0xa4cf},
	{start: 0xac00, end: 0xd7a3},
	{start: 0xf900, end: 0xfaff},
	{start: 0xfe10, end: 0xfe6f},
	{start: 0xff00, end: 0xff60},
	{start: 0xffe0, end: 0xffe6},
	{start: 0x1f300, end: 0x1faff},
	{start: 0x20000, end: 0x3fffd},
}

func ansiSequenceEnd(text string, index int) (int, bool) {
	missingPrefix := index+1 >= len(text)
	hasEscape := !missingPrefix && text[index] == '\x1b'
	hasBracket := !missingPrefix && text[index+1] == '['
	hasANSIPrefix := !missingPrefix && hasEscape && hasBracket
	if !hasANSIPrefix {
		return index, false
	}
	for cursor := index + 2; cursor < len(text); cursor++ {
		aboveControlStart := text[cursor] >= 0x40
		belowControlEnd := text[cursor] <= 0x7e
		endsSequence := aboveControlStart && belowControlEnd
		if endsSequence {
			return cursor + 1, true
		}
	}
	return index, false
}

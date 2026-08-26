package dx

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/yowainwright/diu/internal/fn"
)

const ansiReset = "\x1b[0m"

func VisibleWidth(text string) int {
	width := 0
	for index := 0; index < len(text); {
		next, visible := nextToken(text, index)
		if visible {
			width++
		}
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
	return "[" + bar + "] " + progressLabel(percent)
}

func tableWidths(headers []string, rows [][]string) []int {
	widths := fn.Map(headers, VisibleWidth)
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
	if total <= 0 || current <= 0 {
		return 0
	}
	if current >= total {
		return 100
	}
	return current * 100 / total
}

func progressLabel(percent int) string {
	return strconv.Itoa(percent) + "%"
}

func visiblePrefix(text string, width int, suffix string) string {
	var result strings.Builder
	visible := 0
	hasANSI := false
	for index := 0; index < len(text) && visible < width; {
		next, isVisible := nextToken(text, index)
		result.WriteString(text[index:next])
		hasANSI = hasANSI || !isVisible
		if isVisible {
			visible++
		}
		index = next
	}
	result.WriteString(suffix)
	if hasANSI {
		result.WriteString(ansiReset)
	}
	return result.String()
}

func nextToken(text string, index int) (int, bool) {
	if end, ok := ansiSequenceEnd(text, index); ok {
		return end, false
	}
	_, size := utf8.DecodeRuneInString(text[index:])
	return index + size, true
}

func ansiSequenceEnd(text string, index int) (int, bool) {
	if index+1 >= len(text) || text[index] != '\x1b' || text[index+1] != '[' {
		return index, false
	}
	for cursor := index + 2; cursor < len(text); cursor++ {
		if text[cursor] >= 0x40 && text[cursor] <= 0x7e {
			return cursor + 1, true
		}
	}
	return index, false
}

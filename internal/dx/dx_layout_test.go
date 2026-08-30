package dx_test

import (
	"strings"
	"testing"

	"github.com/yowainwright/diu/internal/dx"
)

func TestRowTruncatesAndAlignsColumns(t *testing.T) {
	row := dx.Row([]int{4, 3}, "abcdef", "go")
	if row != "abc.  go " {
		t.Fatalf("Row = %q, want aligned columns", row)
	}
}

func TestTruncatePreservesAnsiAndVisibleWidth(t *testing.T) {
	value := "\x1b[36mabcdef\x1b[0m"
	truncated := dx.Truncate(value, 4)
	if dx.VisibleWidth(truncated) != 4 {
		t.Fatalf("visible width = %d, want 4", dx.VisibleWidth(truncated))
	}
	preservesColor := strings.Contains(truncated, "\x1b[36m")
	hasReset := strings.HasSuffix(truncated, "\x1b[0m")
	preservesANSI := preservesColor && hasReset
	if !preservesANSI {
		t.Fatalf("Truncate lost ANSI state: %q", truncated)
	}
}

func TestVisibleWidthHandlesUnicodeTerminalWidth(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int
	}{
		{name: "ascii", text: "abc", want: 3},
		{name: "wide", text: "漢字", want: 4},
		{name: "combining", text: "e\u0301", want: 1},
		{name: "ansi wide", text: "\x1b[36m漢\x1b[0m", want: 2},
	}
	for _, test := range tests {
		if got := dx.VisibleWidth(test.text); got != test.want {
			t.Fatalf("%s width = %d, want %d", test.name, got, test.want)
		}
	}
}

func TestTruncateDoesNotSplitWideRunes(t *testing.T) {
	truncated := dx.Truncate("漢字", 3)
	if truncated != "漢." {
		t.Fatalf("Truncate = %q, want wide rune plus suffix", truncated)
	}
	if dx.VisibleWidth(truncated) != 3 {
		t.Fatalf("visible width = %d, want 3", dx.VisibleWidth(truncated))
	}
}

func TestProgressShowsZeroAndCompleteStates(t *testing.T) {
	if got := dx.Progress(0, 10, 5); got != "[-----] 0%" {
		t.Fatalf("zero progress = %q", got)
	}
	if got := dx.Progress(10, 10, 5); got != "[#####] 100%" {
		t.Fatalf("complete progress = %q", got)
	}
}

func TestTableUsesContentWidths(t *testing.T) {
	headers := []string{"tool", "uses"}
	rows := [][]string{{"npm", "12"}, {"homebrew", "2"}}
	table := dx.Table(headers, rows)
	want := "tool      uses\nnpm       12  \nhomebrew  2   "
	if table != want {
		t.Fatalf("Table = %q, want %q", table, want)
	}
}

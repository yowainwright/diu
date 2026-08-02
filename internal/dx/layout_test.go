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
	if !strings.Contains(truncated, "\x1b[36m") || !strings.HasSuffix(truncated, "\x1b[0m") {
		t.Fatalf("Truncate lost ANSI state: %q", truncated)
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
	table := dx.Table([]string{"tool", "uses"}, [][]string{{"npm", "12"}, {"homebrew", "2"}})
	want := "tool      uses\nnpm       12  \nhomebrew  2   "
	if table != want {
		t.Fatalf("Table = %q, want %q", table, want)
	}
}

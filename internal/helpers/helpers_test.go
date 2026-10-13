package helpers

import (
	"bufio"
	"context"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
	"testing/synctest"
	"time"
)

func TestMapTransformsInOrder(t *testing.T) {
	input := []int{3, 1, 3}
	var calls []int
	got := Map(input, func(value int) string {
		calls = append(calls, value)
		return strconv.Itoa(value)
	})
	assertSlice(t, got, []string{"3", "1", "3"})
	assertSlice(t, calls, input)
}

func TestMapReturnsEmptySliceForEmptyInputs(t *testing.T) {
	for name, input := range map[string][]int{"nil": nil, "empty": {}} {
		t.Run(name, func(t *testing.T) {
			got := Map(input, func(int) string {
				t.Fatal("transform called for empty input")
				return ""
			})
			assertSlice(t, got, []string{})
		})
	}
}

func TestMapDoesNotAliasInput(t *testing.T) {
	input := []int{1, 2}
	got := Map(input, func(value int) int { return value })
	got[0] = 9
	assertSlice(t, input, []int{1, 2})
}

func TestFilterPreservesMatchingOrder(t *testing.T) {
	input := []int{3, 2, 1, 3}
	var calls []int
	got := Filter(input, func(value int) bool {
		calls = append(calls, value)
		remainder := value % 2
		return remainder != 0
	})
	assertSlice(t, got, []int{3, 1, 3})
	assertSlice(t, calls, input)
}

func TestFilterReturnsNilWhenNothingMatches(t *testing.T) {
	inputs := map[string][]int{"nil": nil, "empty": {}, "no matches": {1, 3}}
	for name, input := range inputs {
		t.Run(name, func(t *testing.T) {
			got := Filter(input, func(value int) bool {
				remainder := value % 2
				return remainder == 0
			})
			assertSlice(t, got, nil)
		})
	}
}

func TestFilterDoesNotAliasInput(t *testing.T) {
	input := []int{1, 2}
	got := Filter(input, func(int) bool { return true })
	got[0] = 9
	assertSlice(t, input, []int{1, 2})
}

func TestFilterMapTransformsMatchingItemsInOrder(t *testing.T) {
	input := []string{"2", "invalid", "4", "2"}
	var calls []string
	got := FilterMap(input, func(value string) (int, bool) {
		calls = append(calls, value)
		return parseInteger(value)
	})
	assertSlice(t, got, []int{2, 4, 2})
	assertSlice(t, calls, input)
}

func TestFilterMapReturnsEmptySliceWhenNothingMatches(t *testing.T) {
	inputs := map[string][]string{"nil": nil, "empty": {}, "no matches": {"invalid"}}
	for name, input := range inputs {
		t.Run(name, func(t *testing.T) {
			assertSlice(t, FilterMap(input, parseInteger), []int{})
		})
	}
}

func TestFilterMapDoesNotAliasInput(t *testing.T) {
	input := []int{1, 2}
	got := FilterMap(input, func(value int) (int, bool) { return value, true })
	got[0] = 9
	assertSlice(t, input, []int{1, 2})
}

func TestScanLinesParsesMatchingLinesInOrder(t *testing.T) {
	reader := strings.NewReader("2\r\ninvalid\n\n4\n2")
	got, err := ScanLines(reader, parseInteger)
	assertError(t, err, nil)
	assertSlice(t, got, []int{2, 4, 2})
}

func TestScanLinesPreservesLineContent(t *testing.T) {
	reader := strings.NewReader("one\r\n\n two")
	got, err := ScanLines(reader, func(line string) (string, bool) { return line, true })
	assertError(t, err, nil)
	assertSlice(t, got, []string{"one", "", " two"})
}

func TestScanLinesReturnsNilWhenNothingMatches(t *testing.T) {
	for name, input := range map[string]string{"empty": "", "no matches": "invalid\n"} {
		t.Run(name, func(t *testing.T) {
			got, err := ScanLines(strings.NewReader(input), parseInteger)
			assertError(t, err, nil)
			assertSlice(t, got, nil)
		})
	}
}

func TestScanLinesReturnsReaderError(t *testing.T) {
	want := errors.New("read failed")
	got, err := ScanLines(iotest.ErrReader(want), parseInteger)
	assertError(t, err, want)
	assertSlice(t, got, nil)
}

func TestScanLinesPreservesResultsBeforeReaderError(t *testing.T) {
	want := errors.New("read failed")
	reader := io.MultiReader(strings.NewReader("1\n2\n"), iotest.ErrReader(want))
	got, err := ScanLines(reader, parseInteger)
	assertError(t, err, want)
	assertSlice(t, got, []int{1, 2})
}

func TestScanLinesPreservesResultsBeforeOversizedLine(t *testing.T) {
	input := "7\n" + strings.Repeat("x", bufio.MaxScanTokenSize)
	got, err := ScanLines(strings.NewReader(input), parseInteger)
	assertError(t, err, bufio.ErrTooLong)
	assertSlice(t, got, []int{7})
}

func TestSleepWaitsForDuration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := time.Now()
		assertError(t, Sleep(context.Background(), time.Hour), nil)
		assertElapsed(t, started, time.Hour)
	})
}

func TestSleepReturnsImmediatelyForNonPositiveDuration(t *testing.T) {
	for _, delay := range []time.Duration{0, -time.Millisecond} {
		t.Run(delay.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				started := time.Now()
				assertError(t, Sleep(context.Background(), delay), nil)
				assertElapsed(t, started, 0)
			})
		})
	}
}

func TestSleepReturnsExistingCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assertError(t, Sleep(ctx, time.Hour), context.Canceled)
}

func TestSleepCancelsWhileWaiting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		started := time.Now()
		result := make(chan error, 1)
		go func() { result <- Sleep(ctx, time.Hour) }()
		synctest.Wait()
		cancel()
		assertError(t, <-result, context.Canceled)
		assertElapsed(t, started, 0)
	})
}

func TestSleepStopsAtContextDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		defer cancel()
		started := time.Now()
		assertError(t, Sleep(ctx, time.Hour), context.DeadlineExceeded)
		assertElapsed(t, started, time.Millisecond)
	})
}

func TestWaitUntilRejectsNonPositiveInterval(t *testing.T) {
	for _, interval := range []time.Duration{0, -time.Millisecond} {
		t.Run(interval.String(), func(t *testing.T) {
			ready := func() bool { t.Fatal("ready called for invalid interval"); return true }
			err := WaitUntil(context.Background(), interval, ready)
			if err == nil {
				t.Fatal("expected invalid interval error")
			}
			if err.Error() != "poll interval must be positive" {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestWaitUntilReturnsExistingCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ready := func() bool { t.Fatal("ready called after cancellation"); return true }
	assertError(t, WaitUntil(ctx, time.Millisecond, ready), context.Canceled)
}

func TestWaitUntilChecksReadyImmediately(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := time.Now()
		calls := 0
		ready := func() bool { calls++; return true }
		assertError(t, WaitUntil(context.Background(), time.Hour, ready), nil)
		if calls != 1 {
			t.Fatalf("ready calls = %d, want 1", calls)
		}
		assertElapsed(t, started, 0)
	})
}

func TestWaitUntilPollsUntilReady(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
		defer cancel()
		started := time.Now()
		calls := 0
		ready := func() bool { calls++; return calls == 3 }
		assertError(t, WaitUntil(ctx, time.Millisecond, ready), nil)
		if calls != 3 {
			t.Fatalf("ready calls = %d, want 3", calls)
		}
		assertElapsed(t, started, 2*time.Millisecond)
	})
}

func TestWaitUntilStopsAtContextDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Millisecond)
		defer cancel()
		started := time.Now()
		calls := 0
		ready := func() bool { calls++; return false }
		assertError(t, WaitUntil(ctx, time.Millisecond, ready), context.DeadlineExceeded)
		if calls != 3 {
			t.Fatalf("ready calls = %d, want 3", calls)
		}
		assertElapsed(t, started, 3*time.Millisecond)
	})
}

func TestWaitUntilCancelsWhileWaiting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		started := time.Now()
		calls := 0
		ready := func() bool { calls++; return false }
		result := make(chan error, 1)
		go func() { result <- WaitUntil(ctx, time.Hour, ready) }()
		synctest.Wait()
		cancel()
		assertError(t, <-result, context.Canceled)
		if calls != 1 {
			t.Fatalf("ready calls = %d, want 1", calls)
		}
		assertElapsed(t, started, 0)
	})
}

func parseInteger(value string) (int, bool) {
	number, err := strconv.Atoi(value)
	return number, err == nil
}

func assertSlice[T comparable](t *testing.T, got, want []T) {
	t.Helper()
	valuesMatch := slices.Equal(got, want)
	nilMatches := (got == nil) == (want == nil)
	matches := valuesMatch && nilMatches
	if !matches {
		t.Fatalf("slice = %#v, want %#v", got, want)
	}
}

func assertError(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

func assertElapsed(t *testing.T, started time.Time, want time.Duration) {
	t.Helper()
	if got := time.Since(started); got != want {
		t.Fatalf("elapsed = %s, want %s", got, want)
	}
}

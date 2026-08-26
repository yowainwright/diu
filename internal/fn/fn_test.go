package fn

import "testing"

func TestMap(t *testing.T) {
	values := []int{1, 2, 3}
	results := Map(values, func(value int) string {
		return string(rune('a' + value - 1))
	})
	if len(results) != 3 || results[0] != "a" || results[2] != "c" {
		t.Fatalf("Map = %#v", results)
	}
}

func TestMapPreservesNil(t *testing.T) {
	var values []int
	if results := Map(values, func(value int) int { return value }); results != nil {
		t.Fatalf("Map nil = %#v", results)
	}
}

func TestFilter(t *testing.T) {
	values := []int{1, 2, 3, 4}
	results := Filter(values, func(value int) bool { return value%2 == 0 })
	if len(results) != 2 || results[0] != 2 || results[1] != 4 {
		t.Fatalf("Filter = %#v", results)
	}
}

func TestFilterEmptyReturnsNil(t *testing.T) {
	if results := Filter([]int{1}, func(int) bool { return false }); results != nil {
		t.Fatalf("Filter = %#v", results)
	}
}

func TestMaxValueKey(t *testing.T) {
	values := map[string]int{"a": 1, "b": 3, "c": 2}
	if key := MaxValueKey(values); key != "b" {
		t.Fatalf("MaxValueKey = %q", key)
	}
}

func TestMaxValueKeyBreaksTiesOnSmallestKey(t *testing.T) {
	values := map[string]int{"c": 3, "a": 3, "b": 1}
	if key := MaxValueKey(values); key != "a" {
		t.Fatalf("MaxValueKey = %q", key)
	}
}

func TestMaxValueKeyEmptyReturnsZero(t *testing.T) {
	if key := MaxValueKey(map[string]int{}); key != "" {
		t.Fatalf("MaxValueKey = %q", key)
	}
}

func TestSortedKeys(t *testing.T) {
	values := map[string]int{"b": 2, "a": 1, "c": 3}
	keys := SortedKeys(values)
	if len(keys) != 3 || keys[0] != "a" || keys[2] != "c" {
		t.Fatalf("SortedKeys = %#v", keys)
	}
}

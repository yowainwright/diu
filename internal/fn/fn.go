package fn

import (
	"cmp"
	"maps"
	"slices"
)

func Map[S ~[]E, E any, R any](values S, convert func(E) R) []R {
	if values == nil {
		return nil
	}
	results := make([]R, len(values))
	for index, value := range values {
		results[index] = convert(value)
	}
	return results
}

func Filter[S ~[]E, E any](values S, keep func(E) bool) S {
	var results S
	for _, value := range values {
		if keep(value) {
			results = append(results, value)
		}
	}
	return results
}

func SortedKeys[M ~map[K]V, K cmp.Ordered, V any](values M) []K {
	return slices.Sorted(maps.Keys(values))
}

func MaxValueKey[M ~map[K]V, K cmp.Ordered, V cmp.Ordered](values M) K {
	var bestKey K
	var bestValue V
	first := true
	for key, value := range values {
		larger := value > bestValue
		tieBreak := value == bestValue && key < bestKey
		if first || larger || tieBreak {
			bestKey, bestValue, first = key, value, false
		}
	}
	return bestKey
}

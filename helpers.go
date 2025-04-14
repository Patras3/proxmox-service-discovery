package main

func stringBoolMap(from ...string) map[string]bool {
	m := make(map[string]bool, len(from))
	for _, s := range from {
		m[s] = true
	}
	return m
}

// CountSlice returns the number of elements in a slice for which the provided
// predicate returns true.
func CountSlice[S ~[]E, E any](slice S, predicate func(E) bool) int {
	count := 0
	for _, item := range slice {
		if predicate(item) {
			count++
		}
	}
	return count
}

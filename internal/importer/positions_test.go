package importer

import (
	"slices"
	"testing"
)

func checkPositions(t *testing.T, numbers, want []int) {
	t.Helper()
	if got := trackPositions(numbers); !slices.Equal(got, want) {
		t.Errorf("trackPositions(%v) = %v, want %v", numbers, got, want)
	}
}

func TestTrackPositions(t *testing.T) {
	checkPositions(t, []int{1, 2, 3}, []int{1, 2, 3})
	checkPositions(t, []int{1, 4}, []int{1, 4})
	checkPositions(t, []int{2, 3}, []int{2, 3})
	checkPositions(t, []int{1, 1, 2}, []int{1, 3, 2})
	checkPositions(t, []int{1, 2, 0, 0}, []int{1, 2, 3, 4})
	checkPositions(t, []int{0, 0}, []int{1, 2})
	checkPositions(t, []int{3, 99}, []int{3, 99})
	checkPositions(t, nil, []int{})
}

func checkDeclaredCount(t *testing.T, counts []int, want int) {
	t.Helper()
	got := declaredCount(counts)
	if (got == nil) != (want == 0) || (got != nil && *got != want) {
		t.Errorf("declaredCount(%v) = %v, want %d (0 for none)", counts, got, want)
	}
}

func TestDeclaredCount(t *testing.T) {
	checkDeclaredCount(t, []int{12, 12, 12}, 12)
	checkDeclaredCount(t, []int{12, 0, 12}, 12)
	checkDeclaredCount(t, []int{0, 0}, 0)
	checkDeclaredCount(t, nil, 0)
	checkDeclaredCount(t, []int{10, 12, 12}, 12)
	checkDeclaredCount(t, []int{10, 12}, 12)
}

package ruleix

import (
	"fmt"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSearchReportsNewMatches(t *testing.T) {
	index, err := New[batchShortValue, int](batchShortSchema()).Build(
		Zip([]batchShortValue{{1, 1, true}, {2, 2, true}}, []int{11, 22}))
	require.NoError(t, err)
	for _, length := range []int{0, 1, 5, 8, 15, 16, 100, 129, 300} {
		for _, position := range []string{"none", "first", "last", "all"} {
			t.Run(fmt.Sprintf("%d/%s", length, position), func(t *testing.T) {
				conditions := make([]batchShortValue, length)
				found := make([][]int, length)
				want := make([][]int, length)
				expected := false
				for i := range conditions {
					conditions[i] = batchShortValue{999, 999, true}
					// An existing ID does not count as a match, even when it is in the index.
					found[i] = []int{11}
					want[i] = []int{11}
					if position == "all" || position == "first" && i == 0 || position == "last" && i == length-1 {
						conditions[i] = batchShortValue{2, 2, true}
						want[i] = append(want[i], 22)
						expected = true
					}
				}
				require.Equal(t, expected, index.Search(conditions, found))
				require.Equal(t, want, found, "every condition must be processed after a hit")
			})
		}
	}
	// Repeated calls append matches even when that same ID is already present.
	found := [][]int{{22}}
	require.True(t, index.Search([]batchShortValue{{2, 2, true}}, found))
	require.Equal(t, [][]int{{22, 22}}, found)
}

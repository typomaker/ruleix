package ruleix

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLocalEmptyPlanTransitions(t *testing.T) {
	type query struct{ left, right int }
	schema := All(
		Include(func(v query) (int, bool) { return v.left, true }),
		Include(func(v query) (int, bool) { return v.right, true }),
	)
	index, err := New[query, int](schema).Build(Zip([]query{{1, 2}, {2, 1}}, []int{10, 20}))
	require.NoError(t, err)
	root, ok := index.root.(*allRule[query])
	require.True(t, ok)
	for lifetime := range 2 {
		local := index.TestLocal()
		require.Nil(t, local.pool.allPlans)
		for i, value := range []query{{99, 2}, {99, 1}, {1, 2}, {99, 2}, {2, 1}, {1, 2}} {
			got, want := []int{-1}, []int{-1}
			found := local.SearchOne(value, &got)
			require.Equal(t, index.SearchOne(value, &want), found, "lifetime %d query %d", lifetime, i)
			require.Equal(t, want, got)
			if i < 2 {
				require.Nil(t, local.pool.allPlans, "misses must not create plans")
				if cached := root.loadLocalQueryResult(local.pool, value); cached != nil {
					require.Empty(t, cached.ids)
				}
			} else {
				require.NotNil(t, local.pool.allPlans[root], "a subsequent hit must still prepare its plan")
			}
		}
		local.Close()
	}
}

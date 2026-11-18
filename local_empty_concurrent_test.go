package ruleix

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLocalEmptyHintsStayIndependentAcrossConcurrentLocals(t *testing.T) {
	type query struct{ left, right int }
	schema := All(
		Include(func(v query) (int, bool) { return v.left, true }),
		Include(func(v query) (int, bool) { return v.right, true }),
	)
	index, err := New[query, int](schema).Build(Zip([]query{{1, 1}}, []int{7}))
	require.NoError(t, err)
	const workers = 8
	errors := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			for round := range 20 {
				local := index.TestLocal()
				for _, value := range []query{{99, 1}, {99, 2}, {1, 99}, {1, 1}, {99, 1}, {1, 1}} {
					got := []int{-1}
					found := local.SearchOne(value, &got)
					want := value == (query{1, 1})
					if found != want || (want && (len(got) != 2 || got[1] != 7)) || (!want && len(got) != 1) {
						errors <- fmt.Errorf("round %d query %v: found %v, IDs %v", round, value, found, got)
						local.Close()
						return
					}
				}
				local.Close()
			}
		}()
	}
	group.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
}

package ruleix

import (
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
)

type batchShortValue struct {
	a, b int
	hasA bool
}

func batchShortSchema() Rule[batchShortValue] {
	return All(Include(func(v batchShortValue) (int, bool) { return v.a, v.hasA }),
		Include(func(v batchShortValue) (int, bool) { return v.b, true }))
}

func TestShortBatchUsesProvedSourceForEveryCondition(t *testing.T) {
	rows, ids := []batchShortValue{{0, 0, true}}, []int{0}
	// A's first posting is narrow, but A=1 is too wide. Every B posting is narrow.
	for i := 1; i <= 100; i++ {
		rows = append(rows, batchShortValue{1, i, true})
		ids = append(ids, i)
	}
	index, err := New[batchShortValue, int](batchShortSchema()).Build(Zip(rows, ids))
	require.NoError(t, err)
	conditions := make([]batchShortValue, 15)
	for i := range conditions {
		conditions[i] = batchShortValue{1, i, true}
	}
	conditions[0] = rows[0]
	conditions[7].b = 999
	for _, length := range []int{5, 8, 15} {
		found := make([][]int, length)
		for i := range found {
			found[i] = []int{-1}
		}
		var work batchWork
		index.searchBatch(conditions[:length], found, batchTyped, &work)
		for i, q := range conditions[:length] {
			want := []int{-1}
			for j, row := range rows {
				if row.a == q.a && row.b == q.b {
					want = append(want, ids[j])
				}
			}
			require.Equal(t, want, found[i])
		}
		require.Zero(t, work.fallbackOffers, "the proved anchor must not enter a wide fallback")
		require.Positive(t, work.groups)
		require.Positive(t, work.peakScratchBytes)
	}
	// A sampled root must keep its observed kernel, regardless of the proof.
	var inspector Inspector
	observed, err := New[batchShortValue, int](Inspect(&inspector, batchShortSchema())).Build(Zip(rows, ids))
	require.NoError(t, err)
	observed.localTelemetry.Store(63)
	var work batchWork
	observed.searchBatch(conditions[:5], make([][]int, 5), batchTyped, &work)
	require.Equal(t, uint64(5), work.fallbackOffers)
	require.Zero(t, work.groups)
}

func TestShortBatchProofRejectsUnboundedAndWildcardSources(t *testing.T) {
	for _, wildcard := range []bool{false, true} {
		var rows []batchShortValue
		var ids []int
		for a := range 80 {
			for b := range 80 {
				rows = append(rows, batchShortValue{a, b, !wildcard})
				ids = append(ids, len(ids))
			}
		}
		index, err := New[batchShortValue, int](batchShortSchema()).Build(Zip(rows, ids))
		require.NoError(t, err)
		values := []batchShortValue{{2, 3, true}, {4, 5, true}, {6, 7, true}, {8, 9, true}, {10, 11, true}}
		var work batchWork
		found := make([][]int, 5)
		index.searchBatch(values, found, batchTyped, &work)
		require.Equal(t, uint64(5), work.fallbackOffers)
		require.Zero(t, work.groups)
		for _, result := range found {
			if wildcard {
				require.Len(t, result, 80)
			} else {
				require.Len(t, result, 1)
			}
		}
	}
}

func TestShortBatchProofIsOwnedByPublishedBuild(t *testing.T) {
	builder := New[batchShortValue, int](batchShortSchema())
	first, err := builder.Build(Zip([]batchShortValue{{1, 1, true}, {2, 2, true}}, []int{7, 8}))
	require.NoError(t, err)
	var rows []batchShortValue
	var ids []int
	for a := range 70 {
		for b := range 70 {
			rows = append(rows, batchShortValue{a, b, true})
			ids = append(ids, len(ids))
		}
	}
	second, err := builder.Build(Zip(rows, ids))
	require.NoError(t, err)
	values := []batchShortValue{{1, 1, true}, {1, 1, true}, {1, 1, true}, {2, 2, true}, {2, 2, true}}
	oldFound, newFound := make([][]int, 5), make([][]int, 5)
	first.Search(values, oldFound)
	second.Search(values, newFound)
	require.Equal(t, [][]int{{7}, {7}, {7}, {8}, {8}}, oldFound)
	for i, q := range values {
		require.Equal(t, []int{q.a*70 + q.b}, newFound[i])
	}
}

func TestShortBatchWildcardCannotProveCapacity(t *testing.T) {
	rows, ids := make([]batchShortValue, 100), make([]int, 100)
	for i := range rows {
		rows[i] = batchShortValue{i, 0, i != 0}
		ids[i] = i
	}
	index, err := New[batchShortValue, int](batchShortSchema()).Build(Zip(rows, ids))
	require.NoError(t, err)
	values := make([]batchShortValue, 5)
	for i := range values {
		values[i] = batchShortValue{200 + i, 0, true}
	}
	found := make([][]int, 5)
	var work batchWork
	index.searchBatch(values, found, batchTyped, &work)
	require.Equal(t, uint64(5), work.fallbackOffers)
	for _, matches := range found {
		require.Equal(t, []int{0}, matches)
	}
}

func TestShortBatchEmptyProofRequiresEveryOffer(t *testing.T) {
	rows, ids := []batchShortValue{{0, 0, true}}, []int{0}
	for i := 1; i <= 80; i++ {
		rows = append(rows, batchShortValue{1, i, true})
		ids = append(ids, i)
	}
	index, err := New[batchShortValue, int](batchShortSchema()).Build(Zip(rows, ids))
	require.NoError(t, err)
	modes := []string{"all-other-empty", "first-other-empty", "all-anchor-empty", "first-anchor-empty", "missing-field"}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			values := shortBatchMissingValues(mode)
			found := make([][]int, 5)
			for i := range found {
				found[i] = []int{-7}
			}
			var work batchWork
			index.searchBatch(values, found, batchTyped, &work)
			for i, q := range values {
				want := []int{-7}
				for j, row := range rows {
					if q.hasA && row.a == q.a && row.b == q.b {
						want = append(want, ids[j])
					}
				}
				require.Equal(t, want, found[i])
			}
			if mode == "first-other-empty" || mode == "first-anchor-empty" {
				require.Positive(t, work.groups)
			} else {
				require.Zero(t, work.groups)
			}
		})
	}
}

func TestShortBatchConcurrentEmptyAndMatchingCalls(t *testing.T) {
	rows, ids := make([]batchShortValue, 100), make([]int, 100)
	for i := range rows {
		rows[i] = batchShortValue{1, i, true}
		ids[i] = i
	}
	index, err := New[batchShortValue, int](batchShortSchema()).Build(Zip(rows, ids))
	require.NoError(t, err)
	errors := make(chan bool, 8)
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			ok := true
			values := make([]batchShortValue, 5)
			found := make([][]int, 5)
			for turn := range 50 {
				for i := range values {
					values[i] = batchShortValue{1, i, true}
					if turn%3 == 0 || turn%3 == 1 && i == 0 {
						values[i].a = 999
					}
					found[i] = found[i][:0]
				}
				index.Search(values, found)
				for i, q := range values {
					if q.a == 999 {
						ok = ok && len(found[i]) == 0
					} else {
						ok = ok && len(found[i]) == 1 && found[i][0] == i
					}
				}
			}
			errors <- ok
		}()
	}
	workers.Wait()
	close(errors)
	for ok := range errors {
		require.True(t, ok)
	}
}

func shortBatchMissingValues(mode string) []batchShortValue {
	values := make([]batchShortValue, 5)
	for i := range values {
		values[i] = batchShortValue{1, i + 1, true}
		switch mode {
		case "all-other-empty":
			values[i].a = 999
		case "first-other-empty":
			if i == 0 {
				values[i].a = 999
			}
		case "all-anchor-empty":
			values[i].b = 999
		case "first-anchor-empty":
			if i == 0 {
				values[i].b = 999
			}
		case "missing-field":
			values[i].hasA = false
		}
	}
	return values
}

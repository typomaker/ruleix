package ruleix

import (
	"cmp"
	"strings"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
)

type boundaryFixture struct {
	group string
	rank  int
}

func compareBoundaryFixture(a, b boundaryFixture) int {
	if compared := strings.Compare(strings.ToLower(a.group), strings.ToLower(b.group)); compared != 0 {
		return compared
	}
	return cmp.Compare(a.rank, b.rank)
}

func TestComparatorBoundaryLevelsAreNested(t *testing.T) {
	index := newOrderedIndex(compareBoundaryFixture)
	values := []boundaryFixture{{"c", 1}, {"A", 2}, {"b", 1}, {"a", 1}, {"d", 1}}
	for id, value := range values {
		index.insert(value, uint32(id))
	}
	for _, direction := range []direction{greaterThan, lessThan} {
		child := index
		for child.buildStatistics().uniqueValues > 1 {
			parent := rebuildOrderedBoundaries(&child, direction)
			for _, block := range parent.blocks {
				for _, item := range block.items {
					require.Equal(t, item.value,
						roundedOrderedBoundary(&parent, item.value, direction == lessThan))
				}
			}
			require.Less(t, parent.buildStatistics().uniqueValues, child.buildStatistics().uniqueValues)
			child = parent
		}
	}
}

func TestComparatorBoundaryLevelsHaveNoFalseNegatives(t *testing.T) {
	stored := []boundaryFixture{
		{"m", 2}, {"a", 1}, {"z", 9}, {"b", 7}, {"m", 1}, {"q", 4},
	}
	late := []boundaryFixture{{"0", -1}, {"n", 3}, {"zz", 10}}
	queries := append(append([]boundaryFixture{}, stored...),
		boundaryFixture{"A", 0}, boundaryFixture{"c", 3}, boundaryFixture{"zzzz", 20})
	for _, comparison := range []struct {
		name      string
		direction direction
		inclusive bool
	}{
		{"gt", greaterThan, false}, {"gte", greaterThan, true},
		{"lt", lessThan, false}, {"lte", lessThan, true},
	} {
		t.Run(comparison.name, func(t *testing.T) {
			get := func(value boundaryFixture) (boundaryFixture, bool) { return value, true }
			exact := &orderedRule[boundaryFixture, boundaryFixture]{
				get: get, compare: compareBoundaryFixture, dir: comparison.direction,
				inclusive: comparison.inclusive, wildcard: roaring.New(),
				index: newOrderedIndex(compareBoundaryFixture),
			}
			for id, value := range stored {
				exact.insert(value, uint32(id))
			}
			_, first, ok := exact.prepareStreamingFirstGeneration()
			require.True(t, ok)
			lossy := first.(*orderedRule[boundaryFixture, boundaryFixture])
			lossy.fitStreamingNext()
			for offset, value := range late {
				id := uint32(len(stored) + offset)
				exact.insert(value, id)
				lossy.insert(value, id)
			}
			lossy.fitStreamingNext()
			for _, query := range queries {
				for id := uint32(0); id < uint32(len(stored)+len(late)); id++ {
					if exact.matchesID(query, id) {
						require.True(t, lossy.matchesID(query, id), "query=%v id=%d", query, id)
					}
				}
			}
		})
	}
}

func TestComparatorBoundaryRoundingSupportsDescendingStrings(t *testing.T) {
	compare := func(a, b string) int { return strings.Compare(strings.ToLower(b), strings.ToLower(a)) }
	index := newOrderedIndex(compare)
	for id, value := range []string{"Alpha", "bravo", "CHARLIE", "delta"} {
		index.insert(value, uint32(id))
	}
	level := rebuildOrderedBoundaries(&index, greaterThan)
	require.Equal(t, 2, level.buildStatistics().uniqueValues)
	require.Equal(t, "bravo", roundedOrderedBoundary(&level, "beta", false))
	require.Equal(t, "aardvark", roundedOrderedBoundary(&level, "aardvark", true))
}

func TestComparatorBoundaryLookupCoversBlocksAndOpenEdges(t *testing.T) {
	empty := newOrderedIndex(cmp.Compare[int])
	require.Equal(t, 7, roundedOrderedBoundary(&empty, 7, true))

	index := newOrderedIndex(cmp.Compare[int])
	for value := 0; value < 260; value += 2 {
		index.insert(value, uint32(value))
	}
	require.Greater(t, len(index.blocks), 1)
	require.Equal(t, 128, roundedOrderedBoundary(&index, 128, true))
	require.Equal(t, 130, roundedOrderedBoundary(&index, 129, true))
	require.Equal(t, 128, roundedOrderedBoundary(&index, 129, false))
	require.Equal(t, -1, roundedOrderedBoundary(&index, -1, false))
	require.Equal(t, 300, roundedOrderedBoundary(&index, 300, true))
}

func TestComparatorBoundaryFirstGenerationNeedsTwoKeys(t *testing.T) {
	rule := &orderedRule[int, boundaryFixture]{
		compare: compareBoundaryFixture, wildcard: roaring.New(), index: newOrderedIndex(compareBoundaryFixture),
	}
	rule.index.insert(boundaryFixture{"one", 1}, 1)
	_, _, ok := rule.prepareBoundaryFirstGeneration()
	require.False(t, ok)
}

func TestComparatorBoundaryStreamingBuildRespectsHardLimit(t *testing.T) {
	const limit = uint64(12 << 10)
	constraints := make([]boundaryFixture, lossyBuildPressureInterval*2)
	ids := make([]int, len(constraints))
	for id := range constraints {
		constraints[id] = boundaryFixture{group: string(rune('a' + id%23)), rank: id}
		ids[id] = id
	}
	get := func(value boundaryFixture) (boundaryFixture, bool) { return value, true }
	exact, err := New[boundaryFixture, int](GreaterOrEqual(get, compareBoundaryFixture)).Build(Zip(constraints, ids))
	require.NoError(t, err)
	var inspector Inspector
	lossy, err := New[boundaryFixture, int](Inspect(&inspector,
		Lossy(GreaterOrEqual(get, compareBoundaryFixture), MemoryLimit(limit)))).Build(Zip(constraints, ids))
	require.NoError(t, err)
	usage, ok := inspector.Snapshot().MemoryUsage()
	require.True(t, ok)
	require.LessOrEqual(t, usage, limit)
	require.Equal(t, RuleModeLossy, inspector.Snapshot().Mode())
	for _, query := range []boundaryFixture{{"a", -1}, {"m", 4096}, {"z", len(constraints)}} {
		var want, got []int
		exact.Search(query, &want)
		lossy.Search(query, &got)
		gotSet := make(map[int]struct{}, len(got))
		for _, id := range got {
			gotSet[id] = struct{}{}
		}
		for _, id := range want {
			_, found := gotSet[id]
			require.True(t, found, "query=%v omitted ID %d", query, id)
		}
	}
}

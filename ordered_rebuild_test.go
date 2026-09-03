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
	next := rebuildOrderedBoundaries(&index, greaterThan)
	require.Equal(t, 3, next.buildStatistics().uniqueValues)
	require.Equal(t, "bravo", roundedOrderedBoundary(&next, "beta", false))
	require.Equal(t, "aardvark", roundedOrderedBoundary(&next, "aardvark", true))
}

func TestOrderedRebuildMergesOnlyTheSmallestAdjacentPostingPair(t *testing.T) {
	index := newOrderedIndex(cmp.Compare[int])
	index.insertPosting(10, roaring.BitmapOf(1, 2, 3, 4))
	index.insertPosting(20, roaring.BitmapOf(5, 6, 7, 8))
	index.insertPosting(30, roaring.BitmapOf(9))
	index.insertPosting(40, roaring.BitmapOf(10))

	next := rebuildOrderedBoundaries(&index, greaterThan)

	require.Equal(t, 3, next.buildStatistics().uniqueValues)
	require.Equal(t, []uint32{9, 10}, next.exact(30).ToArray())
	require.Equal(t, []uint32{1, 2, 3, 4}, next.exact(10).ToArray())
	require.Equal(t, []uint32{5, 6, 7, 8}, next.exact(20).ToArray())
}

func TestBestOrderedMergeDoesNotAllocateAndCrossesBlocks(t *testing.T) {
	item := func(value int, ids ...uint32) *orderedItem[int] {
		return &orderedItem[int]{value: value, bits: roaring.BitmapOf(ids...)}
	}
	index := newOrderedIndex(cmp.Compare[int])
	index.blocks = []orderedBlock[int]{
		{items: []*orderedItem[int]{item(0, 0, 10, 20), item(1, 99)}},
		{items: []*orderedItem[int]{item(2, 99), item(3, 3, 13, 23)}},
	}

	selected, ok := bestOrderedMerge(&index, greaterThan)
	require.True(t, ok)
	require.Equal(t, 1, selected.position)
	require.Zero(t, testing.AllocsPerRun(100, func() {
		_, _ = bestOrderedMerge(&index, greaterThan)
	}))
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

func TestComparatorLateExtremeJoinsTheNextWholeGeneration(t *testing.T) {
	for _, tc := range []struct {
		name     string
		dir      direction
		late     int
		upward   bool
		expected int
	}{
		{name: "lower", dir: greaterThan, late: -10, expected: -10},
		{name: "upper", dir: lessThan, late: 10, upward: true, expected: 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exact := newOrderedIndex(cmp.Compare[int])
			for id, value := range []int{0, 2, 4, 6} {
				exact.insert(value, uint32(id))
			}
			current := rebuildOrderedBoundaries(&exact, tc.dir)
			require.Equal(t, tc.late, roundedOrderedBoundary(&current, tc.late, tc.upward))
			current.insert(tc.late, 99)
			require.NotNil(t, current.exact(tc.late), "the open edge must be a physical boundary")

			next := rebuildOrderedBoundaries(&current, tc.dir)
			require.Equal(t, tc.expected, roundedOrderedBoundary(&next, tc.late, tc.upward))
			preserved := false
			for _, block := range next.blocks {
				for _, item := range block.items {
					preserved = preserved || item.bits.Contains(99)
				}
			}
			require.True(t, preserved, "the next rebuild must preserve the late ID")
		})
	}
}

func TestComparatorBoundaryFirstGenerationNeedsTwoKeys(t *testing.T) {
	rule := &orderedRule[int, boundaryFixture]{
		compare: compareBoundaryFixture, wildcard: roaring.New(), index: newOrderedIndex(compareBoundaryFixture),
	}
	rule.index.insert(boundaryFixture{"one", 1}, 1)
	_, _, ok := rule.prepareStreamingFirstGeneration()
	require.False(t, ok)
}

func TestOrderedNeighborRebuildKeepsLowEdgeMatches(t *testing.T) {
	constraints, _ := lossyDifferentialData()
	rule := &orderedRule[lossyDifferentialConstraint, int]{
		get: func(value lossyDifferentialConstraint) (int, bool) { return value.value, value.valuePresent }, compare: cmp.Compare[int],
		dir: lessThan, wildcard: roaring.New(), index: newOrderedIndex(cmp.Compare[int]),
	}
	adaptive := &streamingAdaptiveLeaf[lossyDifferentialConstraint]{child: rule}
	for id := range 4096 {
		adaptive.insert(constraints[id], uint32(id))
	}
	fitStreamingAggregate[lossyDifferentialConstraint](adaptive, lossyBuildTarget(16<<10))
	for id := 4096; id < 5632; id++ {
		adaptive.insert(constraints[id], uint32(id))
	}
	fitStreamingAggregateHard[lossyDifferentialConstraint](adaptive, 16<<10)
	rule = adaptive.child.(*orderedRule[lossyDifferentialConstraint, int])
	rule.prepareSearch()
	rule.internBitmaps(newBitmapInterner())
	require.True(t, rule.matchesID(lossyDifferentialConstraint{value: -13001, valuePresent: true}, 1))
}

func TestStreamingOrderedNeighborRebuildKeepsLowEdgeMatches(t *testing.T) {
	constraints, ids := lossyDifferentialData()
	get := func(value lossyDifferentialConstraint) (int, bool) { return value.value, value.valuePresent }
	index, _, err := buildIndexPhysicalAliases(
		Lossy(Less(get, cmp.Compare[int]), MemoryLimit(16<<10)), Zip(constraints, ids),
		false, nil, buildOptions{compilePhysicalAliases: true, enableStreaming: true},
	)
	require.NoError(t, err)
	var matches []int
	index.Search(lossyDifferentialConstraint{value: -13001, valuePresent: true}, &matches)
	require.Contains(t, matches, 1)
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

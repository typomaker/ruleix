package ruleix

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCapacityHint(t *testing.T) {
	require.Equal(t, 0, capacityHint(0))
	require.Equal(t, 2, capacityHint(1))
	require.Equal(t, 105, capacityHint(100))
	require.Equal(t, int(^uint(0)>>1), capacityHint(int(^uint(0)>>1)))
}

func TestBuilderCapacitiesFollowLastSuccessfulBuild(t *testing.T) {
	compare := func(a, b int) int { return a - b }
	schema := All(
		Include(func(v statisticsConstraint) *string { return v.name }),
		GreaterOrEqual(func(v statisticsConstraint) *int { return v.minimum }, compare),
		Between(
			func(v statisticsConstraint) *int { return v.from },
			func(v statisticsConstraint) *int { return v.until },
			compare,
		),
	)
	builder := New[statisticsConstraint, string](schema)
	build := func(size int) *Index[statisticsConstraint, string] {
		t.Helper()
		index, err := builder.Build(func(yield func(statisticsConstraint, string) bool) {
			for i := range size {
				name := fmt.Sprintf("name-%d", i)
				value := i
				if !yield(statisticsConstraint{name: &name, minimum: &value, from: &value, until: &value}, name) {
					return
				}
			}
		})
		require.NoError(t, err)
		return index
	}

	build(300)
	smallAfterLarge := build(1)
	require.Equal(t, 315, cap(smallAfterLarge.values))
	root := smallAfterLarge.root.(*allRule[statisticsConstraint])
	ordered := root.children[1].(*orderedRule[statisticsConstraint, int])
	require.Equal(t, orderedBlockSize*2, ordered.index.firstBlockCapacity)
	between := root.children[2].(*betweenRule[statisticsConstraint, int])
	require.Equal(t, 315, cap(between.minimumFrom))
	require.Equal(t, 315, cap(between.maximumUntil))

	smallAfterSmall := build(1)
	require.Equal(t, 2, cap(smallAfterSmall.values))
	root = smallAfterSmall.root.(*allRule[statisticsConstraint])
	ordered = root.children[1].(*orderedRule[statisticsConstraint, int])
	require.Equal(t, 2, ordered.index.firstBlockCapacity)
	between = root.children[2].(*betweenRule[statisticsConstraint, int])
	require.Equal(t, 2, cap(between.minimumFrom))
	require.Equal(t, 2, cap(between.maximumUntil))
}

type statisticsConstraint struct {
	name     *string
	minimum  *int
	from     *int
	until    *int
	operator string
	compared *int
}

type invalidStatisticsConstraint struct {
	value    int
	operator string
}

func statisticsPtr[T any](value T) *T { return &value }

func TestBuildCollectsCompactPerNodeStatistics(t *testing.T) {
	compare := func(a, b int) int { return a - b }
	schema := All(
		Include(func(v statisticsConstraint) *string { return v.name }),
		Exclude(func(v statisticsConstraint) *string { return v.name }),
		GreaterOrEqual(func(v statisticsConstraint) *int { return v.minimum }, compare),
		Between(
			func(v statisticsConstraint) *int { return v.from },
			func(v statisticsConstraint) *int { return v.until },
			compare,
		),
		CompareBy(
			func(v statisticsConstraint) string { return v.operator },
			func(v statisticsConstraint) *int { return v.compared },
			compare,
		),
	)

	entries := func(yield func(statisticsConstraint, string) bool) {
		values := []struct {
			constraint statisticsConstraint
			id         string
		}{
			{statisticsConstraint{statisticsPtr("a"), statisticsPtr(1), statisticsPtr(10), statisticsPtr(20), "=", statisticsPtr(100)}, "first"},
			{statisticsConstraint{statisticsPtr("b"), statisticsPtr(2), statisticsPtr(11), statisticsPtr(21), "<", statisticsPtr(101)}, "second"},
			{statisticsConstraint{statisticsPtr("a"), statisticsPtr(2), statisticsPtr(10), statisticsPtr(22), "<=", statisticsPtr(102)}, "first"},
			{statisticsConstraint{nil, nil, nil, statisticsPtr(22), ">", statisticsPtr(103)}, "third"},
			{statisticsConstraint{nil, statisticsPtr(3), statisticsPtr(12), nil, ">=", statisticsPtr(104)}, "fourth"},
		}
		for _, entry := range values {
			if !yield(entry.constraint, entry.id) {
				return
			}
		}
	}

	_, statistics, err := buildIndex[statisticsConstraint, string](schema, entries, true, nil)
	require.NoError(t, err)
	require.Equal(t, 5, statistics.entries)
	require.Equal(t, 4, statistics.uniqueIDs)
	require.Len(t, statistics.nodes, 5, "structural All nodes must not receive statistics slots")

	require.Equal(t, 2, statistics.nodes[0].equalityValues)
	require.Equal(t, 2, statistics.nodes[1].equalityValues)
	require.Equal(t, orderedBuildStatistics{uniqueValues: 3, blocks: 1}, statistics.nodes[2].ordered)
	require.Equal(t, 4, statistics.nodes[3].betweenIDs)
	require.Equal(t, [2]orderedBuildStatistics{
		{uniqueValues: 3, blocks: 1},
		{uniqueValues: 3, blocks: 1},
	}, statistics.nodes[3].between)
	require.Equal(t, [5]orderedBuildStatistics{
		{uniqueValues: 1, blocks: 1},
		{uniqueValues: 1, blocks: 1},
		{uniqueValues: 1, blocks: 1},
		{uniqueValues: 1, blocks: 1},
		{uniqueValues: 1, blocks: 1},
	}, statistics.nodes[4].compareBy)
}

func TestFailedBuildDoesNotReturnPartialStatistics(t *testing.T) {
	schema := CompareBy(
		func(v invalidStatisticsConstraint) string { return v.operator },
		func(v invalidStatisticsConstraint) *int { return &v.value },
		func(a, b int) int { return a - b },
	)
	entries := func(yield func(invalidStatisticsConstraint, int) bool) {
		yield(invalidStatisticsConstraint{value: 1, operator: "="}, 1)
		yield(invalidStatisticsConstraint{value: 2, operator: "invalid"}, 2)
	}

	index, statistics, err := buildIndex[invalidStatisticsConstraint, int](schema, entries, true, nil)
	require.Nil(t, index)
	require.Error(t, err)
	require.Equal(t, buildStatistics{}, statistics)
}

func TestBuilderPublishesStatisticsTransactionally(t *testing.T) {
	schema := CompareBy(
		func(value invalidStatisticsConstraint) string { return value.operator },
		func(value invalidStatisticsConstraint) *int { return &value.value },
		func(a, b int) int { return a - b },
	)
	builder := New[invalidStatisticsConstraint, string](schema)

	firstEntries := func(yield func(invalidStatisticsConstraint, string) bool) {
		yield(invalidStatisticsConstraint{value: 1, operator: "="}, "repeated")
		yield(invalidStatisticsConstraint{value: 2, operator: "="}, "repeated")
		yield(invalidStatisticsConstraint{value: 3, operator: "<="}, "unique")
	}
	_, err := builder.Build(firstEntries)
	require.NoError(t, err)
	require.Equal(t, buildStatistics{
		entries:   3,
		uniqueIDs: 2,
		nodes: []nodeBuildStatistics{{compareBy: [5]orderedBuildStatistics{
			{uniqueValues: 2, blocks: 1},
			{},
			{uniqueValues: 1, blocks: 1},
		}}},
	}, builder.hints)

	previous := builder.hints
	invalidEntries := func(yield func(invalidStatisticsConstraint, string) bool) {
		yield(invalidStatisticsConstraint{value: 10, operator: "="}, "one")
		yield(invalidStatisticsConstraint{value: 20, operator: "<"}, "two")
		yield(invalidStatisticsConstraint{value: 30, operator: "invalid"}, "three")
	}

	_, err = builder.Build(invalidEntries)
	require.Error(t, err)
	require.Equal(t, previous, builder.hints)

	finalEntries := func(yield func(invalidStatisticsConstraint, string) bool) {
		yield(invalidStatisticsConstraint{value: 40, operator: ">="}, "final")
	}
	_, err = builder.Build(finalEntries)
	require.NoError(t, err)
	require.Equal(t, 1, builder.hints.entries)
	require.Equal(t, 1, builder.hints.uniqueIDs)
	require.Equal(t, [5]orderedBuildStatistics{
		{}, {}, {}, {}, {uniqueValues: 1, blocks: 1},
	}, builder.hints.nodes[0].compareBy)
}

package ruleix

import (
	"testing"

	"github.com/stretchr/testify/require"
)

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

	_, statistics, err := buildIndex[statisticsConstraint, string](schema, entries, true)
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

	index, statistics, err := buildIndex[invalidStatisticsConstraint, int](schema, entries, true)
	require.Nil(t, index)
	require.Error(t, err)
	require.Equal(t, buildStatistics{}, statistics)
}

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
	compared *int
	operator *Operator
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
			func(v statisticsConstraint) *int { return v.compared },
			func(v statisticsConstraint) *Operator { return v.operator },
			compare,
		),
	)

	entries := func(yield func(statisticsConstraint, string) bool) {
		values := []struct {
			constraint statisticsConstraint
			id         string
		}{
			{
				statisticsConstraint{
					statisticsPtr("a"), statisticsPtr(1), statisticsPtr(10),
					statisticsPtr(20), statisticsPtr(100), statisticsPtr(OperatorEQ),
				},
				"first",
			},
			{
				statisticsConstraint{
					statisticsPtr("b"), statisticsPtr(2), statisticsPtr(11),
					statisticsPtr(21), statisticsPtr(101), statisticsPtr(OperatorLT),
				},
				"second",
			},
			{
				statisticsConstraint{
					statisticsPtr("a"), statisticsPtr(2), statisticsPtr(10),
					statisticsPtr(22), statisticsPtr(102), statisticsPtr(OperatorLTE),
				},
				"first",
			},
			{
				statisticsConstraint{
					nil, nil, nil, statisticsPtr(22), statisticsPtr(103), statisticsPtr(OperatorGT),
				},
				"third",
			},
			{
				statisticsConstraint{
					nil, statisticsPtr(3), statisticsPtr(12),
					nil, statisticsPtr(104), statisticsPtr(OperatorGTE),
				},
				"fourth",
			},
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
	require.Equal(t, [5]orderedBuildStatistics{{1, 1}, {1, 1}, {1, 1}, {1, 1}, {1, 1}}, statistics.nodes[4].compareBy)
}

func TestCompareByCreatesIndexesOnlyForUsedOperators(t *testing.T) {
	compare := func(a, b int) int { return a - b }
	builder := New[statisticsConstraint, string](CompareBy(
		func(v statisticsConstraint) *int { return v.compared },
		func(v statisticsConstraint) *Operator { return v.operator },
		compare,
	))
	build := func(operators ...Operator) *Index[statisticsConstraint, string] {
		t.Helper()
		index, err := builder.Build(func(yield func(statisticsConstraint, string) bool) {
			for i, operator := range operators {
				value := i
				if !yield(statisticsConstraint{compared: &value, operator: &operator}, fmt.Sprint(i)) {
					return
				}
			}
		})
		require.NoError(t, err)
		return index
	}

	build(OperatorEQ, OperatorLT, OperatorLTE, OperatorGT, OperatorGTE)
	index := build(OperatorGTE)
	rule := index.root.(*compareByRule[statisticsConstraint, int])
	for operator, ordered := range rule.indexes {
		if Operator(operator) == OperatorGTE {
			require.NotNil(t, ordered)
		} else {
			require.Nil(t, ordered)
		}
	}
}

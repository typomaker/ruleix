package ruleix

import (
	"cmp"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
)

type finalizedOrderedFixture struct {
	value    int
	operator Operator
}

func finalizedOrderedValue(value finalizedOrderedFixture) (int, bool) { return value.value, true }

func TestBuildRemovesOrderedPrecisionControllerState(t *testing.T) {
	constraints := make([]finalizedOrderedFixture, 128)
	ids := make([]int, len(constraints))
	for i := range constraints {
		constraints[i].value = i
		ids[i] = i
	}

	exact, err := New[finalizedOrderedFixture, int](
		GreaterOrEqual(finalizedOrderedValue, cmp.Compare[int]),
	).Build(Zip(constraints, ids))
	require.NoError(t, err)
	require.Nil(t, exact.root.(*orderedRule[finalizedOrderedFixture, int]).build)

	approximate, err := New[finalizedOrderedFixture, int](Lossy(
		GreaterOrEqual(finalizedOrderedValue, cmp.Compare[int]), MemoryLimit(1024),
	)).Build(Zip(constraints, ids))
	require.NoError(t, err)
	require.Nil(t, approximate.root.(*orderedRule[finalizedOrderedFixture, int]).build)
}

func TestBuildFinalizesBetweenAndCompareByPrecisionState(t *testing.T) {
	constraints := make([]finalizedOrderedFixture, 128)
	ids := make([]int, len(constraints))
	for i := range constraints {
		constraints[i] = finalizedOrderedFixture{value: i, operator: OperatorEQ}
		ids[i] = i
	}

	betweenIndex, err := New[finalizedOrderedFixture, int](Lossy(
		Between(finalizedOrderedValue, finalizedOrderedValue, cmp.Compare[int]), MemoryLimit(2048),
	)).Build(Zip(constraints, ids))
	require.NoError(t, err)
	between := betweenIndex.root.(*betweenRule[finalizedOrderedFixture, int])
	require.Nil(t, between.from.build)
	require.Nil(t, between.until.build)

	compareIndex, err := New[finalizedOrderedFixture, int](Lossy(CompareBy(
		finalizedOrderedValue,
		func(value finalizedOrderedFixture) (Operator, bool) { return value.operator, true },
		cmp.Compare[int],
	), MemoryLimit(1024))).Build(Zip(constraints, ids))
	require.NoError(t, err)
	comparison := compareIndex.root.(*compareByRule[finalizedOrderedFixture, int])
	require.Nil(t, comparison.build)
	require.NotNil(t, comparison.equalityLookup)

	var matches []int
	compareIndex.Search(finalizedOrderedFixture{value: 63}, &matches)
	require.Contains(t, matches, 63)
}

func TestBuildHelpersDefaultToExactWithoutTemporaryState(t *testing.T) {
	require.Nil(t, cloneOrderedRuleBuildState(nil))
	index := newOrderedIndex(cmp.Compare[int])
	index.insertPosting(7, roaring.BitmapOf(3))
	rule := &compareByRule[finalizedOrderedFixture, int]{}
	rule.indexes[OperatorEQ] = &index
	require.Same(t, index.exact(7), rule.equalityBits(7))
}

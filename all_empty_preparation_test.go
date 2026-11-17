package ruleix

import (
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
)

func TestPreparedEmptyKeyEligibility(t *testing.T) {
	rule := &eqRule[int, int, int]{
		get:      func(v int) (int, bool) { return v, v >= 0 },
		wildcard: roaring.New(), values: newEqualityIndex[int](0),
		encode: func(v int, _ equalityQuantizer) int { return v },
	}
	rule.values.add(0, 1)
	require.True(t, rule.supportsEmptyKeyCheck())
	require.False(t, rule.isEmptyKey(0))
	require.True(t, rule.isEmptyKey(99))
	require.True(t, rule.isEmptyKey(-1))
	rule.wildcard.Add(2)
	require.False(t, rule.supportsEmptyKeyCheck())
	rule.wildcard.Clear()
	rule.coarsen = func(v int, _ equalityQuantizer) int { return v }
	require.False(t, rule.supportsEmptyKeyCheck())
}

func TestPreparedEmptyOrderAndEligibilityReset(t *testing.T) {
	exact := &eqRule[int, int, int]{wildcard: roaring.New()}
	wildcard := &eqRule[int, int, int]{wildcard: roaring.BitmapOf(1)}
	root := &allRule[int]{children: []Rule[int]{
		&checkerOnlyRule{child: &countingRule{}}, exact, wildcard, &countingRule{},
	}}
	root.prepareEmptyOrder()
	root.prepareEmptyKeys()
	require.Equal(t, uint32(0x1432), root.emptyOrder)
	require.Equal(t, uint8(2), root.emptyKeys)
	root.children = append(root.children, root.children...)
	root.children = append(root.children, exact)
	root.prepareEmptyOrder()
	root.prepareEmptyKeys()
	require.Zero(t, root.emptyOrder)
	require.Zero(t, root.emptyKeys)
}

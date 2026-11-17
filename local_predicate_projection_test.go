package ruleix

import (
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
)

func TestProjectedOperandMatchesDirectAndRestoresIntersectionScratch(t *testing.T) {
	get := func(v int) (int, bool) { return v, v >= 0 }
	source := Include(get).newState(&nodeIDAllocator{}, &buildStatistics{}).(*eqRule[int, int, int])
	for id := range uint32(70) {
		source.values.add(int(id%4), id)
	}
	source.wildcard.Add(1)
	ids := []uint32{0, 1, 4, 7, 8, 15, 31, 68}
	state := &localAllIntersection{}
	for _, step := range []struct {
		value  int
		active uint64
	}{
		{0, 0x03}, {0, 0xf0}, {0, 0xa5}, {3, 0x06}, {1, 0xff}, {3, 0xff}, {-1, 0x0f}, {-1, 0xff}, {-1, 0xf0},
	} {
		want := source.applyCandidateMask(step.value, ids, step.active)
		got := source.applyPackedEqualityMask(step.value, ids, step.active, 0, state)
		require.Equal(t, want, got)
	}
	require.True(t, state.projecting)
	first := rankedBitmap{bits: roaring.BitmapOf(1, 2, 3, 4, 5, 6, 7, 8, 9, 10), card: 10}
	other := []rankedBitmap{{bits: roaring.BitmapOf(1, 4, 8), card: 3}}
	require.Nil(t, state.candidates(first, other))
	require.False(t, state.projecting)
	require.Equal(t, []uint32{1, 4, 8}, state.candidates(first, other))
}

package ruleix

import (
	"math"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
)

func TestRebuildPostingGenerationMergesWithoutMutatingOld(t *testing.T) {
	old := postingGeneration[uint64]{
		2: roaring.BitmapOf(2, 4),
		3: roaring.BitmapOf(3, 4),
		8: roaring.BitmapOf(8),
	}
	next, usage, ok := rebuildPostingGeneration(old, 2, 24, func(key uint64) uint64 {
		return key / 4
	})
	require.True(t, ok)
	require.Equal(t, []uint32{2, 3, 4}, next[0].ToArray())
	require.Equal(t, []uint32{8}, next[2].ToArray())
	require.Equal(t, uint64(48)+bitmapBytes(next[0])+bitmapBytes(next[2]), usage)

	next[0].Add(99)
	require.Equal(t, []uint32{2, 4}, old[2].ToArray())
	require.Equal(t, []uint32{3, 4}, old[3].ToArray())
}

func TestRebuildPostingGenerationRejectsAccountingOverflow(t *testing.T) {
	old := postingGeneration[uint64]{1: roaring.BitmapOf(1)}
	next, usage, ok := rebuildPostingGeneration(old, 1, math.MaxUint64, func(key uint64) uint64 {
		return key
	})
	require.False(t, ok)
	require.Nil(t, next)
	require.Zero(t, usage)
	require.Equal(t, []uint32{1}, old[1].ToArray())
}

func TestRebuildPostingGenerationRejectsInvalidCapacity(t *testing.T) {
	next, usage, ok := rebuildPostingGeneration(postingGeneration[uint64](nil), -1, 0, func(key uint64) uint64 { return key })
	require.False(t, ok)
	require.Nil(t, next)
	require.Zero(t, usage)
}

func TestLossyBuildStateUsesCurrentLevelForInsertAndSearch(t *testing.T) {
	state := newLossyBuildState(quantizationLevels[int]{steps: []func(int) int{
		func(key int) int { return key / 4 * 4 },
	}}, 24)
	require.True(t, state.insert(5, 1))
	require.Equal(t, 5, state.searchKey(5))

	accounting, ok := state.rebuildNext()
	require.True(t, ok)
	require.Equal(t, uint32(1), state.level)
	require.Equal(t, 4, state.searchKey(5))
	require.Equal(t, accounting.retainedBytes, state.retainedBytes)
	require.Equal(t, accounting.retainedBytes, accounting.transientBytes)

	require.True(t, state.insert(7, 2))
	require.Len(t, state.generation, 1)
	require.Equal(t, []uint32{1, 2}, state.generation[4].ToArray())
}

func TestLossyBuildStateRepeatedRebuildsPreserveIDsAndReleaseOldGeneration(t *testing.T) {
	state := newLossyBuildState(quantizationLevels[int]{steps: []func(int) int{
		func(key int) int { return key / 2 * 2 },
		func(key int) int { return key / 4 * 4 },
	}}, 24)
	for id, key := range []int{1, 2, 3, 7} {
		require.True(t, state.insert(key, uint32(id)))
	}
	first := state.generation
	_, ok := state.rebuildNext()
	require.True(t, ok)
	require.Empty(t, first, "the state must release every old posting reference")
	second := state.generation
	_, ok = state.rebuildNext()
	require.True(t, ok)
	require.Empty(t, second, "the state must release every old posting reference")

	all := roaring.New()
	for _, posting := range state.generation {
		all.Or(posting)
	}
	require.Equal(t, []uint32{0, 1, 2, 3}, all.ToArray())
	require.Len(t, state.generation, 2)
	_, ok = state.rebuildNext()
	require.False(t, ok)
}

func TestLossyBuildStateFailedRebuildKeepsPublishedGeneration(t *testing.T) {
	state := newLossyBuildState(quantizationLevels[int]{steps: []func(int) int{
		func(key int) int { return key },
	}}, math.MaxUint64)
	state.generation[1] = roaring.BitmapOf(9)
	current := state.generation

	accounting, ok := state.rebuildNext()
	require.False(t, ok)
	require.Zero(t, accounting)
	require.Same(t, current[1], state.generation[1])
	require.Equal(t, uint32(0), state.level)
	require.Equal(t, []uint32{9}, current[1].ToArray())
}

func TestLossyBuildStateRejectedInsertDoesNotMutateCurrentPosting(t *testing.T) {
	state := newLossyBuildState(quantizationLevels[int]{}, math.MaxUint64)
	require.False(t, state.insert(1, 7))
	require.Empty(t, state.generation)
	require.Zero(t, state.retainedBytes)
}

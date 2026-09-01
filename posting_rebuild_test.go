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

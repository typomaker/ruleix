package ruleix

import (
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
)

func TestQuantizationLevelZeroIsIdentityForEveryKey(t *testing.T) {
	levels := quantizationLevels[string]{steps: []func(string) string{
		func(string) string { return "coarse" },
	}}
	for _, key := range []string{"", "alpha", "coarse", "\x00binary"} {
		require.Equal(t, key, levels.key(key, 0))
	}
}

func TestQuantizationLevelsAreNestedAndMonotone(t *testing.T) {
	levels := quantizationLevels[int]{steps: []func(int) int{
		func(key int) int { return key / 2 * 2 },
		func(key int) int { return key / 4 * 4 },
		func(int) int { return 0 },
	}}

	previousDistinct := 9
	for level := uint32(0); level <= levels.terminalLevel(); level++ {
		seen := make(map[int]struct{})
		for exact := 0; exact < 9; exact++ {
			direct := levels.key(exact, level)
			seen[direct] = struct{}{}
			if level == levels.terminalLevel() {
				continue
			}
			next, ok := levels.next(direct, level)
			require.True(t, ok)
			require.Equal(t, levels.key(exact, level+1), next)
		}
		require.LessOrEqual(t, len(seen), previousDistinct)
		previousDistinct = len(seen)
	}
	terminal, ok := levels.next(7, levels.terminalLevel())
	require.False(t, ok)
	require.Equal(t, 7, terminal)
}

func TestIdentityAndQuantizedKeysUseCommonPhysicalIndex(t *testing.T) {
	levels := quantizationLevels[int]{steps: []func(int) int{
		func(key int) int { return key / 3 * 3 },
	}}
	build := func(level uint32) equalityIndex[int] {
		index := newEqualityIndex[int](4)
		for id, exact := range []int{1, 2, 3, 4} {
			index.add(levels.key(exact, level), uint32(id))
		}
		return index
	}
	search := func(index *equalityIndex[int], level uint32, exact int) []uint32 {
		posting := index.get(levels.key(exact, level))
		if posting == nil {
			return nil
		}
		bits := roaring.New()
		posting.addTo(bits)
		return bits.ToArray()
	}

	exact, lossy := build(0), build(1)
	require.Equal(t, []uint32{0}, search(&exact, 0, 1))
	require.Equal(t, []uint32{0, 1}, search(&lossy, 1, 1))
	require.IsType(t, equalityIndex[int]{}, exact)
	require.IsType(t, exact, lossy)
}

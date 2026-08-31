package ruleix

import (
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
)

func TestEqualitySetTransitions(t *testing.T) {
	set := newEqualitySet(1)
	require.Equal(t, uint64(1), set.cardinality())
	require.Nil(t, set.small)
	require.Nil(t, set.bits)

	for id := uint32(2); id <= equalityArrayLimit; id++ {
		set.add(id)
	}
	require.Len(t, set.small, equalityArrayLimit)
	require.Nil(t, set.bits)
	require.Equal(t, uint64(equalityArrayLimit), set.cardinality())

	set.add(equalityArrayLimit + 1)
	require.Nil(t, set.small)
	require.NotNil(t, set.bits)
	require.Equal(t, uint64(equalityArrayLimit+1), set.cardinality())

	dst := roaring.New()
	set.addTo(dst)
	require.Equal(t, equalityArrayLimit+1, int(dst.GetCardinality()))
}

func TestEqualitySetAddToForCompactRepresentations(t *testing.T) {
	for _, ids := range [][]uint32{{7}, {1, 3, 9}} {
		set := newEqualitySet(ids[0])
		for _, id := range ids[1:] {
			set.add(id)
		}
		dst := roaring.New()
		set.addTo(dst)
		require.Equal(t, ids, dst.ToArray())
	}
}

func TestEqualityIndexPromotesInlineValuesToMap(t *testing.T) {
	index := newEqualityIndex[int](4)
	index.add(10, 1)
	index.add(20, 2)
	index.add(30, 3)
	require.Nil(t, index.offsets)

	index.add(40, 4)
	require.NotNil(t, index.offsets)
	for value, id := range map[int]uint32{10: 1, 20: 2, 30: 3, 40: 4} {
		require.True(t, index.get(value).contains(id))
	}
	require.Nil(t, index.get(50))
}

func BenchmarkEqualityPostingSearch(b *testing.B) {
	adaptive := newEqualitySet(42)
	legacy := roaring.New()
	legacy.Add(42)
	for id := uint32(142); id < 10_000; id += 100 {
		adaptive.add(id)
		legacy.Add(id)
	}
	bench := func(b *testing.B, add func(*roaring.Bitmap)) {
		dst := roaring.New()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			dst.Clear()
			add(dst)
		}
	}
	b.Run("Adaptive", func(b *testing.B) { bench(b, adaptive.addTo) })
	b.Run("Roaring", func(b *testing.B) { bench(b, func(dst *roaring.Bitmap) { dst.Or(legacy) }) })
}

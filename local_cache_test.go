package ruleix

import (
	"cmp"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValueBitmapCacheEvictsLeastRecentlyUsedEntry(t *testing.T) {
	cache := &valueBitmapCache[int]{}
	a, b, c := 1, 2, 3

	cache.replace(&a).Add(1)
	cache.replace(&b).Add(2)

	bits, found := comparableValueCacheLookup(cache, &a)
	require.True(t, found)
	require.True(t, bits.Contains(1))

	cache.replace(&c).Add(3)

	_, found = comparableValueCacheLookup(cache, &b)
	require.False(t, found)
	bits, found = comparableValueCacheLookup(cache, &a)
	require.True(t, found)
	require.True(t, bits.Contains(1))
	bits, found = comparableValueCacheLookup(cache, &c)
	require.True(t, found)
	require.True(t, bits.Contains(3))
}

func TestBetweenCacheEvictsLeastRecentlyUsedEntry(t *testing.T) {
	type interval struct{ from, until *int }
	pointer := func(value int) *int { return &value }
	schema := Between(
		func(value interval) *int { return value.from },
		func(value interval) *int { return value.until },
		cmp.Compare[int],
	)
	entries := []interval{{from: pointer(0), until: pointer(100)}}
	sequence := Zip(entries, []int{1})
	index, err := New[interval, int](schema).Build(sequence)
	require.NoError(t, err)
	local := index.Local()

	queries := []interval{
		{from: pointer(10), until: pointer(20)},
		{from: pointer(11), until: pointer(21)},
		{from: pointer(10), until: pointer(20)},
		{from: pointer(12), until: pointer(22)},
	}
	var matches []int
	for _, query := range queries {
		local.Search(query, &matches)
	}

	rule := index.root.(*betweenRule[interval, int])
	cache := local.pool.local[rule.nodeID].between.(*betweenCache[int])
	cachedBounds := make(map[[2]int]bool, len(cache.entries))
	for _, entry := range cache.entries {
		cachedBounds[[2]int{entry.from, entry.until}] = true
	}
	require.Equal(t, map[[2]int]bool{{10, 20}: true, {12, 22}: true}, cachedBounds)
}

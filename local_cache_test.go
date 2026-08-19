//nolint:lll // Migration coverage keeps legacy pointer getters inline.
package ruleix

import (
	"cmp"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValueBitmapCacheEvictsLeastRecentlyUsedEntry(t *testing.T) {
	cache := &valueBitmapCache[int]{}
	a, b, c := 1, 2, 3

	cache.replace(optionalValue[int]{value: a, ok: true}).Add(1)
	cache.replace(optionalValue[int]{value: b, ok: true}).Add(2)

	bits, found := comparableValueCacheLookup(cache, optionalValue[int]{value: a, ok: true})
	require.True(t, found)
	require.True(t, bits.Contains(1))

	cache.replace(optionalValue[int]{value: c, ok: true}).Add(3)

	_, found = comparableValueCacheLookup(cache, optionalValue[int]{value: b, ok: true})
	require.False(t, found)
	bits, found = comparableValueCacheLookup(cache, optionalValue[int]{value: a, ok: true})
	require.True(t, found)
	require.True(t, bits.Contains(1))
	bits, found = comparableValueCacheLookup(cache, optionalValue[int]{value: c, ok: true})
	require.True(t, found)
	require.True(t, bits.Contains(3))
}

func TestValueBitmapCacheClearsValueForNilEntry(t *testing.T) {
	type value struct{ id int }
	cache := &valueBitmapCache[*value]{}
	first := &value{id: 1}
	second := &value{id: 2}

	cache.replace(optionalValue[*value]{value: first, ok: true})
	cache.replace(optionalValue[*value]{value: second, ok: true})
	cache.replace(optionalValue[*value]{})

	require.True(t, cache.entries[0].initialized)
	require.False(t, cache.entries[0].hasValue)
	require.Nil(t, cache.entries[0].value)
}

func TestBetweenCacheEvictsLeastRecentlyUsedEntry(t *testing.T) {
	type interval struct{ from, until *int }
	pointer := func(value int) *int { return &value }
	schema := Between(GetterFromPointer(func(value interval) *int { return value.from }), GetterFromPointer(func(value interval) *int { return value.until }), cmp.Compare[int])
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

func TestBetweenCacheClearsMissingBounds(t *testing.T) {
	type bound struct{ id int }
	type interval struct{ from, until **bound }
	pointer := func(value *bound) **bound { return &value }
	compare := func(a, b *bound) int { return cmp.Compare(a.id, b.id) }
	schema := Between(GetterFromPointer(func(value interval) **bound { return value.from }), GetterFromPointer(func(value interval) **bound { return value.until }), compare)
	low, high := &bound{id: 0}, &bound{id: 100}
	index, err := New[interval, int](schema).Build(Zip(
		[]interval{{from: pointer(low), until: pointer(high)}},
		[]int{1},
	))
	require.NoError(t, err)
	local := index.Local()

	for _, query := range []interval{
		{from: pointer(&bound{id: 10}), until: pointer(&bound{id: 20})},
		{from: pointer(&bound{id: 11}), until: pointer(&bound{id: 21})},
		{},
	} {
		var matches []int
		local.Search(query, &matches)
	}

	rule := index.root.(*betweenRule[interval, *bound])
	cache := local.pool.local[rule.nodeID].between.(*betweenCache[*bound])
	require.True(t, cache.entries[0].initialized)
	require.False(t, cache.entries[0].hasFrom)
	require.False(t, cache.entries[0].hasUntil)
	require.Nil(t, cache.entries[0].from)
	require.Nil(t, cache.entries[0].until)
}

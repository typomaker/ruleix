package ruleix

import (
	"cmp"
	"math"
	"testing"
	"time"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
)

func TestOrderedQuantizerLevelsAreNestedAndOutward(t *testing.T) {
	t.Run("integers", func(t *testing.T) {
		q, ok := compileOrderedQuantizer[int8]()
		require.True(t, ok)
		for value := int8(math.MinInt8); value < math.MaxInt8; value++ {
			lower, upper := value, value
			for level := uint32(1); level <= q.terminalLevel(); level++ {
				nextLower := q.rounded(lower, level, false)
				nextUpper := q.rounded(upper, level, true)
				require.LessOrEqual(t, nextLower, value)
				require.GreaterOrEqual(t, nextUpper, value)
				require.Equal(t, q.rounded(value, level, false), nextLower)
				require.Equal(t, q.rounded(value, level, true), nextUpper)
				lower, upper = nextLower, nextUpper
			}
		}
	})
	t.Run("time", func(t *testing.T) {
		q, ok := compileOrderedQuantizer[time.Time]()
		require.True(t, ok)
		value := time.Unix(-17, 987654321).UTC()
		lower := q.rounded(value, 1, false)
		upper := q.rounded(value, 1, true)
		require.False(t, lower.After(value))
		require.False(t, upper.Before(value))
		for level := uint32(2); level <= 12; level++ {
			lower = q.rounded(lower, level, false)
			upper = q.rounded(upper, level, true)
			require.Equal(t, q.rounded(value, level, false), lower)
			require.Equal(t, q.rounded(value, level, true), upper)
		}
	})
}

func TestOrderedQuantizerSupportsNumericKinds(t *testing.T) {
	check := func(ok bool) { require.True(t, ok) }
	_, ok := compileOrderedQuantizer[int]()
	check(ok)
	_, ok = compileOrderedQuantizer[int16]()
	check(ok)
	_, ok = compileOrderedQuantizer[int32]()
	check(ok)
	_, ok = compileOrderedQuantizer[int64]()
	check(ok)
	_, ok = compileOrderedQuantizer[uint]()
	check(ok)
	q8, ok := compileOrderedQuantizer[uint8]()
	check(ok)
	require.Equal(t, uint8(2), q8.rounded(1, 1, true))
	require.Equal(t, uint8(math.MaxUint8), q8.rounded(math.MaxUint8, 1, true))
	_, ok = compileOrderedQuantizer[uint16]()
	check(ok)
	_, ok = compileOrderedQuantizer[uint32]()
	check(ok)
	_, ok = compileOrderedQuantizer[uint64]()
	check(ok)
	_, ok = compileOrderedQuantizer[uintptr]()
	check(ok)
	q32, ok := compileOrderedQuantizer[float32]()
	check(ok)
	require.LessOrEqual(t, q32.rounded(float32(-1.25), 3, false), float32(-1.25))
	q64, ok := compileOrderedQuantizer[float64]()
	check(ok)
	require.GreaterOrEqual(t, q64.rounded(1.25, 3, true), 1.25)
	_, ok = compileOrderedQuantizer[string]()
	require.False(t, ok)
}

func TestQuantizedOrderedRepeatedRebuildAndLateValuesHaveNoFalseNegatives(t *testing.T) {
	type fixture struct{ value int8 }
	get := func(value fixture) (int8, bool) { return value.value, true }
	for _, tc := range []struct {
		name      string
		direction direction
		inclusive bool
	}{
		{"gt", greaterThan, false}, {"gte", greaterThan, true},
		{"lt", lessThan, false}, {"lte", lessThan, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exact := &orderedRule[fixture, int8]{
				get: get, compare: cmp.Compare[int8], dir: tc.direction, inclusive: tc.inclusive,
				wildcard: roaring.New(), index: newOrderedIndex(cmp.Compare[int8]),
			}
			for id, value := range []int8{-128, -65, -3, -1, 0, 1, 2, 63, 126, 127} {
				exact.insert(fixture{value}, uint32(id))
			}
			_, first, ok := exact.prepareStreamingFirstGeneration()
			require.True(t, ok)
			lossy := first.(*quantizedOrderedRule[fixture, int8])
			for range 4 {
				lossy.fitStreamingNext()
			}
			// These arrive after origin and width have already been fixed.
			for id, value := range []int8{-127, 3, 64} {
				exact.insert(fixture{value}, uint32(id+10))
				lossy.insert(fixture{value}, uint32(id+10))
			}
			for query := int16(math.MinInt8); query <= math.MaxInt8; query++ {
				for id := uint32(0); id < 13; id++ {
					if exact.matchesID(fixture{int8(query)}, id) {
						require.True(t, lossy.matchesID(fixture{int8(query)}, id), "query=%d id=%d", query, id)
					}
				}
			}
		})
	}
}

func TestNumericQuantizerRejectsDescendingComparator(t *testing.T) {
	type fixture struct{ value int }
	rule := &orderedRule[fixture, int]{
		get:      func(v fixture) (int, bool) { return v.value, true },
		compare:  func(a, b int) int { return cmp.Compare(b, a) },
		wildcard: roaring.New(), index: newOrderedIndex(func(a, b int) int { return cmp.Compare(b, a) }),
	}
	for id, value := range []int{1, 2, 3} {
		rule.insert(fixture{value}, uint32(id))
	}
	_, _, ok := rule.prepareStreamingFirstGeneration()
	require.False(t, ok)
}

func TestQuantizedTimeStrictBoundariesHaveNoFalseNegatives(t *testing.T) {
	type fixture struct{ value time.Time }
	get := func(value fixture) (time.Time, bool) { return value.value, true }
	base := time.Unix(-2, 0).UTC()
	stored := []time.Time{
		base.Add(1), base.Add(999 * time.Millisecond), base.Add(time.Second),
		base.Add(time.Second + time.Nanosecond), base.Add(3 * time.Second),
	}
	queries := []time.Time{
		base, base.Add(1), base.Add(500 * time.Millisecond), base.Add(time.Second),
		base.Add(time.Second + time.Nanosecond), base.Add(2 * time.Second), base.Add(4 * time.Second),
	}
	for _, direction := range []direction{greaterThan, lessThan} {
		exact := &orderedRule[fixture, time.Time]{
			get: get, compare: time.Time.Compare, dir: direction, inclusive: false,
			wildcard: roaring.New(), index: newOrderedIndex(time.Time.Compare),
		}
		for id, value := range stored {
			exact.insert(fixture{value}, uint32(id))
		}
		_, first, ok := exact.prepareStreamingFirstGeneration()
		require.True(t, ok)
		lossy := first.(*quantizedOrderedRule[fixture, time.Time])
		for range 5 {
			lossy.fitStreamingNext()
		}
		late := base.Add(-17 * time.Second)
		exact.insert(fixture{late}, uint32(len(stored)))
		lossy.insert(fixture{late}, uint32(len(stored)))
		for _, query := range queries {
			for id := uint32(0); id <= uint32(len(stored)); id++ {
				if exact.matchesID(fixture{query}, id) {
					require.True(t, lossy.matchesID(fixture{query}, id), "dir=%d query=%s id=%d", direction, query, id)
				}
			}
		}
	}
}

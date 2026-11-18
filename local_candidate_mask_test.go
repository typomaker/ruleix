package ruleix

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLocalCandidateMaskChecksSourceAndClearsOnClose(t *testing.T) {
	type offer struct {
		group int
		slot  int
	}
	schema := All(
		Include(func(v offer) (int, bool) { return v.group, v.group >= 0 }),
		Include(func(v offer) (int, bool) { return v.slot, v.slot >= 0 }),
	)
	ids := make([]int, 41)
	rows := make([]offer, len(ids))
	for i := range rows {
		ids[i] = i
		rows[i] = offer{group: i / 10, slot: i % 2}
	}
	index, err := New[offer, int](schema).Build(Zip(rows, ids))
	require.NoError(t, err)
	local := index.TestLocal()
	defer local.Close()
	mask := local.pool.candidateMask

	check := func(query offer) {
		t.Helper()
		want, got := []int{-1}, []int{-1}
		require.Equal(t, index.SearchOne(query, &want), local.SearchOne(query, &got))
		require.ElementsMatch(t, want, got)
	}
	check(offer{group: 0, slot: 0})
	first := mask.source
	require.NotNil(t, first)
	check(offer{group: 0, slot: 1})
	require.Same(t, first, mask.source)
	check(offer{group: 1, slot: 0})
	require.NotNil(t, mask.source)
	require.NotSame(t, first, mask.source)
	check(offer{group: 4, slot: 1})
	require.NotNil(t, mask.source)
	require.Equal(t, uint8(1), mask.count)
	require.Equal(t, uint32(40), mask.ids[0])
	check(offer{group: 4, slot: 0})
	require.Equal(t, uint8(1), mask.count)
	lastSource := mask.source
	check(offer{group: 5, slot: 0})
	require.Same(t, lastSource, mask.source)
	check(offer{group: 1, slot: -1})
	check(offer{group: 1, slot: 1})
	require.NotNil(t, mask.source)
	local.Close()
	require.Zero(t, *mask)
}

func TestSmallBetweenCandidateMaskMatchesDirectChecks(t *testing.T) {
	const count = 320
	rows := make([]betweenIDFixture, count)
	ids := make([]int, count)
	for id := range rows {
		ids[id] = id
		rows[id] = betweenIDFixture{
			category: id % 16, from: id % 16, until: 20 + id%12,
			missingFrom: id%7 == 0, missingUntil: id%11 == 0,
		}
	}
	_, root, original, child := buildBetweenIDFixture(t, rows, ids, false, 0, false)
	operand, ok := root.execution[child].directID.(*smallBetweenRule[betweenIDFixture, int])
	require.True(t, ok)
	for lower := -1; lower <= 17; lower++ {
		query := betweenIDFixture{
			from: lower, until: lower + 18,
			missingFrom: lower%3 == 0, missingUntil: lower%4 == 0,
		}
		candidates := []uint32{0, 1, 7, 11, 35, 44, 77, 319}
		const active = 0b11101111
		var want uint64
		for i, id := range candidates {
			bit := uint64(1) << i
			if active&bit != 0 && original.from.matchesID(query, id) && original.until.matchesID(query, id) {
				want |= bit
			}
		}
		require.Equal(t, want, operand.applyCandidateMask(query, candidates, active), "query %+v", query)
	}
}

func TestCandidateMaskStorageIsLocalAndUnobserved(t *testing.T) {
	var inspector Inspector
	get := func(value int) (int, bool) { return value, true }
	index, err := New[int, int](Inspect(&inspector, Include(get))).Build(Zip([]int{1}, []int{1}))
	require.NoError(t, err)
	require.Nil(t, index.pool.candidateMask)
	local := index.TestLocal()
	require.NotNil(t, local.pool.candidateMask)
	local.Close()

	index.localTelemetry.Store(63)
	observed := index.TestLocal()
	defer observed.Close()
	require.True(t, observed.observed)
	require.Nil(t, observed.pool.candidateMask)
	var matches []int
	require.True(t, observed.SearchOne(1, &matches))
	require.Equal(t, []int{1}, matches)
}

func TestLocalCandidateMaskCopiesBitmapPosting(t *testing.T) {
	get := func(value int) (int, bool) { return value, value >= 0 }
	source := Include(get).newState(&nodeIDAllocator{}, &buildStatistics{}).(*eqRule[int, int, int])
	ids := make([]uint32, 10)
	for i := range ids {
		ids[i] = uint32(i * 2)
		source.values.addBitmap(1, ids[i])
	}
	root := &allRule[int]{children: []Rule[int]{source}}
	pool := newLocalBitmapPool(1)
	first := rankedBitmap{card: 10, childIdx: 0}
	root.rememberCandidateMask(pool, -1, first)
	require.Nil(t, pool.candidateMask.source)
	root.rememberCandidateMask(pool, 1, first)
	require.Same(t, source.values.get(1), pool.candidateMask.source)
	require.Equal(t, ids, pool.candidateMask.ids[:pool.candidateMask.count])

	source.wildcard.Add(0)
	require.Nil(t, source.candidateMaskPosting(1))
}

func TestNarrowCandidateMaskPreparation(t *testing.T) {
	get := func(v int) (int, bool) { return v, true }
	supported := &allRule[int]{children: []Rule[int]{Include(get), Include(get)}}
	supported.prepareNarrowMaskOperands()
	require.True(t, supported.narrowMaskPrepared)

	for _, children := range [][]Rule[int]{
		nil,
		{Include(get)},
		{Include(get), Exclude(get)},
		{Include(get), Include(get), Include(get), Include(get), Include(get),
			Include(get), Include(get), Include(get), Include(get)},
	} {
		root := &allRule[int]{children: children}
		root.prepareNarrowMaskOperands()
		require.False(t, root.narrowMaskPrepared)
	}
}

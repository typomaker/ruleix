package ruleix

import (
	"testing"
	"unsafe"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
)

func TestLocalEmptyOperandTracksValuesAndPreservesSource(t *testing.T) {
	type pair struct{ left, right int }
	schema := All(
		Include(func(v pair) (int, bool) { return v.left, v.left >= 0 }),
		Include(func(v pair) (int, bool) { return v.right, v.right >= 0 }),
	)
	rows, ids := make([]pair, 10), make([]int, 10)
	for i := range rows {
		rows[i] = pair{1, 1}
		ids[i] = i
	}
	index, err := New[pair, int](schema).Build(Zip(rows, ids))
	require.NoError(t, err)
	root := index.root.(*allRule[pair])
	require.Equal(t, uint8(3), root.emptyKeys)
	local := index.TestLocal()
	defer local.Close()
	check := func(v pair) {
		t.Helper()
		got, want := []int{-1}, []int{-1}
		require.Equal(t, index.SearchOne(v, &want), local.SearchOne(v, &got))
		require.Equal(t, want, got)
	}
	check(pair{99, 1})
	require.Equal(t, -1, local.pool.candidateMask.first)
	check(pair{99, 2})
	require.Equal(t, -1, local.pool.candidateMask.first)
	check(pair{1, 99})
	require.Equal(t, -2, local.pool.candidateMask.first)
	check(pair{99, 1})
	require.Equal(t, -1, local.pool.candidateMask.first)
	check(pair{1, 1})
	source := local.pool.candidateMask.source
	require.NotNil(t, source)
	check(pair{99, 1})
	require.Same(t, source, local.pool.candidateMask.source)
	check(pair{1, -1})
	require.Same(t, source, local.pool.candidateMask.source)
	check(pair{1, 1})
	require.Same(t, source, local.pool.candidateMask.source)
	other := index.TestLocal()
	defer other.Close()
	var output []int
	other.SearchOne(pair{1, 99}, &output)
	require.Equal(t, -2, other.pool.candidateMask.first)
	require.Same(t, source, local.pool.candidateMask.source)
	mask := local.pool.candidateMask
	local.Close()
	require.Zero(t, *mask)
}

func TestLocalEmptyOperandLargeGroupFallsBack(t *testing.T) {
	type query [9]int
	rules := make([]Rule[query], 9)
	for i := range rules {
		position := i
		rules[i] = Include(func(v query) (int, bool) { return v[position], true })
	}
	index, err := New[query, int](All(rules...)).Build(Zip([]query{{1, 1, 1, 1, 1, 1, 1, 1, 1}}, []int{7}))
	require.NoError(t, err)
	root := index.root.(*allRule[query])
	require.Len(t, root.children, 9)
	require.Zero(t, root.emptyKeys)
	require.Zero(t, root.emptyOrder)
	local := index.TestLocal()
	defer local.Close()
	missing := query{1, 1, 1, 1, 1, 1, 1, 1, 99}
	for range 2 {
		var got []int
		require.False(t, local.SearchOne(missing, &got))
		require.Empty(t, got)
	}
	require.Equal(t, -9, local.pool.candidateMask.first)
	var got []int
	require.True(t, local.SearchOne(query{1, 1, 1, 1, 1, 1, 1, 1, 1}, &got))
	require.Equal(t, []int{7}, got)
}

func TestLocalEmptyOperandSkipsObservedAndCountedExecution(t *testing.T) {
	root := &allRule[int]{children: []Rule[int]{&countingRule{}}}
	plain := newLocalBitmapPool(1)
	require.False(t, root.retryEmptyOperand(plain, 0))
	plain.candidateMask.first = emptyRankInProgress
	rememberEmptyRank(plain, 0)
	require.Equal(t, -1, plain.candidateMask.first)
	plain.candidateMask.first = emptyRankInProgress
	require.False(t, root.retryEmptyOperand(plain, 0))
	plain.observeRuntime = true
	require.False(t, root.retryEmptyOperand(plain, 0))
	plain.candidateMask.first = 0
	rememberEmptyRank(plain, 0)
	require.Zero(t, plain.candidateMask.first)
	plain.observeRuntime = false
	root.executionCounters = &allExecutionCounters{}
	plain.candidateMask.first = -1
	require.False(t, root.retryEmptyOperand(plain, 0))
	plain.candidateMask.first = 0
	rememberEmptyRank(plain, 0)
	require.Zero(t, plain.candidateMask.first)
	root.executionCounters = nil
	require.False(t, root.retryEmptyOperand(newBitmapPool(), 0))
	rememberEmptyRank(newBitmapPool(), 0)
	root.children[0] = &countingRule{ids: []uint32{1}}
	rememberEmptyRank(plain, 0)
	require.Zero(t, plain.candidateMask.first)
}

type emptyPlanningOnlyRule struct {
	*checkerOnlyRule
	empty, full *roaring.Bitmap
}

func (r *emptyPlanningOnlyRule) lookupPlanningBitmap(v int) (*roaring.Bitmap, bool) {
	if v == 0 {
		return r.empty, true
	}
	return r.full, true
}

func TestLocalEmptyOperandCapturesCheapAndPlanningProofs(t *testing.T) {
	cheap := &countingRule{}
	checker := &checkerOnlyRule{child: &countingRule{}}
	planning := &emptyPlanningOnlyRule{
		checkerOnlyRule: &checkerOnlyRule{child: &countingRule{ids: []uint32{1}}},
		empty:           roaring.New(), full: roaring.BitmapOf(1),
	}
	for _, child := range []Rule[int]{cheap, checker, planning} {
		root := &allRule[int]{children: []Rule[int]{child}}
		pool := newLocalBitmapPool(1)
		pool.candidateMask.first = emptyRankInProgress
		require.False(t, root.rankChildren(0, pool, make([]rankedBitmap, 1)))
		require.Equal(t, -1, pool.candidateMask.first)
		require.True(t, root.retryEmptyOperand(pool, 0))
	}
	root := &allRule[int]{children: []Rule[int]{planning}}
	pool := newLocalBitmapPool(1)
	pool.candidateMask.first = -1
	require.False(t, root.retryEmptyOperand(pool, 1))
	require.Zero(t, pool.candidateMask.first)
	checker.child.ids = []uint32{1}
	root.children[0] = checker
	pool.candidateMask.first = -1
	require.False(t, root.retryEmptyOperand(pool, 1))
	require.Zero(t, pool.candidateMask.first)
}

func TestLocalEmptyOperandResultFallback(t *testing.T) {
	child := &countingRule{}
	root := &allRule[int]{children: []Rule[int]{child}}
	pool := newLocalBitmapPool(1)
	require.Nil(t, root.loadEmptyOperandResult(pool, 0))
	require.Nil(t, root.loadEmptyOperandResult(newBitmapPool(), 0))
	pool.candidateMask.first = emptyRankInProgress
	require.Nil(t, root.loadEmptyOperandResult(pool, 0))
	pool.candidateMask.first = -1
	root.executionCounters = &allExecutionCounters{}
	require.Nil(t, root.loadEmptyOperandResult(pool, 0))
	root.executionCounters = nil
	require.Same(t, &emptyOperandResult, root.loadEmptyOperandResult(pool, 0))
	child.ids = []uint32{1}
	require.Nil(t, root.loadEmptyOperandResult(pool, 0))
	require.Zero(t, pool.candidateMask.first)
}

func TestEmptyPreparationFitsExistingLayouts(t *testing.T) {
	require.LessOrEqual(t, unsafe.Sizeof(allRule[int]{}), uintptr(160))
	require.LessOrEqual(t, unsafe.Sizeof(localCandidateMask{}), uintptr(88))
	require.LessOrEqual(t, unsafe.Sizeof(plainLocalBitmapPool{}), uintptr(336))
}

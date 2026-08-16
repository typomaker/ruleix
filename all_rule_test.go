package ruleix

import (
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
)

type countingRule struct {
	ids              []uint32
	searchCalls      int
	cardinalityCalls int
}

func (*countingRule) rule()                                 {}
func (r *countingRule) newState(*nodeIDAllocator) Rule[int] { return r }
func (*countingRule) validate(int) error                    { return nil }
func (*countingRule) insert(int, uint32)                    {}
func (r *countingRule) cardinality(int, *bitmapPool) uint64 {
	r.cardinalityCalls++
	return uint64(len(r.ids))
}
func (r *countingRule) search(_ int, dst *roaring.Bitmap, _ *bitmapPool) {
	r.searchCalls++
	dst.AddMany(r.ids)
}
func (*countingRule) exclude(int, *roaring.Bitmap, *bitmapPool) {}

func TestAllMaterializesEachChildOnce(t *testing.T) {
	first := &countingRule{ids: []uint32{1, 2, 3}}
	second := &countingRule{ids: []uint32{2}}
	third := &countingRule{ids: []uint32{2, 3}}
	rule := All[int](first, second, third)
	pool := newBitmapPool()
	dst := pool.get()
	defer pool.put(dst)

	rule.search(0, dst, pool)

	require.Equal(t, []uint32{2}, dst.ToArray())
	for _, child := range []*countingRule{first, second, third} {
		require.Equal(t, 1, child.searchCalls)
		require.Zero(t, child.cardinalityCalls)
	}
}

func TestAllStopsMaterializingAfterEmptyChild(t *testing.T) {
	first := &countingRule{ids: []uint32{1}}
	empty := &countingRule{}
	unreached := &countingRule{ids: []uint32{1}}
	rule := All[int](first, empty, unreached)
	pool := newBitmapPool()
	dst := pool.get()
	defer pool.put(dst)

	rule.search(0, dst, pool)

	require.True(t, dst.IsEmpty())
	require.Equal(t, 1, first.searchCalls)
	require.Equal(t, 1, empty.searchCalls)
	require.Zero(t, unreached.searchCalls)
}

package ruleix

import (
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
)

type observedRule struct {
	name           string
	cardinalityFor func(int) uint64
	order          *[]string
}

func (*observedRule) rule()                                                   {}
func (r *observedRule) newState(*nodeIDAllocator, *buildStatistics) Rule[int] { return r }
func (*observedRule) validate(int) error                                      { return nil }
func (*observedRule) insert(int, uint32)                                      {}
func (r *observedRule) cardinality(value int, _ *bitmapPool) uint64 {
	return r.cardinalityFor(value)
}
func (r *observedRule) search(_ int, dst *roaring.Bitmap, _ *bitmapPool) {
	*r.order = append(*r.order, r.name)
	dst.Add(0)
}
func (*observedRule) exclude(int, *roaring.Bitmap, *bitmapPool)    {}
func (*observedRule) collectBuildStatistics([]nodeBuildStatistics) {}

func TestAllMaterializesFiltersInSchemaOrder(t *testing.T) {
	var order []string
	left := &observedRule{
		name: "left",
		cardinalityFor: func(value int) uint64 {
			if value == 1 {
				return 100
			}
			return 1
		},
		order: &order,
	}
	right := &observedRule{
		name: "right",
		cardinalityFor: func(value int) uint64 {
			if value == 1 {
				return 1
			}
			return 100
		},
		order: &order,
	}
	entries, err := Zip([]int{0}, []string{"match"})
	require.NoError(t, err)
	ix, err := New[int, string](All[int](left, right)).Build(entries)
	require.NoError(t, err)

	var dst []string
	ix.Search(1, &dst)
	require.Equal(t, []string{"left", "right"}, order)

	order = nil
	ix.Search(2, &dst)
	require.Equal(t, []string{"left", "right"}, order)
}

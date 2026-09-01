package ruleix

import "github.com/RoaringBitmap/roaring/v2"

// It returns every ID stored in that leaf, so it may lose all selectivity but
// can never remove an exact match.
type lossyUniversalRule[T any] struct {
	nodeID nodeID
	bits   *roaring.Bitmap
	name   string
}

func (r *lossyUniversalRule[T]) runtimeNodeID() nodeID                               { return r.nodeID }
func (*lossyUniversalRule[T]) rule()                                                 {}
func (r *lossyUniversalRule[T]) newState(*nodeIDAllocator, *buildStatistics) Rule[T] { return r }
func (*lossyUniversalRule[T]) validate(T) error                                      { return nil }
func (r *lossyUniversalRule[T]) insert(_ T, id uint32)                               { r.bits.Add(id) }
func (r *lossyUniversalRule[T]) cardinality(T, *bitmapPool) uint64                   { return r.bits.GetCardinality() }
func (r *lossyUniversalRule[T]) estimateCardinality(T) uint64                        { return r.bits.GetCardinality() }
func (r *lossyUniversalRule[T]) isCardinalityZero(T) bool                            { return r.bits.IsEmpty() }
func (r *lossyUniversalRule[T]) lookupPlanningBitmap(T) (*roaring.Bitmap, bool)      { return r.bits, true }
func (r *lossyUniversalRule[T]) matchesID(_ T, id uint32) bool                       { return r.bits.Contains(id) }
func (r *lossyUniversalRule[T]) search(_ T, dst *roaring.Bitmap, _ *bitmapPool)      { dst.Or(r.bits) }
func (*lossyUniversalRule[T]) exclude(T, *roaring.Bitmap, *bitmapPool)               {}
func (*lossyUniversalRule[T]) collectBuildStatistics([]nodeBuildStatistics)          {}
func (r *lossyUniversalRule[T]) inspectionStrategy() string                          { return r.name }
func (*lossyUniversalRule[T]) inspectionMode() RuleMode                              { return RuleModeLossy }
func (r *lossyUniversalRule[T]) inspectionDetails() inspectionDetails {
	return representationDetails(uint64(24)+bitmapBytes(r.bits), r.bits.GetCardinality(), 1, 1, true)
}
func (*lossyUniversalRule[T]) streamingLossyAccumulator() {}
func (r *lossyUniversalRule[T]) refreshedStreamingDetails(inspectionDetails) inspectionDetails {
	return r.inspectionDetails()
}
func (r *lossyUniversalRule[T]) prepareSearch()                         { prepareBitmapForSearch(r.bits) }
func (r *lossyUniversalRule[T]) internBitmaps(interner *bitmapInterner) { interner.intern(&r.bits) }

type fixedLossyAllPlanner[T any] struct{ ladder []lossyRepresentation[T] }

func (p fixedLossyAllPlanner[T]) compile(limit uint64) (Rule[T], error) {
	return selectLossyRepresentation(p.ladder, limit, "ruleix: Lossy rule cannot fit the memory limit")
}

func (p fixedLossyAllPlanner[T]) representationLadder() ([]lossyRepresentation[T], error) {
	return p.ladder, nil
}

func newUniversalLossyPlanner[T any](exact Rule[T], node nodeID, name string, all *roaring.Bitmap) lossyAllPlanner[T] {
	usage := uint64(24) + bitmapBytes(all)
	fallback := Rule[T](&inspectionDetailsRule[T]{
		child:   &lossyUniversalRule[T]{nodeID: node, bits: all, name: name},
		details: representationDetails(usage, all.GetCardinality(), 1, 1, true),
	})
	return fixedLossyAllPlanner[T]{ladder: buildLossyRepresentationLadder(exact, []Rule[T]{fallback})}
}

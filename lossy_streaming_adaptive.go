package ruleix

import "github.com/RoaringBitmap/roaring/v2"

type streamingLimitFitter interface{ fitStreamingLimit(uint64) }
type streamingStepFitter interface{ fitStreamingNext() }
type streamingFitAvailability interface{ canFitStreaming() bool }
type streamingNextUsageProvider interface{ nextStreamingUsage() (uint64, bool) }
type streamingNextPreparer interface{ prepareStreamingNext() (uint64, func(), bool) }

type streamingAdaptiveLeaf[T any] struct {
	child                          Rule[T]
	nextUsage                      uint64
	nextUsagePrepared, nextUsageOK bool
	nextApply                      func()
}

func (*streamingAdaptiveLeaf[T]) rule()                                                 {}
func (r *streamingAdaptiveLeaf[T]) newState(*nodeIDAllocator, *buildStatistics) Rule[T] { return r }
func (r *streamingAdaptiveLeaf[T]) validate(v T) error                                  { return r.child.validate(v) }
func (r *streamingAdaptiveLeaf[T]) insert(v T, id uint32) {
	r.nextUsagePrepared, r.nextApply = false, nil
	r.child.insert(v, id)
}
func (r *streamingAdaptiveLeaf[T]) cardinality(v T, p *bitmapPool) uint64 {
	return r.child.cardinality(v, p)
}
func (r *streamingAdaptiveLeaf[T]) search(v T, dst *roaring.Bitmap, p *bitmapPool) {
	r.child.search(v, dst, p)
}
func (r *streamingAdaptiveLeaf[T]) exclude(v T, dst *roaring.Bitmap, p *bitmapPool) {
	r.child.exclude(v, dst, p)
}
func (r *streamingAdaptiveLeaf[T]) collectBuildStatistics(s []nodeBuildStatistics) {
	r.child.collectBuildStatistics(s)
}
func (r *streamingAdaptiveLeaf[T]) inspectionMode() RuleMode   { return inspectionModeOf(r.child) }
func (r *streamingAdaptiveLeaf[T]) inspectionStrategy() string { return inspectionStrategyOf(r.child) }
func (r *streamingAdaptiveLeaf[T]) refreshedStreamingDetails(details inspectionDetails) inspectionDetails {
	return refreshedStreamingRuleDetails(r.child, details)
}
func (r *streamingAdaptiveLeaf[T]) canFitStreaming() bool { _, ok := r.nextStreamingUsage(); return ok }

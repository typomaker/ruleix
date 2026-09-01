package ruleix

import (
	"sort"

	"github.com/RoaringBitmap/roaring/v2"
)

func (r *lossyComparedOrderedRule[T, V]) runtimeNodeID() nodeID { return r.nodeID }
func (*lossyComparedOrderedRule[T, V]) rule()                   {}
func (r *lossyComparedOrderedRule[T, V]) newState(*nodeIDAllocator, *buildStatistics) Rule[T] {
	return r
}
func (*lossyComparedOrderedRule[T, V]) validate(T) error           { return nil }
func (*lossyComparedOrderedRule[T, V]) streamingLossyAccumulator() {}
func (r *lossyComparedOrderedRule[T, V]) refreshedStreamingDetails(details inspectionDetails) inspectionDetails {
	usage := uint64(40) + bitmapBytes(r.wildcard) + uint64(len(r.buckets))*16
	items := r.wildcard.GetCardinality()
	for i, bucket := range r.buckets {
		usage += bitmapBytes(bucket) + comparableValueBytes(any(r.boundaries[i]))
		items += bucket.GetCardinality()
	}
	details.MemoryUsageBytes, details.MemoryUsageAvailable = usage, true
	details.Items, details.ItemsAvailable = items, true
	details.GranularityValue, details.GranularityAvailable = uint64(len(r.buckets)), true
	return details
}
func (r *lossyComparedOrderedRule[T, V]) fitStreamingLimit(limit uint64) {
	buckets := lossyComparedBuckets[V]{
		compare: r.compare, minimum: r.minimum, maximum: r.maximum,
		capacity: r.capacity, boundaries: r.boundaries, buckets: r.buckets,
	}
	for r.refreshedStreamingDetails(inspectionDetails{}).MemoryUsageBytes > limit && len(buckets.buckets) > 1 {
		buckets.coarsenOne()
		r.boundaries, r.buckets = buckets.boundaries, buckets.buckets
	}
	r.capacity = min(max(r.capacity, 1), len(buckets.buckets))
	r.boundaries, r.buckets = buckets.boundaries, buckets.buckets
}
func (r *lossyComparedOrderedRule[T, V]) fitStreamingNext() {
	if len(r.buckets) <= 1 {
		return
	}
	buckets := lossyComparedBuckets[V]{
		compare: r.compare, minimum: r.minimum, maximum: r.maximum,
		capacity: r.capacity, boundaries: r.boundaries, buckets: r.buckets,
	}
	buckets.coarsenOne()
	r.capacity = min(max(r.capacity, 1), len(buckets.buckets))
	r.boundaries, r.buckets = buckets.boundaries, buckets.buckets
}
func (r *lossyComparedOrderedRule[T, V]) nextStreamingUsage() (uint64, bool) {
	usage, _, ok := r.prepareStreamingNext()
	return usage, ok
}
func (r *lossyComparedOrderedRule[T, V]) prepareStreamingNext() (uint64, func(), bool) {
	if len(r.buckets) <= 1 {
		return 0, nil, false
	}
	clone := *r
	buckets := cloneLossyComparedBuckets(lossyComparedBuckets[V]{
		compare: r.compare, minimum: r.minimum, maximum: r.maximum,
		capacity: r.capacity, boundaries: r.boundaries, buckets: r.buckets,
	})
	buckets.coarsenOne()
	clone.capacity = min(max(clone.capacity, 1), len(buckets.buckets))
	clone.boundaries, clone.buckets = buckets.boundaries, buckets.buckets
	usage := clone.refreshedStreamingDetails(inspectionDetails{}).MemoryUsageBytes
	return usage, func() {
		r.capacity, r.boundaries, r.buckets = clone.capacity, clone.boundaries, clone.buckets
	}, true
}
func (r *lossyComparedOrderedRule[T, V]) insert(v T, id uint32) {
	value, ok := r.get(v)
	if !ok {
		r.wildcard.Add(id)
		return
	}
	buckets := lossyComparedBuckets[V]{
		compare: r.compare, minimum: r.minimum, maximum: r.maximum,
		capacity: r.capacity, boundaries: r.boundaries, buckets: r.buckets,
	}
	buckets.insert(value, id)
	r.minimum, r.maximum = buckets.minimum, buckets.maximum
	r.capacity = buckets.capacity
	r.boundaries, r.buckets = buckets.boundaries, buckets.buckets
}
func (r *lossyComparedOrderedRule[T, V]) matchingBucketRange(v T) (int, int, bool) {
	value, ok := r.get(v)
	if !ok || len(r.buckets) == 0 {
		return 0, 0, false
	}
	last := len(r.buckets) - 1
	if r.dir == greaterThan {
		minimum := r.compare(value, r.minimum)
		if minimum < 0 || (!r.inclusive && minimum == 0) {
			return 0, 0, false
		}
		if r.compare(value, r.maximum) >= 0 {
			return 0, last, true
		}
		end := sort.Search(len(r.boundaries), func(i int) bool {
			return r.compare(r.boundaries[i], value) >= 0
		})
		return 0, end, true
	}
	maximum := r.compare(value, r.maximum)
	if maximum > 0 || (!r.inclusive && maximum == 0) {
		return 0, 0, false
	}
	if r.compare(value, r.minimum) <= 0 {
		return 0, last, true
	}
	first := sort.Search(len(r.boundaries), func(i int) bool {
		return r.compare(r.boundaries[i], value) >= 0
	})
	return first, last, true
}
func (r *lossyComparedOrderedRule[T, V]) search(v T, dst *roaring.Bitmap, _ *bitmapPool) {
	dst.Or(r.wildcard)
	first, last, ok := r.matchingBucketRange(v)
	if !ok {
		return
	}
	for i := first; i <= last; i++ {
		dst.Or(r.buckets[i])
	}
}
func (r *lossyComparedOrderedRule[T, V]) estimateCardinality(v T) uint64 {
	n := r.wildcard.GetCardinality()
	first, last, ok := r.matchingBucketRange(v)
	if !ok {
		return n
	}
	for i := first; i <= last; i++ {
		n += r.buckets[i].GetCardinality()
	}
	return n
}
func (r *lossyComparedOrderedRule[T, V]) cardinality(v T, _ *bitmapPool) uint64 {
	return r.estimateCardinality(v)
}
func (r *lossyComparedOrderedRule[T, V]) isCardinalityZero(v T) bool {
	return r.estimateCardinality(v) == 0
}
func (r *lossyComparedOrderedRule[T, V]) matchesID(v T, id uint32) bool {
	if r.wildcard.Contains(id) {
		return true
	}
	first, last, ok := r.matchingBucketRange(v)
	if !ok {
		return false
	}
	for i := first; i <= last; i++ {
		if r.buckets[i].Contains(id) {
			return true
		}
	}
	return false
}
func (*lossyComparedOrderedRule[T, V]) exclude(T, *roaring.Bitmap, *bitmapPool)      {}
func (*lossyComparedOrderedRule[T, V]) collectBuildStatistics([]nodeBuildStatistics) {}
func (*lossyComparedOrderedRule[T, V]) inspectionStrategy() string                   { return "lossy-ordered" }
func (*lossyComparedOrderedRule[T, V]) inspectionMode() RuleMode                     { return RuleModeLossy }
func (r *lossyComparedOrderedRule[T, V]) prepareSearch() {
	prepareBitmapForSearch(r.wildcard)
	for _, bits := range r.buckets {
		prepareBitmapForSearch(bits)
	}
}

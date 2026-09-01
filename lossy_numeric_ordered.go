package ruleix

import (
	"math"

	"github.com/RoaringBitmap/roaring/v2"
)

func (r *lossyOrderedRule[T, V]) runtimeNodeID() nodeID { return r.nodeID }

func lossyOrderedBucket(key, minimum, width, count uint64) uint64 {
	bucket := (key - minimum) / width
	if bucket >= count {
		return count - 1
	}
	return bucket
}

func lossyOrderedGrid(minimum, maximum uint64, wanted int) (uint64, int) {
	count := uint64(max(wanted, 1))
	span := maximum - minimum
	width := span/count + 1
	if width == 0 {
		width = math.MaxUint64
	}
	used := min(span/width+1, count)
	return width, int(used)
}

// regrid rebuilds a coarser numeric grid using only the interval represented
// by each old bucket. An old posting is copied to every overlapping new bucket,
// preserving the no-false-negative contract without retaining exact values.
func (r *lossyOrderedRule[T, V]) regrid(minimum, maximum uint64, wanted int) {
	width, count := lossyOrderedGrid(minimum, maximum, wanted)
	next := make([]*roaring.Bitmap, count)
	oldMinimum, oldMaximum, oldWidth := r.min, r.max, r.width
	for i, bits := range r.buckets {
		if bits == nil {
			continue
		}
		start := oldMinimum
		if i != 0 {
			if oldWidth != 0 && uint64(i) > math.MaxUint64/oldWidth {
				start = math.MaxUint64
			} else {
				offset := uint64(i) * oldWidth
				if offset > math.MaxUint64-oldMinimum {
					start = math.MaxUint64
				} else {
					start += offset
				}
			}
		}
		end := oldMaximum
		if oldWidth != math.MaxUint64 && oldWidth-1 <= math.MaxUint64-start {
			end = min(end, start+oldWidth-1)
		}
		first := lossyOrderedBucket(start, minimum, width, uint64(count))
		last := lossyOrderedBucket(end, minimum, width, uint64(count))
		for bucket := first; bucket <= last; bucket++ {
			if next[bucket] == nil {
				next[bucket] = roaring.New()
			}
			next[bucket].Or(bits)
		}
	}
	r.min, r.max, r.width, r.buckets = minimum, maximum, width, next
}

func (*lossyOrderedRule[T, V]) rule()                                                 {}
func (r *lossyOrderedRule[T, V]) newState(*nodeIDAllocator, *buildStatistics) Rule[T] { return r }
func (*lossyOrderedRule[T, V]) validate(T) error                                      { return nil }
func (*lossyOrderedRule[T, V]) streamingLossyAccumulator()                            {}
func (r *lossyOrderedRule[T, V]) refreshedStreamingDetails(details inspectionDetails) inspectionDetails {
	usage := uint64(32) + bitmapBytes(r.wildcard) + uint64(len(r.buckets))*8
	items := r.wildcard.GetCardinality()
	for _, bucket := range r.buckets {
		if bucket != nil {
			usage += bitmapBytes(bucket)
			items += bucket.GetCardinality()
		}
	}
	details.MemoryUsageBytes, details.MemoryUsageAvailable = usage, true
	details.Items, details.ItemsAvailable = items, true
	details.GranularityValue, details.GranularityAvailable = uint64(len(r.buckets)), true
	return details
}
func (r *lossyOrderedRule[T, V]) fitStreamingLimit(limit uint64) {
	for r.refreshedStreamingDetails(inspectionDetails{}).MemoryUsageBytes > limit && len(r.buckets) > 1 {
		r.regrid(r.min, r.max, len(r.buckets)-1)
	}
}
func (r *lossyOrderedRule[T, V]) fitStreamingNext() {
	if len(r.buckets) > 1 {
		r.regrid(r.min, r.max, len(r.buckets)-1)
	}
}
func (r *lossyOrderedRule[T, V]) nextStreamingUsage() (uint64, bool) {
	usage, _, ok := r.prepareStreamingNext()
	return usage, ok
}
func (r *lossyOrderedRule[T, V]) prepareStreamingNext() (uint64, func(), bool) {
	if len(r.buckets) <= 1 {
		return 0, nil, false
	}
	clone := *r
	clone.regrid(clone.min, clone.max, len(clone.buckets)-1)
	usage := clone.refreshedStreamingDetails(inspectionDetails{}).MemoryUsageBytes
	return usage, func() {
		r.min, r.max, r.width, r.buckets = clone.min, clone.max, clone.width, clone.buckets
	}, true
}
func (r *lossyOrderedRule[T, V]) insert(v T, id uint32) {
	value, ok := r.get(v)
	if !ok {
		r.wildcard.Add(id)
		return
	}
	key := r.encoder.key(value)
	if len(r.buckets) == 0 {
		r.min, r.max, r.width = key, key, math.MaxUint64
		r.buckets = []*roaring.Bitmap{roaring.BitmapOf(id)}
		return
	}
	if key < r.min || key > r.max {
		r.regrid(min(r.min, key), max(r.max, key), len(r.buckets))
	}
	bucket := lossyOrderedBucket(key, r.min, r.width, uint64(len(r.buckets)))
	if r.buckets[bucket] == nil {
		r.buckets[bucket] = roaring.New()
	}
	r.buckets[bucket].Add(id)
}
func (r *lossyOrderedRule[T, V]) search(v T, dst *roaring.Bitmap, _ *bitmapPool) {
	dst.Or(r.wildcard)
	first, last, ok := r.matchingBucketRange(v)
	if !ok {
		return
	}
	for n := first; n <= last; n++ {
		if b := r.buckets[n]; b != nil {
			dst.Or(b)
		}
	}
}
func (r *lossyOrderedRule[T, V]) matchingBucketRange(v T) (uint64, uint64, bool) {
	value, ok := r.get(v)
	if !ok || len(r.buckets) == 0 {
		return 0, 0, false
	}
	key := r.encoder.key(value)
	last := uint64(len(r.buckets) - 1)
	if r.dir == greaterThan {
		if key < r.min || (!r.inclusive && key == r.min) {
			return 0, 0, false
		}
		if key < r.max {
			last = lossyOrderedBucket(key, r.min, r.width, uint64(len(r.buckets)))
		}
		return 0, last, true
	}
	if key > r.max || (!r.inclusive && key == r.max) {
		return 0, 0, false
	}
	first := uint64(0)
	if key > r.min {
		first = lossyOrderedBucket(key, r.min, r.width, uint64(len(r.buckets)))
	}
	return first, last, true
}
func (r *lossyOrderedRule[T, V]) estimateCardinality(v T) uint64 {
	n := r.wildcard.GetCardinality()
	first, last, ok := r.matchingBucketRange(v)
	if !ok {
		return n
	}
	for bucket := first; bucket <= last; bucket++ {
		if bits := r.buckets[bucket]; bits != nil {
			n += bits.GetCardinality()
		}
	}
	return n
}
func (r *lossyOrderedRule[T, V]) isCardinalityZero(v T) bool {
	return r.estimateCardinality(v) == 0
}
func (r *lossyOrderedRule[T, V]) matchesID(v T, id uint32) bool {
	if r.wildcard.Contains(id) {
		return true
	}
	first, last, ok := r.matchingBucketRange(v)
	if !ok {
		return false
	}
	for bucket := first; bucket <= last; bucket++ {
		if bits := r.buckets[bucket]; bits != nil && bits.Contains(id) {
			return true
		}
	}
	return false
}
func (r *lossyOrderedRule[T, V]) cardinality(v T, _ *bitmapPool) uint64 {
	return r.estimateCardinality(v)
}
func (*lossyOrderedRule[T, V]) exclude(T, *roaring.Bitmap, *bitmapPool)      {}
func (*lossyOrderedRule[T, V]) collectBuildStatistics([]nodeBuildStatistics) {}
func (*lossyOrderedRule[T, V]) inspectionStrategy() string                   { return "lossy-ordered-buckets" }
func (*lossyOrderedRule[T, V]) inspectionMode() RuleMode                     { return RuleModeLossy }
func (r *lossyOrderedRule[T, V]) prepareSearch() {
	prepareBitmapForSearch(r.wildcard)
	for _, b := range r.buckets {
		if b != nil {
			prepareBitmapForSearch(b)
		}
	}
}
func (r *lossyOrderedRule[T, V]) internBitmaps(i *bitmapInterner) {
	i.intern(&r.wildcard)
	for n, b := range r.buckets {
		if b != nil {
			i.intern(&b)
			r.buckets[n] = b
		}
	}
}

func addAccounting(total, add uint64) (uint64, bool) {
	if math.MaxUint64-total < add {
		return 0, false
	}
	return total + add, true
}

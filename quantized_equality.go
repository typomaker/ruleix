package ruleix

import (
	"math/bits"
	"sort"

	"github.com/RoaringBitmap/roaring/v2"
)

func (*eqRule[T, V]) streamingLossyAccumulator() {}

func (r *eqRule[T, V]) inspectionMode() RuleMode {
	if r.quantizer.bucketCount != 0 {
		return RuleModeLossy
	}
	return RuleModeExact
}

func (r *eqRule[T, V]) fitStreamingLimit(limit uint64) {
	for r.streamingEqualityDetails(inspectionDetails{}).MemoryUsageBytes > limit && r.quantizer.bucketCount > 1 {
		r.rebucket(nextEqualityBucketCount(r.quantizer.bucketCount))
	}
}

func (r *eqRule[T, V]) fitStreamingNext() {
	if r.quantizer.bucketCount > 1 {
		r.rebucket(nextEqualityBucketCount(r.quantizer.bucketCount))
	}
}

func (r *eqRule[T, V]) nextStreamingUsage() (uint64, bool) {
	usage, _, ok := r.prepareStreamingNext()
	return usage, ok
}

func (r *eqRule[T, V]) prepareStreamingNext() (uint64, func(), bool) {
	if r.quantizer.bucketCount <= 1 {
		return 0, nil, false
	}
	clone := *r
	clone.rebucket(nextEqualityBucketCount(clone.quantizer.bucketCount))
	usage := clone.streamingEqualityDetails(inspectionDetails{}).MemoryUsageBytes
	return usage, func() { r.quantizer, r.values = clone.quantizer, clone.values }, true
}

func nextEqualityBucketCount(current uint64) uint64 {
	for _, count := range equalityBucketCounts(lossyMaxBucketBits) {
		if count > 0 && count < current {
			return count
		}
	}
	return 1
}

func (r *eqRule[T, V]) rebucket(count uint64) {
	count = min(max(count, 1), r.quantizer.bucketCount)
	if count == r.quantizer.bucketCount {
		return
	}
	nextQuantizer := newEqualityQuantizer(count)
	old := make(postingGeneration[uint64], len(r.values.sets))
	r.values.visit(func(key equalityPhysicalKey[V], set *equalitySet) {
		bits := roaring.New()
		set.addTo(bits)
		old[key.bucket] = bits
	})
	keys := make([]uint64, 0, len(old))
	for key := range old {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	next, _, ok := rebuildPostingGenerationInOrder(old, keys, int(count), 24, func(key uint64) uint64 {
		return r.quantizer.coarsen(key, nextQuantizer)
	})
	if !ok {
		return
	}
	values := newEqualityIndex[equalityPhysicalKey[V]](len(next))
	keys = keys[:0]
	for key := range next {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, key := range keys {
		values.addSet(bucketEqualityKey[V](key), &equalitySet{bits: next[key]})
	}
	r.quantizer, r.values = nextQuantizer, values
}

func (r *eqRule[T, V]) lookupPlanningBitmap(v T) (*roaring.Bitmap, bool) {
	if !r.wildcard.IsEmpty() {
		return nil, false
	}
	value, ok := r.get(v)
	if !ok {
		return r.wildcard, true
	}
	set := r.values.get(r.equalityKey(value))
	if set == nil {
		return r.wildcard, true
	}
	if set.bits == nil {
		return nil, false
	}
	return set.bits, true
}

func (r *eqRule[T, V]) estimateCachedCardinality(v T, pool *bitmapPool) (uint64, bool) {
	bits, found := r.lookupCachedBitmap(v, pool)
	if !found {
		return 0, false
	}
	return bits.GetCardinality(), true
}

func reduceEqualityHash(hash, bucketCount uint64) uint64 {
	return newEqualityQuantizer(bucketCount).key(hash)
}

func coarsenEqualityBucket(bucket, current, next uint64) uint64 {
	return newEqualityQuantizer(current).coarsen(bucket, newEqualityQuantizer(next))
}

func reduceEqualityBaseBucket(base, upper, count uint64) uint64 {
	merged := upper - count
	if base < merged*2 {
		return base / 2
	}
	return base - merged
}

func equalityBucketUpper(count uint64) uint64 {
	return uint64(1) << bits.Len64(count-1)
}

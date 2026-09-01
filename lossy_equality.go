package ruleix

import (
	"math/bits"
	"unsafe"

	"github.com/RoaringBitmap/roaring/v2"
)

type lossyEqualityRule[T any, V comparable] struct {
	nodeID         nodeID
	get            Getter[T, V]
	wildcard       *roaring.Bitmap
	wildcardSource physicalSourceID
	wildcardClass  uint32
	quantizer      equalityQuantizer
	codec          equalityCodec[V]
	buckets        map[uint64]lossyEqualityPosting
}

type lossyEqualityPosting struct {
	bits   *roaring.Bitmap
	source physicalSourceID
	class  uint32
}

func (r *lossyEqualityRule[T, V]) runtimeNodeID() nodeID { return r.nodeID }

func (r *lossyEqualityRule[T, V]) streamingUniversal() (nodeID, *roaring.Bitmap, string) {
	bits := r.wildcard.Clone()
	for _, posting := range r.buckets {
		bits.Or(posting.bits)
	}
	return r.nodeID, bits, "lossy-streaming-universal"
}

func (*lossyEqualityRule[T, V]) streamingLossyAccumulator() {}
func (r *lossyEqualityRule[T, V]) refreshedStreamingDetails(details inspectionDetails) inspectionDetails {
	usage := uint64(40) + bitmapBytes(r.wildcard)
	items := r.wildcard.GetCardinality()
	for _, posting := range r.buckets {
		usage += 24 + bitmapBytes(posting.bits)
		items += posting.bits.GetCardinality()
	}
	details.MemoryUsageBytes, details.MemoryUsageAvailable = usage, true
	details.Items, details.ItemsAvailable = items, true
	details.GranularityValue, details.GranularityAvailable = uint64(len(r.buckets)), true
	return details
}
func (r *lossyEqualityRule[T, V]) fitStreamingLimit(limit uint64) {
	for r.refreshedStreamingDetails(inspectionDetails{}).MemoryUsageBytes > limit && r.quantizer.bucketCount > 1 {
		r.rebucket(nextEqualityBucketCount(r.quantizer.bucketCount))
	}
}
func (r *lossyEqualityRule[T, V]) fitStreamingNext() {
	if r.quantizer.bucketCount > 1 {
		r.rebucket(nextEqualityBucketCount(r.quantizer.bucketCount))
	}
}
func (r *lossyEqualityRule[T, V]) nextStreamingUsage() (uint64, bool) {
	usage, _, ok := r.prepareStreamingNext()
	return usage, ok
}
func (r *lossyEqualityRule[T, V]) prepareStreamingNext() (uint64, func(), bool) {
	if r.quantizer.bucketCount <= 1 {
		return 0, nil, false
	}
	clone := *r
	clone.rebucket(nextEqualityBucketCount(clone.quantizer.bucketCount))
	usage := clone.refreshedStreamingDetails(inspectionDetails{}).MemoryUsageBytes
	return usage, func() { r.quantizer, r.buckets = clone.quantizer, clone.buckets }, true
}

func nextEqualityBucketCount(current uint64) uint64 {
	for _, count := range equalityBucketCounts(lossyMaxBucketBits) {
		if count > 0 && count < current {
			return count
		}
	}
	return 1
}

// rebucket maps each old nested hash class to its single parent class. The
// original hash values are not retained.
func (r *lossyEqualityRule[T, V]) rebucket(count uint64) {
	count = min(max(count, 1), r.quantizer.bucketCount)
	if count == r.quantizer.bucketCount {
		return
	}
	nextQuantizer := newEqualityQuantizer(count)
	next := make(map[uint64]lossyEqualityPosting, min(len(r.buckets), int(count)))
	for old, posting := range r.buckets {
		bucket := r.quantizer.coarsen(old, nextQuantizer)
		merged := next[bucket]
		if merged.bits == nil {
			merged.bits = roaring.New()
		}
		merged.bits.Or(posting.bits)
		next[bucket] = merged
	}
	r.quantizer, r.buckets = nextQuantizer, next
}

func (r *lossyEqualityRule[T, V]) lookupPlanningBitmap(v T) (*roaring.Bitmap, bool) {
	// A wildcard requires a union with the concrete bucket, so it cannot expose
	// one of its owned bitmaps as the complete child result.
	if !r.wildcard.IsEmpty() {
		return nil, false
	}
	value, ok := r.get(v)
	if !ok {
		return r.wildcard, true
	}
	hash := r.codec.hash(value)
	bits := r.buckets[r.quantizer.key(hash)].bits
	if bits == nil {
		return r.wildcard, true
	}
	return bits, true
}

func (*lossyEqualityRule[T, V]) rule()                                                 {}
func (r *lossyEqualityRule[T, V]) newState(*nodeIDAllocator, *buildStatistics) Rule[T] { return r }
func (*lossyEqualityRule[T, V]) validate(T) error                                      { return nil }
func (r *lossyEqualityRule[T, V]) insert(v T, id uint32) {
	value, ok := r.get(v)
	if !ok {
		r.wildcard.Add(id)
		return
	}
	bucket := r.quantizer.key(r.codec.hash(value))
	posting := r.buckets[bucket]
	if posting.bits == nil {
		posting.bits = roaring.New()
	}
	posting.bits.Add(id)
	r.buckets[bucket] = posting
}
func (r *lossyEqualityRule[T, V]) search(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	value := getOptional(r.get, v)
	if pool.local != nil {
		cache := equalityCache[V](pool, r.nodeID)
		if bits, found := comparableValueCacheLookup(cache, value); found {
			dst.Or(bits)
			return
		}
		if comparableValueCacheAdmit(cache, value) {
			bits := cache.replace(value, pool)
			r.addMatches(value, bits)
			dst.Or(bits)
			cache.commit(bits, pool)
			return
		}
	}
	r.addMatches(value, dst)
}
func (r *lossyEqualityRule[T, V]) addMatches(value optionalValue[V], dst *roaring.Bitmap) {
	dst.Or(r.wildcard)
	if !value.ok {
		return
	}
	hash := r.codec.hash(value.value)
	if bits := r.buckets[r.quantizer.key(hash)].bits; bits != nil {
		dst.Or(bits)
	}
}
func (r *lossyEqualityRule[T, V]) estimateCardinality(v T) uint64 {
	n := r.wildcard.GetCardinality()
	value, ok := r.get(v)
	if !ok {
		return n
	}
	hash := r.codec.hash(value)
	if bits := r.buckets[r.quantizer.key(hash)].bits; bits != nil {
		n += bits.GetCardinality()
	}
	return n
}
func (r *lossyEqualityRule[T, V]) estimateCheapCardinality(v T) uint64 {
	return r.estimateCardinality(v)
}
func (r *lossyEqualityRule[T, V]) lookupEqualityClass(v T) uint32 {
	value, ok := r.get(v)
	if !ok {
		return r.wildcardClass
	}
	hash := r.codec.hash(value)
	return r.buckets[r.quantizer.key(hash)].class
}
func (r *lossyEqualityRule[T, V]) estimateCachedCardinality(v T, pool *bitmapPool) (uint64, bool) {
	bits, found := r.lookupCachedBitmap(v, pool)
	if !found {
		return 0, false
	}
	return bits.GetCardinality(), true
}
func (r *lossyEqualityRule[T, V]) lookupCachedBitmap(v T, pool *bitmapPool) (*roaring.Bitmap, bool) {
	return lookupEqualityCachedBitmap(pool, r.nodeID, getOptional(r.get, v))
}
func (r *lossyEqualityRule[T, V]) localQueryKey(v T) (any, uint64) {
	return getOptional(r.get, v), uint64(16 + unsafe.Sizeof(optionalValue[V]{}))
}
func (r *lossyEqualityRule[T, V]) localQueryKeyMatches(v T, key any) bool {
	want, ok := key.(optionalValue[V])
	return ok && want == getOptional(r.get, v)
}
func (r *lossyEqualityRule[T, V]) isCardinalityZero(v T) bool {
	return r.estimateCardinality(v) == 0
}
func (r *lossyEqualityRule[T, V]) matchesID(v T, id uint32) bool {
	if r.wildcard.Contains(id) {
		return true
	}
	value, ok := r.get(v)
	if !ok {
		return false
	}
	hash := r.codec.hash(value)
	bits := r.buckets[r.quantizer.key(hash)].bits
	return bits != nil && bits.Contains(id)
}
func (*lossyEqualityRule[T, V]) directIDWork() uint64 { return allEqualityDirectIDWork }

// reduceEqualityHash maps the complete codec hash onto a nested immutable
// class. Intermediate levels pairwise merge a prefix of the finest cells.
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
func (r *lossyEqualityRule[T, V]) cardinality(v T, _ *bitmapPool) uint64 {
	return r.estimateCardinality(v)
}
func (*lossyEqualityRule[T, V]) exclude(T, *roaring.Bitmap, *bitmapPool)      {}
func (*lossyEqualityRule[T, V]) collectBuildStatistics([]nodeBuildStatistics) {}
func (*lossyEqualityRule[T, V]) inspectionStrategy() string                   { return "lossy-grouped-hash" }
func (*lossyEqualityRule[T, V]) inspectionMode() RuleMode                     { return RuleModeLossy }
func (r *lossyEqualityRule[T, V]) prepareSearch() {
	prepareBitmapForSearch(r.wildcard)
	for _, posting := range r.buckets {
		prepareBitmapForSearch(posting.bits)
	}
}
func (r *lossyEqualityRule[T, V]) internBitmaps(i *bitmapInterner) {
	r.wildcardSource = i.internSource(&r.wildcard)
	for k, posting := range r.buckets {
		posting.source = i.internSource(&posting.bits)
		r.buckets[k] = posting
	}
}
func (r *lossyEqualityRule[T, V]) equalitySourceCount() int { return 1 + len(r.buckets) }
func (r *lossyEqualityRule[T, V]) visitEqualitySources(visit func(equalitySourcePair)) {
	visit(equalitySourcePair{wildcard: r.wildcardSource})
	for _, posting := range r.buckets {
		if posting.source == 0 {
			continue
		}
		visit(equalitySourcePair{wildcard: r.wildcardSource, posting: posting.source})
	}
}
func (r *lossyEqualityRule[T, V]) assignEqualityClasses(classes map[equalitySourcePair]uint32) {
	r.wildcardClass = classes[equalitySourcePair{wildcard: r.wildcardSource}]
	for key, posting := range r.buckets {
		if posting.source != 0 {
			posting.class = classes[equalitySourcePair{wildcard: r.wildcardSource, posting: posting.source}]
			r.buckets[key] = posting
		}
	}
}

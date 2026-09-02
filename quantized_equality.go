package ruleix

import (
	"math/bits"
	"sort"
	"unsafe"

	"github.com/RoaringBitmap/roaring/v2"
)

// quantizedEqualityRule uses the same equalityIndex/equalitySet posting layout
// as exact equality. Its only policy is the compiled value-to-key transform.
type quantizedEqualityRule[T any, V comparable] struct {
	nodeID         nodeID
	get            Getter[T, V]
	wildcard       *roaring.Bitmap
	wildcardSource physicalSourceID
	wildcardClass  uint32
	quantizer      equalityQuantizer
	codec          equalityCodec[V]
	values         equalityIndex[uint64]
}

func (r *quantizedEqualityRule[T, V]) runtimeNodeID() nodeID { return r.nodeID }

func (*quantizedEqualityRule[T, V]) streamingLossyAccumulator() {}
func (r *quantizedEqualityRule[T, V]) refreshedStreamingDetails(details inspectionDetails) inspectionDetails {
	usage := uint64(40) + bitmapBytes(r.wildcard)
	items := r.wildcard.GetCardinality()
	for index := range r.values.sets {
		posting := &r.values.sets[index]
		usage += 24 + equalitySetBytes(posting)
		items += posting.cardinality()
	}
	details.MemoryUsageBytes, details.MemoryUsageAvailable = usage, true
	details.Items, details.ItemsAvailable = items, true
	details.GranularityValue, details.GranularityAvailable = uint64(len(r.values.sets)), true
	return details
}
func (r *quantizedEqualityRule[T, V]) fitStreamingLimit(limit uint64) {
	for r.refreshedStreamingDetails(inspectionDetails{}).MemoryUsageBytes > limit && r.quantizer.bucketCount > 1 {
		r.rebucket(nextEqualityBucketCount(r.quantizer.bucketCount))
	}
}
func (r *quantizedEqualityRule[T, V]) fitStreamingNext() {
	if r.quantizer.bucketCount > 1 {
		r.rebucket(nextEqualityBucketCount(r.quantizer.bucketCount))
	}
}
func (r *quantizedEqualityRule[T, V]) nextStreamingUsage() (uint64, bool) {
	usage, _, ok := r.prepareStreamingNext()
	return usage, ok
}
func (r *quantizedEqualityRule[T, V]) prepareStreamingNext() (uint64, func(), bool) {
	if r.quantizer.bucketCount <= 1 {
		return 0, nil, false
	}
	clone := *r
	clone.rebucket(nextEqualityBucketCount(clone.quantizer.bucketCount))
	usage := clone.refreshedStreamingDetails(inspectionDetails{}).MemoryUsageBytes
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

// rebucket maps each old nested hash class to its single parent class. The
// original hash values are not retained.
func (r *quantizedEqualityRule[T, V]) rebucket(count uint64) {
	count = min(max(count, 1), r.quantizer.bucketCount)
	if count == r.quantizer.bucketCount {
		return
	}
	nextQuantizer := newEqualityQuantizer(count)
	old := make(postingGeneration[uint64], len(r.values.sets))
	r.values.visit(func(key uint64, set *equalitySet) {
		bits := roaring.New()
		set.addTo(bits)
		old[key] = bits
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
	values := newEqualityIndex[uint64](len(next))
	keys = keys[:0]
	for key := range next {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, key := range keys {
		values.addSet(key, &equalitySet{bits: next[key]})
	}
	r.quantizer, r.values = nextQuantizer, values
}

func (r *quantizedEqualityRule[T, V]) lookupPlanningBitmap(v T) (*roaring.Bitmap, bool) {
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
	set := r.values.get(r.quantizer.key(hash))
	if set == nil {
		return r.wildcard, true
	}
	if set.bits == nil {
		return nil, false
	}
	return set.bits, true
}

func (*quantizedEqualityRule[T, V]) rule()                                                 {}
func (r *quantizedEqualityRule[T, V]) newState(*nodeIDAllocator, *buildStatistics) Rule[T] { return r }
func (*quantizedEqualityRule[T, V]) validate(T) error                                      { return nil }
func (r *quantizedEqualityRule[T, V]) insert(v T, id uint32) {
	value, ok := r.get(v)
	if !ok {
		r.wildcard.Add(id)
		return
	}
	bucket := r.quantizer.key(r.codec.hash(value))
	r.values.addBitmap(bucket, id)
}
func (r *quantizedEqualityRule[T, V]) search(v T, dst *roaring.Bitmap, pool *bitmapPool) {
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
func (r *quantizedEqualityRule[T, V]) addMatches(value optionalValue[V], dst *roaring.Bitmap) {
	if !value.ok {
		addEqualityMatches(r.wildcard, &r.values, optionalValue[uint64]{}, dst)
		return
	}
	key := r.quantizer.key(r.codec.hash(value.value))
	addEqualityMatches(r.wildcard, &r.values, optionalValue[uint64]{value: key, ok: true}, dst)
}
func (r *quantizedEqualityRule[T, V]) estimateCardinality(v T) uint64 {
	n := r.wildcard.GetCardinality()
	value, ok := r.get(v)
	if !ok {
		return n
	}
	hash := r.codec.hash(value)
	if set := r.values.get(r.quantizer.key(hash)); set != nil {
		n += set.cardinality()
	}
	return n
}
func (r *quantizedEqualityRule[T, V]) estimateCheapCardinality(v T) uint64 {
	return r.estimateCardinality(v)
}
func (r *quantizedEqualityRule[T, V]) lookupEqualityClass(v T) uint32 {
	value, ok := r.get(v)
	if !ok {
		return r.wildcardClass
	}
	hash := r.codec.hash(value)
	if set := r.values.get(r.quantizer.key(hash)); set != nil {
		return set.class
	}
	return r.wildcardClass
}
func (r *quantizedEqualityRule[T, V]) estimateCachedCardinality(v T, pool *bitmapPool) (uint64, bool) {
	bits, found := r.lookupCachedBitmap(v, pool)
	if !found {
		return 0, false
	}
	return bits.GetCardinality(), true
}
func (r *quantizedEqualityRule[T, V]) lookupCachedBitmap(v T, pool *bitmapPool) (*roaring.Bitmap, bool) {
	return lookupEqualityCachedBitmap(pool, r.nodeID, getOptional(r.get, v))
}
func (r *quantizedEqualityRule[T, V]) localQueryKey(v T) (any, uint64) {
	return getOptional(r.get, v), uint64(16 + unsafe.Sizeof(optionalValue[V]{}))
}
func (r *quantizedEqualityRule[T, V]) localQueryKeyMatches(v T, key any) bool {
	want, ok := key.(optionalValue[V])
	return ok && want == getOptional(r.get, v)
}
func (r *quantizedEqualityRule[T, V]) isCardinalityZero(v T) bool {
	return r.estimateCardinality(v) == 0
}
func (r *quantizedEqualityRule[T, V]) matchesID(v T, id uint32) bool {
	if r.wildcard.Contains(id) {
		return true
	}
	value, ok := r.get(v)
	if !ok {
		return false
	}
	hash := r.codec.hash(value)
	set := r.values.get(r.quantizer.key(hash))
	return set != nil && set.contains(id)
}
func (*quantizedEqualityRule[T, V]) directIDWork() uint64 { return allEqualityDirectIDWork }

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
func (r *quantizedEqualityRule[T, V]) cardinality(v T, _ *bitmapPool) uint64 {
	return r.estimateCardinality(v)
}
func (*quantizedEqualityRule[T, V]) exclude(T, *roaring.Bitmap, *bitmapPool)      {}
func (*quantizedEqualityRule[T, V]) collectBuildStatistics([]nodeBuildStatistics) {}
func (*quantizedEqualityRule[T, V]) inspectionStrategy() string                   { return "equality" }
func (*quantizedEqualityRule[T, V]) inspectionMode() RuleMode                     { return RuleModeLossy }
func (r *quantizedEqualityRule[T, V]) prepareSearch() {
	prepareBitmapForSearch(r.wildcard)
	for index := range r.values.sets {
		r.values.sets[index].prepareSearch()
	}
}
func (r *quantizedEqualityRule[T, V]) internBitmaps(i *bitmapInterner) {
	r.wildcardSource = i.internSource(&r.wildcard)
	for index := range r.values.sets {
		r.values.sets[index].internBitmaps(i)
	}
}
func (r *quantizedEqualityRule[T, V]) equalitySourceCount() int { return 1 + len(r.values.sets) }
func (r *quantizedEqualityRule[T, V]) visitEqualitySources(visit func(equalitySourcePair)) {
	visit(equalitySourcePair{wildcard: r.wildcardSource})
	for index := range r.values.sets {
		set := &r.values.sets[index]
		if set.source == 0 {
			continue
		}
		visit(equalitySourcePair{wildcard: r.wildcardSource, posting: set.source})
	}
}
func (r *quantizedEqualityRule[T, V]) assignEqualityClasses(classes map[equalitySourcePair]uint32) {
	r.wildcardClass = classes[equalitySourcePair{wildcard: r.wildcardSource}]
	for index := range r.values.sets {
		set := &r.values.sets[index]
		if set.source != 0 {
			set.class = classes[equalitySourcePair{wildcard: r.wildcardSource, posting: set.source}]
		}
	}
}

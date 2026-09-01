package ruleix

import (
	"unsafe"

	"github.com/RoaringBitmap/roaring/v2"
)

type ternaryEqRule[T any, V comparable] struct {
	nodeID         nodeID
	get            Getter[T, V]
	wildcard       *roaring.Bitmap
	wildcardSource physicalSourceID
	wildcardClass  uint32
	keys           [3]V
	sets           [3]equalitySet
}

func (r *ternaryEqRule[T, V]) runtimeNodeID() nodeID    { return r.nodeID }
func (*ternaryEqRule[T, V]) inspectionStrategy() string { return "equality-ternary" }
func (r *ternaryEqRule[T, V]) canonicalDescriptor() canonicalRuleDescriptor {
	return canonicalRuleDescriptor{representation: canonicalEquality, schema: r.nodeID}
}
func (*ternaryEqRule[T, V]) rule()                                                 {}
func (r *ternaryEqRule[T, V]) newState(*nodeIDAllocator, *buildStatistics) Rule[T] { return r }
func (*ternaryEqRule[T, V]) validate(T) error                                      { return nil }
func (*ternaryEqRule[T, V]) insert(T, uint32)                                      {}
func (r *ternaryEqRule[T, V]) matchingSet(value optionalValue[V]) *equalitySet {
	if !value.ok {
		return nil
	}
	if value.value == r.keys[0] {
		return &r.sets[0]
	}
	if value.value == r.keys[1] {
		return &r.sets[1]
	}
	if value.value == r.keys[2] {
		return &r.sets[2]
	}
	return nil
}
func (r *ternaryEqRule[T, V]) cardinality(v T, _ *bitmapPool) uint64 {
	return r.estimateCardinality(v)
}
func (r *ternaryEqRule[T, V]) estimateCardinality(v T) uint64 {
	n := r.wildcard.GetCardinality()
	if set := r.matchingSet(getOptional(r.get, v)); set != nil {
		n += set.cardinality()
	}
	return n
}
func (r *ternaryEqRule[T, V]) estimateCheapCardinality(v T) uint64 {
	return r.estimateCardinality(v)
}
func (r *ternaryEqRule[T, V]) lookupCachedBitmap(v T, pool *bitmapPool) (*roaring.Bitmap, bool) {
	return lookupEqualityCachedBitmap(pool, r.nodeID, getOptional(r.get, v))
}
func (r *ternaryEqRule[T, V]) localQueryKey(v T) (any, uint64) {
	return getOptional(r.get, v), uint64(16 + unsafe.Sizeof(optionalValue[V]{}))
}
func (r *ternaryEqRule[T, V]) localQueryKeyMatches(v T, key any) bool {
	want, ok := key.(optionalValue[V])
	return ok && want == getOptional(r.get, v)
}
func (r *ternaryEqRule[T, V]) isCardinalityZero(v T) bool {
	return r.wildcard.IsEmpty() && r.matchingSet(getOptional(r.get, v)) == nil
}
func (r *ternaryEqRule[T, V]) matchesID(v T, id uint32) bool {
	if r.wildcard.Contains(id) {
		return true
	}
	set := r.matchingSet(getOptional(r.get, v))
	return set != nil && set.contains(id)
}
func (*ternaryEqRule[T, V]) directIDWork() uint64 { return allEqualityDirectIDWork }
func (r *ternaryEqRule[T, V]) search(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	value := getOptional(r.get, v)
	if pool.local == nil {
		r.addMatches(value, dst)
		return
	}
	cache := equalityCache[V](pool, r.nodeID)
	if bits, found := comparableValueCacheLookup(cache, value); found {
		dst.Or(bits)
		return
	}
	if !comparableValueCacheAdmit(cache, value) {
		r.addMatches(value, dst)
		return
	}
	bits := cache.replace(value, pool)
	r.addMatches(value, bits)
	dst.Or(bits)
	cache.commit(bits, pool)
}
func (r *ternaryEqRule[T, V]) addMatches(value optionalValue[V], dst *roaring.Bitmap) {
	dst.Or(r.wildcard)
	if set := r.matchingSet(value); set != nil {
		set.addTo(dst)
	}
}
func (r *ternaryEqRule[T, V]) sharedWildcard() *roaring.Bitmap { return r.wildcard }
func (r *ternaryEqRule[T, V]) visitEqualityResultBitmaps(visit func(*roaring.Bitmap)) {
	visit(r.wildcard)
	for i := range r.sets {
		if r.sets[i].bits != nil {
			visit(r.sets[i].bits)
		}
	}
}
func (r *ternaryEqRule[T, V]) lookupEqualityResultComponents(v T) (*roaring.Bitmap, *roaring.Bitmap, bool) {
	set := r.matchingSet(getOptional(r.get, v))
	posting, deduplicable := equalitySetBitmap(set)
	return r.wildcard, posting, set == nil || deduplicable
}
func (r *ternaryEqRule[T, V]) lookupEqualityClass(v T) uint32 {
	if set := r.matchingSet(getOptional(r.get, v)); set != nil {
		return set.class
	}
	return r.wildcardClass
}
func (r *ternaryEqRule[T, V]) addConcreteMatches(v T, dst *roaring.Bitmap) {
	if set := r.matchingSet(getOptional(r.get, v)); set != nil {
		set.addTo(dst)
	}
}
func (r *ternaryEqRule[T, V]) intersectConcreteMatches(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	intersectEqualitySet(r.matchingSet(getOptional(r.get, v)), dst, pool)
}
func (*ternaryEqRule[T, V]) exclude(T, *roaring.Bitmap, *bitmapPool)      {}
func (*ternaryEqRule[T, V]) collectBuildStatistics([]nodeBuildStatistics) {}
func (r *ternaryEqRule[T, V]) prepareSearch() {
	prepareBitmapForSearch(r.wildcard)
	for i := range r.sets {
		r.sets[i].prepareSearch()
	}
}
func (r *ternaryEqRule[T, V]) internBitmaps(interner *bitmapInterner) {
	r.wildcardSource = interner.internSource(&r.wildcard)
	for i := range r.sets {
		r.sets[i].internBitmaps(interner)
	}
}
func (r *ternaryEqRule[T, V]) equalitySourceCount() int { return 4 }
func (r *ternaryEqRule[T, V]) visitEqualitySources(visit func(equalitySourcePair)) {
	visit(equalitySourcePair{wildcard: r.wildcardSource})
	for i := range r.sets {
		set := r.sets[i]
		if set.source != 0 {
			visit(equalitySourcePair{wildcard: r.wildcardSource, posting: set.source})
		}
	}
}
func (r *ternaryEqRule[T, V]) assignEqualityClasses(classes map[equalitySourcePair]uint32) {
	r.wildcardClass = classes[equalitySourcePair{wildcard: r.wildcardSource}]
	for i := range r.sets {
		set := &r.sets[i]
		if set.source != 0 {
			set.class = classes[equalitySourcePair{wildcard: r.wildcardSource, posting: set.source}]
		}
	}
}

// quaternaryEqRule is the largest fixed equality specialization. Measurements
// keep five or more values on the general map-backed representation.
type quaternaryEqRule[T any, V comparable] struct {
	nodeID         nodeID
	get            Getter[T, V]
	wildcard       *roaring.Bitmap
	wildcardSource physicalSourceID
	wildcardClass  uint32
	keys           [4]V
	sets           [4]equalitySet
}

func (r *quaternaryEqRule[T, V]) runtimeNodeID() nodeID    { return r.nodeID }
func (*quaternaryEqRule[T, V]) inspectionStrategy() string { return "equality-quaternary" }
func (r *quaternaryEqRule[T, V]) canonicalDescriptor() canonicalRuleDescriptor {
	return canonicalRuleDescriptor{representation: canonicalEquality, schema: r.nodeID}
}
func (*quaternaryEqRule[T, V]) rule()                                                 {}
func (r *quaternaryEqRule[T, V]) newState(*nodeIDAllocator, *buildStatistics) Rule[T] { return r }
func (*quaternaryEqRule[T, V]) validate(T) error                                      { return nil }
func (*quaternaryEqRule[T, V]) insert(T, uint32)                                      {}
func (r *quaternaryEqRule[T, V]) matchingSet(value optionalValue[V]) *equalitySet {
	if !value.ok {
		return nil
	}
	if value.value == r.keys[0] {
		return &r.sets[0]
	}
	if value.value == r.keys[1] {
		return &r.sets[1]
	}
	if value.value == r.keys[2] {
		return &r.sets[2]
	}
	if value.value == r.keys[3] {
		return &r.sets[3]
	}
	return nil
}
func (r *quaternaryEqRule[T, V]) cardinality(v T, _ *bitmapPool) uint64 {
	return r.estimateCardinality(v)
}
func (r *quaternaryEqRule[T, V]) estimateCardinality(v T) uint64 {
	n := r.wildcard.GetCardinality()
	if set := r.matchingSet(getOptional(r.get, v)); set != nil {
		n += set.cardinality()
	}
	return n
}
func (r *quaternaryEqRule[T, V]) estimateCheapCardinality(v T) uint64 {
	return r.estimateCardinality(v)
}
func (r *quaternaryEqRule[T, V]) lookupCachedBitmap(v T, pool *bitmapPool) (*roaring.Bitmap, bool) {
	return lookupEqualityCachedBitmap(pool, r.nodeID, getOptional(r.get, v))
}
func (r *quaternaryEqRule[T, V]) localQueryKey(v T) (any, uint64) {
	return getOptional(r.get, v), uint64(16 + unsafe.Sizeof(optionalValue[V]{}))
}
func (r *quaternaryEqRule[T, V]) localQueryKeyMatches(v T, key any) bool {
	want, ok := key.(optionalValue[V])
	return ok && want == getOptional(r.get, v)
}
func (r *quaternaryEqRule[T, V]) isCardinalityZero(v T) bool {
	return r.wildcard.IsEmpty() && r.matchingSet(getOptional(r.get, v)) == nil
}
func (r *quaternaryEqRule[T, V]) matchesID(v T, id uint32) bool {
	if r.wildcard.Contains(id) {
		return true
	}
	set := r.matchingSet(getOptional(r.get, v))
	return set != nil && set.contains(id)
}
func (*quaternaryEqRule[T, V]) directIDWork() uint64 { return allEqualityDirectIDWork }
func (r *quaternaryEqRule[T, V]) search(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	value := getOptional(r.get, v)
	if pool.local == nil {
		r.addMatches(value, dst)
		return
	}
	cache := equalityCache[V](pool, r.nodeID)
	if bits, found := comparableValueCacheLookup(cache, value); found {
		dst.Or(bits)
		return
	}
	if !comparableValueCacheAdmit(cache, value) {
		r.addMatches(value, dst)
		return
	}
	bits := cache.replace(value, pool)
	r.addMatches(value, bits)
	dst.Or(bits)
	cache.commit(bits, pool)
}
func (r *quaternaryEqRule[T, V]) addMatches(value optionalValue[V], dst *roaring.Bitmap) {
	dst.Or(r.wildcard)
	if set := r.matchingSet(value); set != nil {
		set.addTo(dst)
	}
}
func (r *quaternaryEqRule[T, V]) sharedWildcard() *roaring.Bitmap { return r.wildcard }
func (r *quaternaryEqRule[T, V]) visitEqualityResultBitmaps(visit func(*roaring.Bitmap)) {
	visit(r.wildcard)
	for i := range r.sets {
		if r.sets[i].bits != nil {
			visit(r.sets[i].bits)
		}
	}
}
func (r *quaternaryEqRule[T, V]) lookupEqualityResultComponents(v T) (*roaring.Bitmap, *roaring.Bitmap, bool) {
	set := r.matchingSet(getOptional(r.get, v))
	posting, deduplicable := equalitySetBitmap(set)
	return r.wildcard, posting, set == nil || deduplicable
}
func (r *quaternaryEqRule[T, V]) lookupEqualityClass(v T) uint32 {
	if set := r.matchingSet(getOptional(r.get, v)); set != nil {
		return set.class
	}
	return r.wildcardClass
}
func (r *quaternaryEqRule[T, V]) addConcreteMatches(v T, dst *roaring.Bitmap) {
	if set := r.matchingSet(getOptional(r.get, v)); set != nil {
		set.addTo(dst)
	}
}
func (r *quaternaryEqRule[T, V]) intersectConcreteMatches(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	intersectEqualitySet(r.matchingSet(getOptional(r.get, v)), dst, pool)
}
func (*quaternaryEqRule[T, V]) exclude(T, *roaring.Bitmap, *bitmapPool)      {}
func (*quaternaryEqRule[T, V]) collectBuildStatistics([]nodeBuildStatistics) {}
func (r *quaternaryEqRule[T, V]) prepareSearch() {
	prepareBitmapForSearch(r.wildcard)
	for i := range r.sets {
		r.sets[i].prepareSearch()
	}
}
func (r *quaternaryEqRule[T, V]) internBitmaps(interner *bitmapInterner) {
	r.wildcardSource = interner.internSource(&r.wildcard)
	for i := range r.sets {
		r.sets[i].internBitmaps(interner)
	}
}
func (r *quaternaryEqRule[T, V]) equalitySourceCount() int { return 5 }
func (r *quaternaryEqRule[T, V]) visitEqualitySources(visit func(equalitySourcePair)) {
	visit(equalitySourcePair{wildcard: r.wildcardSource})
	for i := range r.sets {
		set := r.sets[i]
		if set.source != 0 {
			visit(equalitySourcePair{wildcard: r.wildcardSource, posting: set.source})
		}
	}
}
func (r *quaternaryEqRule[T, V]) assignEqualityClasses(classes map[equalitySourcePair]uint32) {
	r.wildcardClass = classes[equalitySourcePair{wildcard: r.wildcardSource}]
	for i := range r.sets {
		set := &r.sets[i]
		if set.source != 0 {
			set.class = classes[equalitySourcePair{wildcard: r.wildcardSource, posting: set.source}]
		}
	}
}

func equalityCache[V comparable](pool *bitmapPool, id nodeID) *valueBitmapCache[V] {
	node := &pool.local[int(id)]
	cache, _ := node.equality.(*valueBitmapCache[V])
	if cache == nil {
		cache = newValueBitmapCache[V](pool, id)
		node.equality = cache
	}
	return cache
}

func lookupEqualityCachedBitmap[V comparable](
	pool *bitmapPool,
	id nodeID,
	value optionalValue[V],
) (*roaring.Bitmap, bool) {
	if pool.local == nil {
		return nil, false
	}
	cache, _ := pool.local[int(id)].equality.(*valueBitmapCache[V])
	if cache == nil {
		return nil, false
	}
	return comparableValueCacheLookup(cache, value)
}

func equalitySetBitmap(set *equalitySet) (*roaring.Bitmap, bool) {
	if set == nil || set.bits == nil {
		return nil, false
	}
	return set.bits, true
}

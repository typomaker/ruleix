package ruleix

import (
	"unsafe"

	"github.com/RoaringBitmap/roaring/v2"
)

// unaryEqRule and binaryEqRule are immutable build-time specializations for
// low-cardinality equality filters. Besides avoiding a map lookup, they discard
// the slice and capacity retained by the general equality index.
type unaryEqRule[T any, V comparable] struct {
	nodeID         nodeID
	get            Getter[T, V]
	wildcard       *roaring.Bitmap
	wildcardSource physicalSourceID
	wildcardClass  uint32
	key            V
	set            equalitySet
}

func (r *unaryEqRule[T, V]) runtimeNodeID() nodeID { return r.nodeID }

func (*unaryEqRule[T, V]) inspectionStrategy() string { return "equality-unary" }
func (r *unaryEqRule[T, V]) canonicalDescriptor() canonicalRuleDescriptor {
	return canonicalRuleDescriptor{representation: canonicalEquality, schema: r.nodeID}
}

func (*unaryEqRule[T, V]) rule()                                                 {}
func (r *unaryEqRule[T, V]) newState(*nodeIDAllocator, *buildStatistics) Rule[T] { return r }
func (*unaryEqRule[T, V]) validate(T) error                                      { return nil }
func (*unaryEqRule[T, V]) insert(T, uint32)                                      {}
func (r *unaryEqRule[T, V]) matchingSet(value optionalValue[V]) *equalitySet {
	if value.ok && value.value == r.key {
		return &r.set
	}
	return nil
}
func (r *unaryEqRule[T, V]) cardinality(v T, _ *bitmapPool) uint64 {
	return r.estimateCardinality(v)
}
func (r *unaryEqRule[T, V]) estimateCardinality(v T) uint64 {
	n := r.wildcard.GetCardinality()
	if set := r.matchingSet(getOptional(r.get, v)); set != nil {
		n += set.cardinality()
	}
	return n
}
func (r *unaryEqRule[T, V]) estimateCheapCardinality(v T) uint64 {
	return r.estimateCardinality(v)
}
func (r *unaryEqRule[T, V]) lookupCachedBitmap(v T, pool *bitmapPool) (*roaring.Bitmap, bool) {
	return lookupEqualityCachedBitmap(pool, r.nodeID, getOptional(r.get, v))
}
func (r *unaryEqRule[T, V]) localQueryKey(v T) (any, uint64) {
	return getOptional(r.get, v), uint64(16 + unsafe.Sizeof(optionalValue[V]{}))
}
func (r *unaryEqRule[T, V]) localQueryKeyMatches(v T, key any) bool {
	want, ok := key.(optionalValue[V])
	return ok && want == getOptional(r.get, v)
}
func (r *unaryEqRule[T, V]) isCardinalityZero(v T) bool {
	return r.wildcard.IsEmpty() && r.matchingSet(getOptional(r.get, v)) == nil
}
func (r *unaryEqRule[T, V]) matchesID(v T, id uint32) bool {
	if r.wildcard.Contains(id) {
		return true
	}
	set := r.matchingSet(getOptional(r.get, v))
	return set != nil && set.contains(id)
}
func (*unaryEqRule[T, V]) directIDWork() uint64 { return allEqualityDirectIDWork }
func (r *unaryEqRule[T, V]) search(v T, dst *roaring.Bitmap, pool *bitmapPool) {
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
func (r *unaryEqRule[T, V]) addMatches(value optionalValue[V], dst *roaring.Bitmap) {
	dst.Or(r.wildcard)
	if set := r.matchingSet(value); set != nil {
		set.addTo(dst)
	}
}
func (r *unaryEqRule[T, V]) sharedWildcard() *roaring.Bitmap { return r.wildcard }
func (r *unaryEqRule[T, V]) visitEqualityResultBitmaps(visit func(*roaring.Bitmap)) {
	visit(r.wildcard)
	if r.set.bits != nil {
		visit(r.set.bits)
	}
}
func (r *unaryEqRule[T, V]) lookupEqualityResultComponents(v T) (*roaring.Bitmap, *roaring.Bitmap, bool) {
	set := r.matchingSet(getOptional(r.get, v))
	posting, deduplicable := equalitySetBitmap(set)
	return r.wildcard, posting, set == nil || deduplicable
}
func (r *unaryEqRule[T, V]) lookupEqualityClass(v T) uint32 {
	if set := r.matchingSet(getOptional(r.get, v)); set != nil {
		return set.class
	}
	return r.wildcardClass
}
func (r *unaryEqRule[T, V]) addConcreteMatches(v T, dst *roaring.Bitmap) {
	if set := r.matchingSet(getOptional(r.get, v)); set != nil {
		set.addTo(dst)
	}
}
func (r *unaryEqRule[T, V]) intersectConcreteMatches(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	intersectEqualitySet(r.matchingSet(getOptional(r.get, v)), dst, pool)
}
func (*unaryEqRule[T, V]) exclude(T, *roaring.Bitmap, *bitmapPool)      {}
func (*unaryEqRule[T, V]) collectBuildStatistics([]nodeBuildStatistics) {}
func (r *unaryEqRule[T, V]) prepareSearch() {
	prepareBitmapForSearch(r.wildcard)
	r.set.prepareSearch()
}
func (r *unaryEqRule[T, V]) internBitmaps(interner *bitmapInterner) {
	r.wildcardSource = interner.internSource(&r.wildcard)
	r.set.internBitmaps(interner)
}
func (r *unaryEqRule[T, V]) equalitySourceCount() int { return 2 }
func (r *unaryEqRule[T, V]) visitEqualitySources(visit func(equalitySourcePair)) {
	visit(equalitySourcePair{wildcard: r.wildcardSource})
	if r.set.source != 0 {
		visit(equalitySourcePair{wildcard: r.wildcardSource, posting: r.set.source})
	}
}
func (r *unaryEqRule[T, V]) assignEqualityClasses(classes map[equalitySourcePair]uint32) {
	r.wildcardClass = classes[equalitySourcePair{wildcard: r.wildcardSource}]
	if r.set.source != 0 {
		r.set.class = classes[equalitySourcePair{wildcard: r.wildcardSource, posting: r.set.source}]
	}
}

type binaryEqRule[T any, V comparable] struct {
	nodeID         nodeID
	get            Getter[T, V]
	wildcard       *roaring.Bitmap
	wildcardSource physicalSourceID
	wildcardClass  uint32
	keys           [2]V
	sets           [2]equalitySet
}

func (r *binaryEqRule[T, V]) runtimeNodeID() nodeID { return r.nodeID }

func (*binaryEqRule[T, V]) inspectionStrategy() string { return "equality-binary" }
func (r *binaryEqRule[T, V]) canonicalDescriptor() canonicalRuleDescriptor {
	return canonicalRuleDescriptor{representation: canonicalEquality, schema: r.nodeID}
}

func (*binaryEqRule[T, V]) rule()                                                 {}
func (r *binaryEqRule[T, V]) newState(*nodeIDAllocator, *buildStatistics) Rule[T] { return r }
func (*binaryEqRule[T, V]) validate(T) error                                      { return nil }
func (*binaryEqRule[T, V]) insert(T, uint32)                                      {}
func (r *binaryEqRule[T, V]) matchingSet(value optionalValue[V]) *equalitySet {
	if !value.ok {
		return nil
	}
	if value.value == r.keys[0] {
		return &r.sets[0]
	}
	if value.value == r.keys[1] {
		return &r.sets[1]
	}
	return nil
}
func (r *binaryEqRule[T, V]) cardinality(v T, _ *bitmapPool) uint64 {
	return r.estimateCardinality(v)
}
func (r *binaryEqRule[T, V]) estimateCardinality(v T) uint64 {
	n := r.wildcard.GetCardinality()
	if set := r.matchingSet(getOptional(r.get, v)); set != nil {
		n += set.cardinality()
	}
	return n
}
func (r *binaryEqRule[T, V]) estimateCheapCardinality(v T) uint64 {
	return r.estimateCardinality(v)
}
func (r *binaryEqRule[T, V]) lookupCachedBitmap(v T, pool *bitmapPool) (*roaring.Bitmap, bool) {
	return lookupEqualityCachedBitmap(pool, r.nodeID, getOptional(r.get, v))
}
func (r *binaryEqRule[T, V]) localQueryKey(v T) (any, uint64) {
	return getOptional(r.get, v), uint64(16 + unsafe.Sizeof(optionalValue[V]{}))
}
func (r *binaryEqRule[T, V]) localQueryKeyMatches(v T, key any) bool {
	want, ok := key.(optionalValue[V])
	return ok && want == getOptional(r.get, v)
}
func (r *binaryEqRule[T, V]) isCardinalityZero(v T) bool {
	return r.wildcard.IsEmpty() && r.matchingSet(getOptional(r.get, v)) == nil
}
func (r *binaryEqRule[T, V]) matchesID(v T, id uint32) bool {
	if r.wildcard.Contains(id) {
		return true
	}
	set := r.matchingSet(getOptional(r.get, v))
	return set != nil && set.contains(id)
}
func (*binaryEqRule[T, V]) directIDWork() uint64 { return allEqualityDirectIDWork }
func (r *binaryEqRule[T, V]) search(v T, dst *roaring.Bitmap, pool *bitmapPool) {
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
func (r *binaryEqRule[T, V]) addMatches(value optionalValue[V], dst *roaring.Bitmap) {
	dst.Or(r.wildcard)
	if set := r.matchingSet(value); set != nil {
		set.addTo(dst)
	}
}
func (r *binaryEqRule[T, V]) sharedWildcard() *roaring.Bitmap { return r.wildcard }
func (r *binaryEqRule[T, V]) visitEqualityResultBitmaps(visit func(*roaring.Bitmap)) {
	visit(r.wildcard)
	for i := range r.sets {
		if r.sets[i].bits != nil {
			visit(r.sets[i].bits)
		}
	}
}
func (r *binaryEqRule[T, V]) lookupEqualityResultComponents(v T) (*roaring.Bitmap, *roaring.Bitmap, bool) {
	set := r.matchingSet(getOptional(r.get, v))
	posting, deduplicable := equalitySetBitmap(set)
	return r.wildcard, posting, set == nil || deduplicable
}
func (r *binaryEqRule[T, V]) lookupEqualityClass(v T) uint32 {
	if set := r.matchingSet(getOptional(r.get, v)); set != nil {
		return set.class
	}
	return r.wildcardClass
}
func (r *binaryEqRule[T, V]) addConcreteMatches(v T, dst *roaring.Bitmap) {
	if set := r.matchingSet(getOptional(r.get, v)); set != nil {
		set.addTo(dst)
	}
}
func (r *binaryEqRule[T, V]) intersectConcreteMatches(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	intersectEqualitySet(r.matchingSet(getOptional(r.get, v)), dst, pool)
}
func (*binaryEqRule[T, V]) exclude(T, *roaring.Bitmap, *bitmapPool)      {}
func (*binaryEqRule[T, V]) collectBuildStatistics([]nodeBuildStatistics) {}
func (r *binaryEqRule[T, V]) prepareSearch() {
	prepareBitmapForSearch(r.wildcard)
	for i := range r.sets {
		r.sets[i].prepareSearch()
	}
}
func (r *binaryEqRule[T, V]) internBitmaps(interner *bitmapInterner) {
	r.wildcardSource = interner.internSource(&r.wildcard)
	for i := range r.sets {
		r.sets[i].internBitmaps(interner)
	}
}
func (r *binaryEqRule[T, V]) equalitySourceCount() int { return 3 }
func (r *binaryEqRule[T, V]) visitEqualitySources(visit func(equalitySourcePair)) {
	visit(equalitySourcePair{wildcard: r.wildcardSource})
	for i := range r.sets {
		set := r.sets[i]
		if set.source != 0 {
			visit(equalitySourcePair{wildcard: r.wildcardSource, posting: set.source})
		}
	}
}
func (r *binaryEqRule[T, V]) assignEqualityClasses(classes map[equalitySourcePair]uint32) {
	r.wildcardClass = classes[equalitySourcePair{wildcard: r.wildcardSource}]
	for i := range r.sets {
		set := &r.sets[i]
		if set.source != 0 {
			set.class = classes[equalitySourcePair{wildcard: r.wildcardSource, posting: set.source}]
		}
	}
}

// ternaryEqRule is the three-value counterpart of the unary and binary
// specializations. It replaces the build-time equality index completely, so
// the finished rule retains neither its map nor its slice and hint fields.

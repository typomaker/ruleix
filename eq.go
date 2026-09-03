package ruleix

import (
	"slices"
	"unsafe"

	"github.com/RoaringBitmap/roaring/v2"
)

// equalityArrayLimit is the largest equality posting list kept as a compact
// uint32 slice. Larger lists use Roaring, whose containers become more
// efficient once the fixed bitmap/object overhead is amortized.
const equalityArrayLimit = 32

// equalitySet avoids allocating a full Roaring bitmap for the overwhelmingly
// common one-ID equality value. Canonical IDs usually arrive in increasing
// order; repeated external IDs may arrive out of order, so add keeps the small
// representation sorted for binary duplicate checks and Roaring conversion.
type equalitySet struct {
	single uint32
	small  []uint32
	bits   *roaring.Bitmap
	source physicalSourceID
	class  uint32
}

func newEqualitySet(id uint32) *equalitySet { return &equalitySet{single: id} }

type equalityIndex[V comparable] struct {
	offsets map[V]uint32
	sets    []equalitySet
	keys    [3]V
	count   uint8
	hint    int
}

func newEqualityIndex[V comparable](capacity int) equalityIndex[V] {
	return equalityIndex[V]{sets: make([]equalitySet, 0, capacity), hint: capacity}
}

func (i *equalityIndex[V]) get(value V) *equalitySet {
	if i.offsets == nil {
		for index := range int(i.count) {
			if i.keys[index] == value {
				return &i.sets[index]
			}
		}
		return nil
	}
	offset, ok := i.offsets[value]
	if !ok {
		return nil
	}
	return &i.sets[offset]
}

func (i *equalityIndex[V]) add(value V, id uint32) {
	if set := i.get(value); set != nil {
		set.add(id)
		return
	}
	if i.offsets == nil && i.count < uint8(len(i.keys)) {
		i.keys[i.count] = value
		i.count++
		i.sets = append(i.sets, equalitySet{single: id})
		return
	}
	if i.offsets == nil {
		capacity := i.hint
		if capacity < len(i.keys)+1 {
			capacity = len(i.keys) + 1
		}
		i.offsets = make(map[V]uint32, capacity)
		for index := range int(i.count) {
			i.offsets[i.keys[index]] = uint32(index)
		}
	}
	i.sets = append(i.sets, equalitySet{single: id})
	i.offsets[value] = uint32(len(i.sets) - 1)
}

func (i *equalityIndex[V]) addBitmap(value V, id uint32) {
	if set := i.get(value); set != nil {
		if set.bits == nil {
			bits := roaring.New()
			set.addTo(bits)
			set.bits, set.small = bits, nil
		}
		set.bits.Add(id)
		return
	}
	i.addSet(value, &equalitySet{bits: roaring.BitmapOf(id)})
}

// addSet publishes a complete posting under an already transformed key.
// Collisions merge into the same equalitySet, just like repeated exact keys.
func (i *equalityIndex[V]) addSet(value V, incoming *equalitySet) {
	if set := i.get(value); set != nil {
		incoming.addToSet(set)
		return
	}
	if i.offsets == nil && i.count < uint8(len(i.keys)) {
		i.keys[i.count] = value
		i.count++
		i.sets = append(i.sets, incoming.clone())
		return
	}
	if i.offsets == nil {
		capacity := max(i.hint, len(i.keys)+1)
		i.offsets = make(map[V]uint32, capacity)
		for index := range int(i.count) {
			i.offsets[i.keys[index]] = uint32(index)
		}
	}
	i.sets = append(i.sets, incoming.clone())
	i.offsets[value] = uint32(len(i.sets) - 1)
}

func (s *equalitySet) clone() equalitySet {
	clone := *s
	clone.source, clone.class = 0, 0
	clone.small = slices.Clone(s.small)
	if s.bits != nil {
		clone.bits = s.bits.Clone()
	}
	return clone
}

func (i *equalityIndex[V]) visit(visit func(V, *equalitySet)) {
	if i.offsets == nil {
		for index := range int(i.count) {
			visit(i.keys[index], &i.sets[index])
		}
		return
	}
	for key, offset := range i.offsets {
		visit(key, &i.sets[offset])
	}
}

func (s *equalitySet) add(id uint32) {
	if s.bits != nil {
		s.bits.Add(id)
		return
	}
	if s.small == nil {
		if s.single == id {
			return
		}
		if s.single < id {
			s.small = []uint32{s.single, id}
		} else {
			s.small = []uint32{id, s.single}
		}
		return
	}
	if len(s.small) < equalityArrayLimit {
		if id > s.small[len(s.small)-1] {
			s.small = append(s.small, id)
			return
		}
		pos, found := slices.BinarySearch(s.small, id)
		if found {
			return
		}
		s.small = append(s.small, 0)
		copy(s.small[pos+1:], s.small[pos:])
		s.small[pos] = id
		return
	}
	if _, found := slices.BinarySearch(s.small, id); found {
		return
	}
	s.bits = roaring.New()
	// AddMany eagerly chooses a dense bitmap container for some sparse batches.
	// Incremental Add preserves Roaring's compact array container here.
	for _, existing := range s.small {
		s.bits.Add(existing)
	}
	s.bits.Add(id)
	s.small = nil
}

func (s *equalitySet) cardinality() uint64 {
	if s.bits != nil {
		return s.bits.GetCardinality()
	}
	if s.small != nil {
		return uint64(len(s.small))
	}
	return 1
}

func (s *equalitySet) addTo(dst *roaring.Bitmap) {
	if s.bits != nil {
		dst.Or(s.bits)
		return
	}
	if s.small != nil {
		for _, id := range s.small {
			dst.Add(id)
		}
		return
	}
	dst.Add(s.single)
}

func (s *equalitySet) addToSet(dst *equalitySet) {
	if dst.bits == nil {
		bits := roaring.New()
		dst.addTo(bits)
		dst.bits, dst.small = bits, nil
	}
	s.addTo(dst.bits)
}

// intersectEqualitySet keeps immutable bitmap postings on the direct And
// path. Only compact array/singleton postings need a temporary bitmap because
// Roaring cannot intersect a bitmap with those native representations.
func intersectEqualitySet(set *equalitySet, dst *roaring.Bitmap, pool *bitmapPool) {
	if set == nil {
		dst.Clear()
		return
	}
	if set.bits != nil {
		dst.And(set.bits)
		return
	}
	concrete := pool.get()
	set.addTo(concrete)
	dst.And(concrete)
	pool.put(concrete)
}

func (s *equalitySet) contains(id uint32) bool {
	if s.bits != nil {
		return s.bits.Contains(id)
	}
	if s.small != nil {
		_, found := slices.BinarySearch(s.small, id)
		return found
	}
	return s.single == id
}

func (s *equalitySet) prepareSearch() {
	if s.bits != nil {
		prepareBitmapForSearch(s.bits)
	}
}
func (s *equalitySet) internBitmaps(interner *bitmapInterner) {
	if s.bits != nil {
		s.source = interner.internSource(&s.bits)
	}
}

type eqRule[T any, V comparable, K comparable] struct {
	nodeID          nodeID
	get             Getter[T, V]
	wildcard        *roaring.Bitmap
	wildcardSource  physicalSourceID
	wildcardClass   uint32
	codec           equalityCodec[V]
	codecErr        error
	quantizer       equalityQuantizer
	encode          func(V, equalityQuantizer) K
	coarsen         func(K, equalityQuantizer) K
	less            func(K, K) bool
	firstGeneration func(*eqRule[T, V, K]) (uint64, Rule[T], bool)
	values          equalityIndex[K]
}

func (r *eqRule[T, V, K]) runtimeNodeID() nodeID { return r.nodeID }

func (*eqRule[T, V, K]) inspectionStrategy() string { return "equality" }
func (r *eqRule[T, V, K]) refreshedStreamingDetails(details inspectionDetails) inspectionDetails {
	return r.streamingEqualityDetails(details)
}

func (r *eqRule[T, V, K]) equalityKey(value V) K {
	return r.encode(value, r.quantizer)
}

func (*eqRule[T, V, K]) rule() {}
func (r *eqRule[T, V, K]) canonicalDescriptor() canonicalRuleDescriptor {
	return canonicalRuleDescriptor{representation: canonicalEquality, schema: r}
}
func (r *eqRule[T, V, K]) newState(ids *nodeIDAllocator, hints *buildStatistics) Rule[T] {
	id := ids.allocate()
	return &eqRule[T, V, K]{
		nodeID: id, get: r.get, codec: r.codec, codecErr: r.codecErr,
		encode: r.encode, coarsen: r.coarsen, less: r.less, firstGeneration: r.firstGeneration,
		wildcard: roaring.New(),
		values:   newEqualityIndex[K](capacityHint(hints.node(id).equalityValues)),
	}
}
func (r *eqRule[T, V, K]) validate(T) error { return r.codecErr }
func (r *eqRule[T, V, K]) insert(v T, id uint32) {
	value, ok := r.get(v)
	if !ok {
		r.wildcard.Add(id)
		return
	}
	r.values.add(r.equalityKey(value), id)
}
func (r *eqRule[T, V, K]) cardinality(v T, _ *bitmapPool) uint64 {
	return r.estimateCardinality(v)
}
func (r *eqRule[T, V, K]) estimateCardinality(v T) uint64 {
	n := r.wildcard.GetCardinality()
	if value, ok := r.get(v); ok {
		if set := r.values.get(r.equalityKey(value)); set != nil {
			n += set.cardinality()
		}
	}
	return n
}
func (r *eqRule[T, V, K]) estimateCheapCardinality(v T) uint64 { return r.estimateCardinality(v) }
func (r *eqRule[T, V, K]) lookupCachedBitmap(v T, pool *bitmapPool) (*roaring.Bitmap, bool) {
	return lookupEqualityCachedBitmap(pool, r.nodeID, getOptional(r.get, v))
}
func (r *eqRule[T, V, K]) localQueryKey(v T) (any, uint64) {
	return getOptional(r.get, v), uint64(16 + unsafe.Sizeof(optionalValue[V]{}))
}
func (r *eqRule[T, V, K]) localQueryKeyMatches(v T, key any) bool {
	want, ok := key.(optionalValue[V])
	return ok && want == getOptional(r.get, v)
}
func (r *eqRule[T, V, K]) isCardinalityZero(v T) bool {
	if !r.wildcard.IsEmpty() {
		return false
	}
	value, ok := r.get(v)
	return !ok || r.values.get(r.equalityKey(value)) == nil
}
func (r *eqRule[T, V, K]) matchesID(v T, id uint32) bool {
	if r.wildcard.Contains(id) {
		return true
	}
	value, ok := r.get(v)
	if !ok {
		return false
	}
	set := r.values.get(r.equalityKey(value))
	return set != nil && set.contains(id)
}
func (*eqRule[T, V, K]) directIDWork() uint64 { return allEqualityDirectIDWork }
func (r *eqRule[T, V, K]) search(v T, dst *roaring.Bitmap, pool *bitmapPool) {
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

func (r *eqRule[T, V, K]) addMatches(value optionalValue[V], dst *roaring.Bitmap) {
	key := optionalValue[K]{}
	if value.ok {
		key = optionalValue[K]{value: r.equalityKey(value.value), ok: true}
	}
	addEqualityMatches(r.wildcard, &r.values, key, dst)
}
func (r *eqRule[T, V, K]) sharedWildcard() *roaring.Bitmap { return r.wildcard }
func (r *eqRule[T, V, K]) visitEqualityResultBitmaps(visit func(*roaring.Bitmap)) {
	visit(r.wildcard)
	for i := range r.values.sets {
		if bits := r.values.sets[i].bits; bits != nil {
			visit(bits)
		}
	}
}
func (r *eqRule[T, V, K]) lookupEqualityResultComponents(v T) (*roaring.Bitmap, *roaring.Bitmap, bool) {
	value, ok := r.get(v)
	if !ok {
		return r.wildcard, nil, true
	}
	set := r.values.get(r.equalityKey(value))
	posting, deduplicable := equalitySetBitmap(set)
	return r.wildcard, posting, set == nil || deduplicable
}
func (r *eqRule[T, V, K]) lookupEqualityClass(v T) uint32 {
	value, ok := r.get(v)
	if !ok {
		return r.wildcardClass
	}
	if set := r.values.get(r.equalityKey(value)); set != nil {
		return set.class
	}
	return r.wildcardClass
}
func (r *eqRule[T, V, K]) addConcreteMatches(v T, dst *roaring.Bitmap) {
	value, ok := r.get(v)
	if ok {
		if set := r.values.get(r.equalityKey(value)); set != nil {
			set.addTo(dst)
		}
	}
}
func (r *eqRule[T, V, K]) intersectConcreteMatches(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	value, ok := r.get(v)
	if !ok {
		dst.Clear()
		return
	}
	intersectEqualitySet(r.values.get(r.equalityKey(value)), dst, pool)
}
func (*eqRule[T, V, K]) exclude(T, *roaring.Bitmap, *bitmapPool) {}
func (r *eqRule[T, V, K]) optimize(total uint64) Rule[T] {
	if r.wildcard.GetCardinality() == total {
		return newMatchAllRule[T](r.wildcard)
	}
	return r
}
func (r *eqRule[T, V, K]) collectBuildStatistics(stats []nodeBuildStatistics) {
	stats[r.nodeID].equalityValues = len(r.values.sets)
}
func (r *eqRule[T, V, K]) prepareSearch() {
	prepareBitmapForSearch(r.wildcard)
	for i := range r.values.sets {
		r.values.sets[i].prepareSearch()
	}
}
func (r *eqRule[T, V, K]) internBitmaps(interner *bitmapInterner) {
	r.wildcardSource = interner.internSource(&r.wildcard)
	for i := range r.values.sets {
		r.values.sets[i].internBitmaps(interner)
	}
}
func (r *eqRule[T, V, K]) equalitySourceCount() int { return 1 + len(r.values.sets) }
func (r *eqRule[T, V, K]) visitEqualitySources(visit func(equalitySourcePair)) {
	visit(equalitySourcePair{wildcard: r.wildcardSource})
	for i := range r.values.sets {
		set := r.values.sets[i]
		if set.source != 0 {
			visit(equalitySourcePair{wildcard: r.wildcardSource, posting: set.source})
		}
	}
}
func (r *eqRule[T, V, K]) assignEqualityClasses(classes map[equalitySourcePair]uint32) {
	r.wildcardClass = classes[equalitySourcePair{wildcard: r.wildcardSource}]
	for i := range r.values.sets {
		set := &r.values.sets[i]
		if set.source != 0 {
			set.class = classes[equalitySourcePair{wildcard: r.wildcardSource, posting: set.source}]
		}
	}
}

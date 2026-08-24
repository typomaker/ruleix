package ruleix

import (
	"fmt"
	"slices"

	"github.com/RoaringBitmap/roaring/v2"
)

func (r *eqRule[T, V]) compileLossy(limit uint64) (Rule[T], error) {
	return r.newLossyAllPlanner().compile(limit)
}

type equalityLossyAllPlanner[T any, V comparable] struct {
	representations []Rule[T]
	exact           Rule[T]
	prepare         func() []Rule[T]
	err             error
}

func (r *eqRule[T, V]) newLossyAllPlanner() lossyAllPlanner[T] {
	// V1 accounting: wildcard payload, 16 bytes of strategy metadata, and for
	// each occupied bucket an 8-byte key, 8-byte logical slot, and payload.
	exact := uint64(16) + bitmapBytes(r.wildcard)
	items := r.wildcard.GetCardinality()
	distinct := uint64(0)
	type hashedSet struct {
		hash uint64
		set  *equalitySet
	}
	hashed := make([]hashedSet, 0, len(r.values.sets))
	valid := true
	addHash := func(value V, set *equalitySet) {
		hash, ok := hashScalar(any(value))
		if !ok {
			valid = false
			return
		}
		hashed = append(hashed, hashedSet{hash: hash, set: set})
	}
	if r.values.offsets == nil {
		for n := range int(r.values.count) {
			items += equalitySetCardinality(&r.values.sets[n])
			distinct++
			addHash(r.values.keys[n], &r.values.sets[n])
			encoded, ok := canonicalScalar(nil, any(r.values.keys[n]))
			if !ok {
				continue
			}
			exact += uint64(len(encoded)) + 8 + equalitySetBytes(&r.values.sets[n])
		}
	} else {
		for value, offset := range r.values.offsets {
			items += equalitySetCardinality(&r.values.sets[offset])
			distinct++
			addHash(value, &r.values.sets[offset])
			encoded, ok := canonicalScalar(nil, any(value))
			if !ok {
				continue
			}
			exact += uint64(len(encoded)) + 8 + equalitySetBytes(&r.values.sets[offset])
		}
	}
	exactRepresentation := Rule[T](&inspectionDetailsRule[T]{
		child:   r,
		details: representationDetails(exact, items, distinct, 0, false),
	})
	planner := &equalityLossyAllPlanner[T, V]{exact: exactRepresentation}
	if !valid {
		planner.err = fmt.Errorf("ruleix: Lossy equality requires a supported scalar value type")
		return planner
	}
	planner.prepare = func() []Rule[T] {
		representations := make([]Rule[T], 0, lossyMaxBucketBits+1)
		bucketItems := make(map[uint64]uint64)
		var sameValuePairs float64
		for _, value := range hashed {
			count := equalitySetCardinality(value.set)
			sameValuePairs += float64(count) * float64(count)
		}
		concreteItems := items - r.wildcard.GetCardinality()
		allDifferentValuePairs := float64(concreteItems)*float64(concreteItems) - sameValuePairs
		for bits := uint(0); bits <= lossyMaxBucketBits; bits++ {
			clear(bucketItems)
			candidate := &lossyEqualityRule[T, V]{
				nodeID: r.nodeID, get: r.get, wildcard: r.wildcard,
				shift: 64 - bits, buckets: make(map[uint64]*roaring.Bitmap),
			}
			for _, value := range hashed {
				bucket := value.hash >> candidate.shift
				count := equalitySetCardinality(value.set)
				bucketItems[bucket] += count
				posting := candidate.buckets[bucket]
				if posting == nil {
					posting = roaring.New()
					candidate.buckets[bucket] = posting
				}
				value.set.addTo(posting)
			}
			usage := uint64(16) + bitmapBytes(candidate.wildcard)
			for _, posting := range candidate.buckets {
				usage += 16 + bitmapBytes(posting)
			}
			details := representationDetails(usage, items, distinct, uint64(len(candidate.buckets)), true)
			if allDifferentValuePairs > 0 {
				var collidingDifferentValuePairs float64
				for _, count := range bucketItems {
					collidingDifferentValuePairs += float64(count) * float64(count)
				}
				collidingDifferentValuePairs -= sameValuePairs
				details.EstimatedFalsePositiveRateValue = collidingDifferentValuePairs / allDifferentValuePairs
				details.EstimatedFalsePositiveRateAvailable = true
			}
			representations = append(representations, &inspectionDetailsRule[T]{child: candidate, details: details})
		}
		return representations
	}
	return planner
}

func (p *equalityLossyAllPlanner[T, V]) compile(limit uint64) (Rule[T], error) {
	if inspectionDetailsOf(p.exact).MemoryUsageBytes <= limit {
		return p.exact, nil
	}
	if p.err != nil {
		return nil, p.err
	}
	if p.representations == nil {
		p.representations = p.prepare()
		p.prepare = nil
	}
	var selected Rule[T]
	for _, candidate := range p.representations {
		if inspectionDetailsOf(candidate).MemoryUsageBytes <= limit {
			selected = candidate
		} else {
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("ruleix: Lossy equality cannot fit the memory limit")
	}
	return selected, nil
}

func equalitySetCardinality(s *equalitySet) uint64 {
	if s.bits != nil {
		return s.bits.GetCardinality()
	}
	if s.small != nil {
		return uint64(len(s.small))
	}
	return 1
}

func equalitySetBytes(s *equalitySet) uint64 {
	if s.bits != nil {
		return bitmapBytes(s.bits)
	}
	if s.small != nil {
		return uint64(len(s.small)) * 4
	}
	return 4
}

// Include matches a query field when it equals the stored field. A missing
// stored value is a wildcard and matches every concrete query value; a missing
// query value matches only stored wildcards.
//
// For example, to match a rule's optional country:
//
//	ruleix.Include(func(c Constraint) (string, bool) { return c.Country, true })
func Include[T any, V comparable](get Getter[T, V]) Rule[T] {
	return &eqRule[T, V]{get: get}
}

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
}

func newEqualitySet(id uint32) *equalitySet { return &equalitySet{single: id} }

type equalityIndex[V comparable] struct {
	offsets map[V]uint32
	sets    []equalitySet
	keys    [2]V
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
		interner.intern(&s.bits)
	}
}

type eqRule[T any, V comparable] struct {
	nodeID   nodeID
	get      Getter[T, V]
	wildcard *roaring.Bitmap
	values   equalityIndex[V]
}

func (*eqRule[T, V]) inspectionStrategy() string { return "equality" }

func (*eqRule[T, V]) rule() {}
func (r *eqRule[T, V]) newState(ids *nodeIDAllocator, hints *buildStatistics) Rule[T] {
	id := ids.allocate()
	return &eqRule[T, V]{
		nodeID: id, get: r.get,
		wildcard: roaring.New(), values: newEqualityIndex[V](capacityHint(hints.node(id).equalityValues)),
	}
}
func (*eqRule[T, V]) validate(T) error { return nil }
func (r *eqRule[T, V]) insert(v T, id uint32) {
	value, ok := r.get(v)
	if !ok {
		r.wildcard.Add(id)
		return
	}
	r.values.add(value, id)
}
func (r *eqRule[T, V]) cardinality(v T, _ *bitmapPool) uint64 {
	return r.estimateCardinality(v)
}
func (r *eqRule[T, V]) estimateCardinality(v T) uint64 {
	n := r.wildcard.GetCardinality()
	if value, ok := r.get(v); ok {
		if set := r.values.get(value); set != nil {
			n += set.cardinality()
		}
	}
	return n
}
func (r *eqRule[T, V]) estimateCheapCardinality(v T) uint64 { return r.estimateCardinality(v) }
func (r *eqRule[T, V]) isCardinalityZero(v T) bool {
	if !r.wildcard.IsEmpty() {
		return false
	}
	value, ok := r.get(v)
	return !ok || r.values.get(value) == nil
}
func (r *eqRule[T, V]) matchesID(v T, id uint32) bool {
	if r.wildcard.Contains(id) {
		return true
	}
	value, ok := r.get(v)
	if !ok {
		return false
	}
	set := r.values.get(value)
	return set != nil && set.contains(id)
}
func (r *eqRule[T, V]) search(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	value := getOptional(r.get, v)
	if pool.local == nil {
		r.addMatches(value, dst)
		return
	}

	node := &pool.local[int(r.nodeID)]
	cache, _ := node.equality.(*valueBitmapCache[V])
	if cache == nil {
		cache = newValueBitmapCache[V](pool)
		node.equality = cache
	}
	if bits, found := comparableValueCacheLookup(cache, value); found {
		dst.Or(bits)
		return
	}
	if !comparableValueCacheAdmit(cache, value) {
		r.addMatches(value, dst)
		return
	}

	bits := cache.replace(value)
	r.addMatches(value, bits)
	dst.Or(bits)
}

func (r *eqRule[T, V]) addMatches(value optionalValue[V], dst *roaring.Bitmap) {
	dst.Or(r.wildcard)
	if value.ok {
		if set := r.values.get(value.value); set != nil {
			set.addTo(dst)
		}
	}
}
func (r *eqRule[T, V]) sharedWildcard() *roaring.Bitmap { return r.wildcard }
func (r *eqRule[T, V]) addConcreteMatches(v T, dst *roaring.Bitmap) {
	value, ok := r.get(v)
	if ok {
		if set := r.values.get(value); set != nil {
			set.addTo(dst)
		}
	}
}
func (*eqRule[T, V]) exclude(T, *roaring.Bitmap, *bitmapPool) {}
func (r *eqRule[T, V]) optimize(total uint64) Rule[T] {
	if r.wildcard.GetCardinality() == total {
		return newMatchAllRule[T](r.wildcard)
	}
	switch len(r.values.sets) {
	case 1:
		return &unaryEqRule[T, V]{
			nodeID: r.nodeID, get: r.get, wildcard: r.wildcard,
			key: r.values.keys[0], set: r.values.sets[0],
		}
	case 2:
		return &binaryEqRule[T, V]{
			nodeID: r.nodeID, get: r.get, wildcard: r.wildcard,
			keys: r.values.keys, sets: [2]equalitySet{r.values.sets[0], r.values.sets[1]},
		}
	}
	return r
}
func (r *eqRule[T, V]) collectBuildStatistics(stats []nodeBuildStatistics) {
	stats[r.nodeID].equalityValues = len(r.values.sets)
}
func (r *eqRule[T, V]) prepareSearch() {
	prepareBitmapForSearch(r.wildcard)
	for i := range r.values.sets {
		r.values.sets[i].prepareSearch()
	}
}
func (r *eqRule[T, V]) internBitmaps(interner *bitmapInterner) {
	interner.intern(&r.wildcard)
	for i := range r.values.sets {
		r.values.sets[i].internBitmaps(interner)
	}
}

// unaryEqRule and binaryEqRule are immutable build-time specializations for
// low-cardinality equality filters. Besides avoiding a map lookup, they discard
// the slice and capacity retained by the general equality index.
type unaryEqRule[T any, V comparable] struct {
	nodeID   nodeID
	get      Getter[T, V]
	wildcard *roaring.Bitmap
	key      V
	set      equalitySet
}

func (*unaryEqRule[T, V]) inspectionStrategy() string { return "equality-unary" }

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
	bits := cache.replace(value)
	r.addMatches(value, bits)
	dst.Or(bits)
}
func (r *unaryEqRule[T, V]) addMatches(value optionalValue[V], dst *roaring.Bitmap) {
	dst.Or(r.wildcard)
	if set := r.matchingSet(value); set != nil {
		set.addTo(dst)
	}
}
func (r *unaryEqRule[T, V]) sharedWildcard() *roaring.Bitmap { return r.wildcard }
func (r *unaryEqRule[T, V]) addConcreteMatches(v T, dst *roaring.Bitmap) {
	if set := r.matchingSet(getOptional(r.get, v)); set != nil {
		set.addTo(dst)
	}
}
func (*unaryEqRule[T, V]) exclude(T, *roaring.Bitmap, *bitmapPool)      {}
func (*unaryEqRule[T, V]) collectBuildStatistics([]nodeBuildStatistics) {}
func (r *unaryEqRule[T, V]) prepareSearch() {
	prepareBitmapForSearch(r.wildcard)
	r.set.prepareSearch()
}
func (r *unaryEqRule[T, V]) internBitmaps(interner *bitmapInterner) {
	interner.intern(&r.wildcard)
	r.set.internBitmaps(interner)
}

type binaryEqRule[T any, V comparable] struct {
	nodeID   nodeID
	get      Getter[T, V]
	wildcard *roaring.Bitmap
	keys     [2]V
	sets     [2]equalitySet
}

func (*binaryEqRule[T, V]) inspectionStrategy() string { return "equality-binary" }

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
	bits := cache.replace(value)
	r.addMatches(value, bits)
	dst.Or(bits)
}
func (r *binaryEqRule[T, V]) addMatches(value optionalValue[V], dst *roaring.Bitmap) {
	dst.Or(r.wildcard)
	if set := r.matchingSet(value); set != nil {
		set.addTo(dst)
	}
}
func (r *binaryEqRule[T, V]) sharedWildcard() *roaring.Bitmap { return r.wildcard }
func (r *binaryEqRule[T, V]) addConcreteMatches(v T, dst *roaring.Bitmap) {
	if set := r.matchingSet(getOptional(r.get, v)); set != nil {
		set.addTo(dst)
	}
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
	interner.intern(&r.wildcard)
	for i := range r.sets {
		r.sets[i].internBitmaps(interner)
	}
}

func equalityCache[V comparable](pool *bitmapPool, id nodeID) *valueBitmapCache[V] {
	node := &pool.local[int(id)]
	cache, _ := node.equality.(*valueBitmapCache[V])
	if cache == nil {
		cache = newValueBitmapCache[V](pool)
		node.equality = cache
	}
	return cache
}

package ruleix

import (
	"slices"

	"github.com/RoaringBitmap/roaring/v2"
)

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
	n := r.wildcard.GetCardinality()
	if value, ok := r.get(v); ok {
		if set := r.values.get(value); set != nil {
			n += set.cardinality()
		}
	}
	return n
}
func (r *eqRule[T, V]) isCardinalityZero(v T) bool {
	if !r.wildcard.IsEmpty() {
		return false
	}
	value, ok := r.get(v)
	return !ok || r.values.get(value) == nil
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
		cache = &valueBitmapCache[V]{}
		node.equality = cache
	}
	if bits, found := comparableValueCacheLookup(cache, value); found {
		dst.Or(bits)
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
func (*eqRule[T, V]) exclude(T, *roaring.Bitmap, *bitmapPool) {}
func (r *eqRule[T, V]) optimize(total uint64) Rule[T] {
	if r.wildcard.GetCardinality() == total {
		return newMatchAllRule[T](r.wildcard)
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

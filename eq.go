package ruleix

import (
	"slices"

	"github.com/RoaringBitmap/roaring/v2"
)

// Include matches a query field when it equals the stored field. A nil stored
// value is a wildcard and matches every concrete query value; a nil query value
// matches only stored wildcards.
//
// For example, to match a rule's optional country:
//
//	ruleix.Include(func(c Constraint) *string { return c.Country })
func Include[T any, V comparable](get func(T) *V) Rule[T] {
	return &eqRule[T, V]{get: get, wildcard: roaring.New(), values: make(map[V]*equalitySet)}
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

type eqRule[T any, V comparable] struct {
	get      func(T) *V
	wildcard *roaring.Bitmap
	values   map[V]*equalitySet
}

func (*eqRule[T, V]) rule()            {}
func (*eqRule[T, V]) validate(T) error { return nil }
func (r *eqRule[T, V]) insert(v T, id uint32) {
	value := r.get(v)
	if value == nil {
		r.wildcard.Add(id)
		return
	}
	set := r.values[*value]
	if set == nil {
		r.values[*value] = newEqualitySet(id)
		return
	}
	set.add(id)
}
func (r *eqRule[T, V]) cardinality(v T, _ *bitmapPool) uint64 {
	n := r.wildcard.GetCardinality()
	if value := r.get(v); value != nil {
		if set := r.values[*value]; set != nil {
			n += set.cardinality()
		}
	}
	return n
}
func (r *eqRule[T, V]) search(v T, dst *roaring.Bitmap, _ *bitmapPool) {
	dst.Or(r.wildcard)
	if value := r.get(v); value != nil {
		if set := r.values[*value]; set != nil {
			set.addTo(dst)
		}
	}
}
func (*eqRule[T, V]) exclude(T, *roaring.Bitmap, *bitmapPool) {}

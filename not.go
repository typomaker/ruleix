package ruleix

import "github.com/RoaringBitmap/roaring/v2"

// Exclude indexes an optional forbidden value. Nil means no forbidden value. A
// concrete match excludes the associated result ID even when another stored
// constraint with that ID matches.
func Exclude[T any, V comparable](get func(T) *V) Rule[T] {
	return &notRule[T, V]{
		get:         get,
		wildcard:    roaring.New(),
		constrained: roaring.New(),
		values:      make(map[V]*equalitySet),
	}
}

type notRule[T any, V comparable] struct {
	get         func(T) *V
	wildcard    *roaring.Bitmap
	constrained *roaring.Bitmap
	values      map[V]*equalitySet
}

func (*notRule[T, V]) rule()            {}
func (*notRule[T, V]) validate(T) error { return nil }
func (r *notRule[T, V]) insert(v T, id uint32) {
	value := r.get(v)
	if value == nil {
		r.wildcard.Add(id)
		return
	}
	r.constrained.Add(id)
	set := r.values[*value]
	if set == nil {
		r.values[*value] = newEqualitySet(id)
		return
	}
	set.add(id)
}
func (r *notRule[T, V]) cardinality(T, *bitmapPool) uint64 {
	return r.wildcard.GetCardinality() + r.constrained.GetCardinality()
}
func (r *notRule[T, V]) search(_ T, dst *roaring.Bitmap, _ *bitmapPool) {
	dst.Or(r.wildcard)
	dst.Or(r.constrained)
}
func (r *notRule[T, V]) exclude(v T, dst *roaring.Bitmap, _ *bitmapPool) {
	value := r.get(v)
	if value == nil {
		return
	}
	if set := r.values[*value]; set != nil {
		set.addTo(dst)
	}
}

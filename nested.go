package ruleix

import "github.com/RoaringBitmap/roaring/v2"

// Nested applies child to an optional nested value. A nil stored value is a wildcard.
func Nested[T any, V any](get func(T) *V, child Rule[V]) Rule[T] {
	return &nestedRule[T, V]{get: get, wildcard: roaring.New(), child: child}
}

// Optional is an expressive alias for Nested.
func Optional[T any, V any](get func(T) *V, child Rule[V]) Rule[T] {
	return Nested(get, child)
}

type nestedRule[T any, V any] struct {
	get      func(T) *V
	wildcard *roaring.Bitmap
	child    Rule[V]
}

func (*nestedRule[T, V]) rule() {}
func (r *nestedRule[T, V]) validate(v T) error {
	value := r.get(v)
	if value == nil {
		return nil
	}
	return r.child.validate(*value)
}
func (r *nestedRule[T, V]) insert(v T, id uint32) {
	value := r.get(v)
	if value == nil {
		r.wildcard.Add(id)
		return
	}
	r.child.insert(*value, id)
}
func (r *nestedRule[T, V]) cardinality(v T, pool *bitmapPool) uint64 {
	n := r.wildcard.GetCardinality()
	if value := r.get(v); value != nil {
		n += r.child.cardinality(*value, pool)
	}
	return n
}
func (r *nestedRule[T, V]) search(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	dst.Or(r.wildcard)
	value := r.get(v)
	if value == nil {
		return
	}
	tmp := pool.get()
	r.child.search(*value, tmp, pool)
	dst.Or(tmp)
	pool.put(tmp)
}
func (r *nestedRule[T, V]) exclude(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	if value := r.get(v); value != nil {
		r.child.exclude(*value, dst, pool)
	}
}

// Package ruleix implements a strongly typed in-memory rule index.
package ruleix

import "github.com/RoaringBitmap/roaring/v2"

// Compare returns a negative number when a < b, zero when a == b, and a
// positive number when a > b.
type Compare[V any] func(a, b V) int

// Rule is a type-safe index node accepting values of T.
type Rule[T any] interface {
	rule()
	validate(T) error
	insert(T, uint32)
	cardinality(T, *bitmapPool) uint64
	search(T, *roaring.Bitmap, *bitmapPool)
	exclude(T, *roaring.Bitmap, *bitmapPool)
}

func measuredCardinality[T any](r Rule[T], value T, pool *bitmapPool) uint64 {
	bm := pool.get()
	r.search(value, bm, pool)
	n := bm.GetCardinality()
	pool.put(bm)
	return n
}

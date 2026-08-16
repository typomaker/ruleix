// Package ruleix implements a strongly typed in-memory rule index.
package ruleix

import "github.com/RoaringBitmap/roaring/v2"

// Compare defines an ordering for values used by ordered filters. It returns a
// negative number when a < b, zero when a == b, and a positive number when
// a > b. The standard library's cmp.Compare is suitable for ordered types.
type Compare[V any] func(a, b V) int

// Rule describes how constraints and query values of T are matched. Construct
// rules with Include, Exclude, the ordered filters, Between, CompareBy, and All.
// Its implementation is sealed so an index can rely on all rule invariants.
type Rule[T any] interface {
	rule()
	newState(*nodeIDAllocator) Rule[T]
	validate(T) error
	insert(T, uint32)
	cardinality(T, *bitmapPool) uint64
	search(T, *roaring.Bitmap, *bitmapPool)
	exclude(T, *roaring.Bitmap, *bitmapPool)
}

type nodeID uint32

type nodeIDAllocator struct{ next nodeID }

func (a *nodeIDAllocator) allocate() nodeID {
	id := a.next
	a.next++
	return id
}

func measuredCardinality[T any](r Rule[T], value T, pool *bitmapPool) uint64 {
	bm := pool.get()
	r.search(value, bm, pool)
	n := bm.GetCardinality()
	pool.put(bm)
	return n
}

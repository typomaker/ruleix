package ruleix

import (
	"sync"

	"github.com/RoaringBitmap/roaring/v2"
)

type bitmapPool struct {
	pool       sync.Pool
	rankedPool sync.Pool
	equality   []any
	ordered    []any
}

// maxPooledBitmapBytes bounds the live Roaring container memory represented by
// a bitmap returned to the pool. Bitmap.Clear deliberately retains the backing
// arrays of the top-level container index, so pooling an unusually wide bitmap
// can otherwise make a short-lived search burst raise the pool's memory floor.
// 64 KiB keeps the common small and medium scratch bitmaps reusable while
// discarding bitmaps large enough to have accumulated many containers.
const maxPooledBitmapBytes = 64 << 10

type rankedBitmap struct {
	bits *roaring.Bitmap
	card uint64
}

func newBitmapPool() *bitmapPool {
	p := &bitmapPool{}
	p.pool.New = func() interface{} { return roaring.New() }
	p.rankedPool.New = func() interface{} { return make([]rankedBitmap, 0, 16) }
	return p
}

func newLocalBitmapPool(nodes int) *bitmapPool {
	p := newBitmapPool()
	p.equality = make([]any, nodes)
	p.ordered = make([]any, nodes)
	return p
}
func (p *bitmapPool) get() *roaring.Bitmap {
	bm := p.pool.Get().(*roaring.Bitmap)
	bm.Clear()
	return bm
}
func (p *bitmapPool) put(bm *roaring.Bitmap) {
	if bm.GetSizeInBytes() > maxPooledBitmapBytes {
		return
	}
	bm.Clear()
	p.pool.Put(bm)
}
func (p *bitmapPool) getRanked(n int) []rankedBitmap {
	ranked := p.rankedPool.Get().([]rankedBitmap)
	if cap(ranked) < n {
		return make([]rankedBitmap, n)
	}
	return ranked[:n]
}
func (p *bitmapPool) putRanked(ranked []rankedBitmap) {
	clear(ranked)
	p.rankedPool.Put(ranked[:0])
}

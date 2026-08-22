package ruleix

import (
	"sync"

	"github.com/RoaringBitmap/roaring/v2"
)

type bitmapPool struct {
	pool       sync.Pool
	rankedPool sync.Pool
	local      []localNodeCache
}

// maxPooledBitmapBytes bounds the live Roaring container memory represented by
// a bitmap returned to the pool. Bitmap.Clear deliberately retains the backing
// arrays of the top-level container index, so pooling an unusually wide bitmap
// can otherwise make a short-lived search burst raise the pool's memory floor.
// 64 KiB keeps the common small and medium scratch bitmaps reusable while
// discarding bitmaps large enough to have accumulated many containers.
const maxPooledBitmapBytes = 64 << 10

type rankedBitmap struct {
	bits     *roaring.Bitmap
	card     uint64
	childIdx int
}

type rankedBitmapBuffer struct {
	items []rankedBitmap
}

func newBitmapPool() *bitmapPool {
	p := &bitmapPool{}
	p.pool.New = func() interface{} { return roaring.New() }
	p.rankedPool.New = func() interface{} {
		return &rankedBitmapBuffer{items: make([]rankedBitmap, 0, 16)}
	}
	return p
}

func newLocalBitmapPool(nodes int) *bitmapPool {
	p := newBitmapPool()
	p.local = make([]localNodeCache, nodes)
	return p
}
func (p *bitmapPool) get() *roaring.Bitmap {
	bm := p.pool.Get().(*roaring.Bitmap)
	bm.Clear()
	bm.SetCopyOnWrite(true)
	return bm
}
func (p *bitmapPool) put(bm *roaring.Bitmap) {
	if bm.GetSizeInBytes() > maxPooledBitmapBytes {
		return
	}
	bm.Clear()
	p.pool.Put(bm)
}
func (p *bitmapPool) getRanked(n int) *rankedBitmapBuffer {
	buffer := p.rankedPool.Get().(*rankedBitmapBuffer)
	if cap(buffer.items) < n {
		buffer.items = make([]rankedBitmap, n)
	} else {
		buffer.items = buffer.items[:n]
	}
	return buffer
}
func (p *bitmapPool) putRanked(buffer *rankedBitmapBuffer) {
	clear(buffer.items)
	buffer.items = buffer.items[:0]
	p.rankedPool.Put(buffer)
}

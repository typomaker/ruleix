package ruleix

import (
	"sync"

	"github.com/RoaringBitmap/roaring/v2"
)

type bitmapPool struct {
	pool            sync.Pool
	rankedPool      sync.Pool
	local           []localNodeCache
	allPlans        map[any]*localAllPlan
	allResultBytes  uint64
	childCacheBytes uint64
	cacheEpoch      uint64
	observers       cacheObservers // test-only fallback for caches without a compiled node ID
	inspectors      localInspectorRuntimeChunk
	nodeObservers   []cacheObservers
	rootRuntime     *inspectorRuntime
	rootObserver    inspectorRuntimeObserver
	observeRuntime  bool
	samplePlanner   bool
	explorePlanner  bool
	plannerSnapshot *plannerProfileSnapshot
	plannerOverlay  plannerProfileOverlay
}

type localInspectorRuntime struct {
	shared *inspectorRuntime
	values inspectorRuntimeValues
}

type localInspectorRuntimeChunk struct {
	items [8]localInspectorRuntime
	n     uint8
	next  *localInspectorRuntimeChunk
}

func (p *bitmapPool) inspectorObserver(shared *inspectorRuntime) inspectorRuntimeObserver {
	if p.local == nil {
		return inspectorRuntimeObserver{shared: shared}
	}
	chunk := &p.inspectors
	for {
		for i := range int(chunk.n) {
			if chunk.items[i].shared == shared {
				return inspectorRuntimeObserver{shared: shared, local: &chunk.items[i].values}
			}
		}
		if int(chunk.n) < len(chunk.items) {
			i := chunk.n
			chunk.n++
			chunk.items[i].shared = shared
			return inspectorRuntimeObserver{shared: shared, local: &chunk.items[i].values}
		}
		if chunk.next == nil {
			chunk.next = &localInspectorRuntimeChunk{}
		}
		chunk = chunk.next
	}
}

func (p *bitmapPool) flushInspectorMetrics() {
	for chunk := &p.inspectors; chunk != nil; chunk = chunk.next {
		for i := range int(chunk.n) {
			entry := &chunk.items[i]
			v, dst := &entry.values, entry.shared
			dst.candidateChecks.Add(v.candidateChecks)
			dst.rangePrunings.Add(v.rangePrunings)
			dst.emptyResults.Add(v.emptyResults)
			dst.cacheHits.Add(v.cacheHits)
			dst.cacheMisses.Add(v.cacheMisses)
			dst.cacheAdmissions.Add(v.cacheAdmissions)
			dst.cacheEvictions.Add(v.cacheEvictions)
			dst.cacheExpansions.Add(v.cacheExpansions)
			for bucket := range v.cardinality {
				dst.cardinality[bucket].Add(v.cardinality[bucket])
			}
			entry.values = inspectorRuntimeValues{}
		}
	}
}

// maxPooledBitmapBytes bounds the live Roaring container memory represented by
// a bitmap returned to the pool. Bitmap.Clear deliberately retains the backing
// arrays of the top-level container index, so pooling an unusually wide bitmap
// can otherwise make a short-lived search burst raise the pool's memory floor.
// 64 KiB keeps the common small and medium scratch bitmaps reusable while
// discarding bitmaps large enough to have accumulated many containers.
const maxPooledBitmapBytes = 64 << 10

// maxLocalAllResultBytes bounds exact-intersection bitmaps retained by one
// Local across every compiled All node. Plans remain cheap and schema-bounded;
// result payloads depend on query cardinality and need a separate byte budget.
const maxLocalAllResultBytes = 64 << 10

// maxLocalChildCacheBytes bounds materialized filter results retained across
// every node in one Local. Entry-count limits alone are insufficient when a
// repeated query produces a very large bitmap.
const maxLocalChildCacheBytes = 1 << 20

type rankedBitmap struct {
	bits     *roaring.Bitmap
	card     uint64
	childIdx int
	owned    bool
}

type rankedBitmapBuffer struct {
	items []rankedBitmap
}

func newBitmapPool() *bitmapPool {
	p := &bitmapPool{observeRuntime: true}
	p.pool.New = func() interface{} { return roaring.New() }
	p.rankedPool.New = func() interface{} {
		return &rankedBitmapBuffer{items: make([]rankedBitmap, 0, 16)}
	}
	return p
}

func newLocalBitmapPool(nodes int, configured ...[][]*inspectorRuntime) *bitmapPool {
	p := newBitmapPool()
	p.observeRuntime = false
	p.local = make([]localNodeCache, nodes)
	var inspectors [][]*inspectorRuntime
	if len(configured) != 0 {
		inspectors = configured[0]
	}
	if inspectors != nil {
		p.observeRuntime = true
		p.nodeObservers = make([]cacheObservers, nodes)
		for id, runtimes := range inspectors {
			for _, runtime := range runtimes {
				p.nodeObservers[id].push(p.inspectorObserver(runtime))
			}
		}
	}
	return p
}

func (p *bitmapPool) observersFor(id nodeID) *cacheObservers {
	if int(id) >= len(p.nodeObservers) || p.nodeObservers[id].n == 0 {
		return nil
	}
	observers := p.nodeObservers[id]
	return &observers
}

func (p *bitmapPool) bindRootInspector(runtime *inspectorRuntime) {
	if runtime == nil || p.rootRuntime == runtime {
		return
	}
	p.rootRuntime = runtime
	p.observeRuntime = true
	p.rootObserver = p.inspectorObserver(runtime)
}

func (p *bitmapPool) rootInspectorObserver(runtime *inspectorRuntime) inspectorRuntimeObserver {
	if p.local != nil && p.rootRuntime == runtime {
		return p.rootObserver
	}
	return p.inspectorObserver(runtime)
}
func (p *bitmapPool) resetLocal() {
	p.flushInspectorMetrics()
	for _, plan := range p.allPlans {
		plan.resetResults(p)
	}
	p.cacheEpoch++
	for i := range p.local {
		p.local[i].reset(p)
	}
	p.childCacheBytes = 0
	p.samplePlanner = false
	p.explorePlanner = false
	p.plannerSnapshot = nil
	p.plannerOverlay = plannerProfileOverlay{}
}

func (p *bitmapPool) invalidateResultCache() { p.cacheEpoch++ }
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

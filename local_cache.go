package ruleix

import "github.com/RoaringBitmap/roaring/v2"

type localNodeCache struct {
	equality  any
	ordered   any
	compareBy any
	between   any
	exclusion any
}

type localAllPlan struct {
	order     []int
	firstCard uint64
	bytes     uint64
	// Keep a bounded exact-intersection working set alongside the learned child
	// order. Repeated Local queries can share their immutable containers with a
	// scratch result instead of cloning the first posting on every search.
	results [4]localAllResult
	next    uint8
	valid   bool
}

type localAllResult struct {
	inputs []*roaring.Bitmap
	ids    []uint32
	keys   []any
	epoch  uint64
	bits   *roaring.Bitmap
	bytes  uint64
	idsSet bool
}

func (p *localAllPlan) resetResults(pool *bitmapPool) {
	for i := range p.results {
		result := &p.results[i]
		if result.bits != nil {
			pool.put(result.bits)
		}
		pool.allResultBytes -= result.bytes
		*result = localAllResult{}
	}
	p.next = 0
}

type localCacheResetter interface {
	reset(*bitmapPool)
}

func (c *localNodeCache) reset(pool *bitmapPool) {
	for _, cached := range [...]any{c.equality, c.ordered, c.compareBy, c.between, c.exclusion} {
		if resetter, ok := cached.(localCacheResetter); ok {
			resetter.reset(pool)
		}
	}
}

type cacheObservers struct {
	items [8]inspectorRuntimeObserver
	n     uint8
}

func (o *cacheObservers) push(metrics inspectorRuntimeObserver) {
	if int(o.n) == len(o.items) {
		return
	}
	o.items[o.n] = metrics
	o.n++
}
func (o *cacheObservers) pop() {
	if o.n == 0 {
		return
	}
	o.n--
	o.items[o.n] = inspectorRuntimeObserver{}
}
func (o *cacheObservers) clone() *cacheObservers {
	if o.n == 0 {
		return nil
	}
	cloned := *o
	return &cloned
}
func (o *cacheObservers) each(yield func(inspectorRuntimeObserver)) {
	if o == nil {
		return
	}
	for i := range int(o.n) {
		yield(o.items[i])
	}
}
func (o *cacheObservers) hit() {
	if o == nil {
		return
	}
	for i := range int(o.n) {
		o.items[i].cacheHit()
	}
}
func (o *cacheObservers) miss() {
	if o == nil {
		return
	}
	for i := range int(o.n) {
		o.items[i].cacheMiss()
	}
}
func (o *cacheObservers) admission() {
	if o == nil {
		return
	}
	for i := range int(o.n) {
		o.items[i].cacheAdmission()
	}
}
func (o *cacheObservers) eviction() {
	if o == nil {
		return
	}
	for i := range int(o.n) {
		o.items[i].cacheEviction()
	}
}
func (o *cacheObservers) expansion() {
	if o == nil {
		return
	}
	for i := range int(o.n) {
		o.items[i].cacheExpansion()
	}
}

type valueBitmapCache[V any] struct {
	entries   [2]valueBitmapCacheEntry[V]
	seen      *valueBitmapSeen[V]
	overflow  *valueBitmapCacheOverflow[V]
	next      uint8
	pressure  uint8
	misses    uint8
	observers *cacheObservers
}

type valueBitmapCacheOverflow[V any] struct {
	entries [2]valueBitmapCacheEntry[V]
	used    [4]uint64
	clock   uint64
}

type valueBitmapSeen[V any] struct {
	entries  [2]valueBitmapCacheKey[V]
	overflow *[2]valueBitmapCacheKey[V]
	next     uint8
}

type valueBitmapCacheKey[V any] struct {
	initialized bool
	hasValue    bool
	value       V
}

type valueBitmapCacheEntry[V any] struct {
	initialized bool
	hasValue    bool
	value       V
	bits        *roaring.Bitmap
	bytes       uint64
}

func newValueBitmapCache[V any](pool *bitmapPool, id ...nodeID) *valueBitmapCache[V] {
	if len(id) != 0 {
		return &valueBitmapCache[V]{observers: pool.observersFor(id[0])}
	}
	return &valueBitmapCache[V]{observers: pool.observers.clone()}
}

func (c *valueBitmapCache[V]) lookup(value optionalValue[V], equal func(V, V) bool) (*roaring.Bitmap, bool) {
	hasValue, capacity := value.ok, c.capacity()
	for i := 0; i < capacity; i++ {
		entry := c.entry(i)
		if entry.initialized && entry.hasValue == hasValue && (!hasValue || equal(entry.value, value.value)) {
			if c.overflow == nil {
				c.next = uint8(1 - i)
			} else {
				c.overflow.clock++
				c.overflow.used[i] = c.overflow.clock
			}
			c.observers.hit()
			return entry.bits, true
		}
	}
	c.observers.miss()
	return nil, false
}

func (c *valueBitmapCache[V]) peek(value optionalValue[V], equal func(V, V) bool) (*roaring.Bitmap, bool) {
	hasValue, capacity := value.ok, c.capacity()
	for i := 0; i < capacity; i++ {
		entry := c.entry(i)
		if entry.initialized && entry.hasValue == hasValue && (!hasValue || equal(entry.value, value.value)) {
			return entry.bits, true
		}
	}
	return nil, false
}

func (c *valueBitmapCache[V]) replace(value optionalValue[V], pool *bitmapPool) *roaring.Bitmap {
	pool.invalidateResultCache()
	if c.overflow == nil && c.entries[c.next].initialized {
		c.pressure++
		if c.pressure >= 2 {
			c.grow()
		}
	}
	index := int(c.next)
	if c.overflow != nil {
		index = c.leastRecentlyUsed()
		c.overflow.clock++
		c.overflow.used[index] = c.overflow.clock
	} else {
		c.next = (c.next + 1) % uint8(len(c.entries))
	}
	entry := c.entry(index)
	if entry.initialized {
		c.observers.eviction()
	}
	if entry.bits == nil {
		entry.bits = pool.get()
	} else {
		entry.bits.Clear()
	}
	pool.childCacheBytes -= entry.bytes
	entry.bytes = 0
	entry.bits.SetCopyOnWrite(true)
	entry.initialized = true
	entry.hasValue = value.ok
	var zero V
	entry.value = zero
	if value.ok {
		entry.value = value.value
	}
	return entry.bits
}

func (c *valueBitmapCache[V]) commit(bits *roaring.Bitmap, pool *bitmapPool) {
	for i := 0; i < c.capacity(); i++ {
		entry := c.entry(i)
		if entry.bits != bits {
			continue
		}
		bytes := bits.GetSizeInBytes()
		if bytes > uint64(maxLocalChildCacheBytes)-min(pool.childCacheBytes, uint64(maxLocalChildCacheBytes)) {
			entry.initialized = false
			var zero V
			entry.value = zero
			entry.bits = nil
			pool.put(bits)
			return
		}
		entry.bytes = bytes
		pool.childCacheBytes += bytes
		return
	}
}

func (c *valueBitmapCache[V]) reset(pool *bitmapPool) {
	for i := 0; i < c.capacity(); i++ {
		entry := c.entry(i)
		if entry.bits != nil {
			pool.childCacheBytes -= entry.bytes
			pool.put(entry.bits)
		}
	}
	observers := c.observers
	*c = valueBitmapCache[V]{observers: observers}
}

func (c *valueBitmapCache[V]) capacity() int {
	if c.overflow != nil {
		return 4
	}
	return 2
}

func (c *valueBitmapCache[V]) entry(index int) *valueBitmapCacheEntry[V] {
	if index < len(c.entries) {
		return &c.entries[index]
	}
	return &c.overflow.entries[index-len(c.entries)]
}

func (c *valueBitmapCache[V]) grow() {
	c.overflow = &valueBitmapCacheOverflow[V]{clock: 2}
	// next is the least recently used entry in the two-slot cache.
	c.overflow.used[c.next] = 1
	c.overflow.used[1-c.next] = 2
	c.next = 0
	c.observers.expansion()
}

func (c *valueBitmapCache[V]) leastRecentlyUsed() int {
	oldest := 0
	for i := 1; i < c.capacity(); i++ {
		entry := c.entry(i)
		if !entry.initialized {
			return i
		}
		if c.overflow.used[i] < c.overflow.used[oldest] {
			oldest = i
		}
	}
	return oldest
}

// admit reports whether a missed value has been seen recently enough to merit
// retaining its materialized bitmap. One-off values stay out of the cache,
// avoiding retained memory and cache pollution on high-churn query streams.
func (c *valueBitmapCache[V]) admit(value optionalValue[V], equal func(V, V) bool) bool {
	if c.seen == nil {
		c.seen = &valueBitmapSeen[V]{}
	}
	seenCapacity := 2
	if c.seen.overflow != nil {
		seenCapacity = 4
	}
	for i := 0; i < seenCapacity; i++ {
		entry := c.seenEntry(i)
		if entry.initialized && entry.hasValue == value.ok && (!value.ok || equal(entry.value, value.value)) {
			entry.initialized = false
			var zero V
			entry.value = zero
			if c.seenEmpty() {
				c.seen = nil
			}
			c.observers.admission()
			return true
		}
	}
	c.misses++
	if c.misses >= 8 && c.seen.overflow == nil {
		c.seen.overflow = &[2]valueBitmapCacheKey[V]{}
		seenCapacity = 4
	}
	entry := c.seenEntry(int(c.seen.next))
	c.seen.next = (c.seen.next + 1) % uint8(seenCapacity)
	entry.initialized = true
	entry.hasValue = value.ok
	var zero V
	entry.value = zero
	if value.ok {
		entry.value = value.value
	}
	return false
}

func (c *valueBitmapCache[V]) seenEntry(index int) *valueBitmapCacheKey[V] {
	if index < len(c.seen.entries) {
		return &c.seen.entries[index]
	}
	return &c.seen.overflow[index-len(c.seen.entries)]
}

func (c *valueBitmapCache[V]) seenEmpty() bool {
	for i := 0; i < 2; i++ {
		if c.seen.entries[i].initialized {
			return false
		}
		if c.seen.overflow != nil && c.seen.overflow[i].initialized {
			return false
		}
	}
	return true
}

func comparableValueCacheLookup[V comparable](c *valueBitmapCache[V], value optionalValue[V]) (*roaring.Bitmap, bool) {
	return c.lookup(value, func(a, b V) bool { return a == b })
}

func comparableValueCacheAdmit[V comparable](c *valueBitmapCache[V], value optionalValue[V]) bool {
	return c.admit(value, func(a, b V) bool { return a == b })
}

func comparedValueCacheLookup[V any](
	c *valueBitmapCache[V],
	value optionalValue[V],
	compare Compare[V],
) (*roaring.Bitmap, bool) {
	return c.lookup(value, func(a, b V) bool { return compare(a, b) == 0 })
}

func comparedValueCachePeek[V any](
	c *valueBitmapCache[V],
	value optionalValue[V],
	compare Compare[V],
) (*roaring.Bitmap, bool) {
	return c.peek(value, func(a, b V) bool { return compare(a, b) == 0 })
}

func comparedValueCacheAdmit[V any](
	c *valueBitmapCache[V],
	value optionalValue[V],
	compare Compare[V],
) bool {
	return c.admit(value, func(a, b V) bool { return compare(a, b) == 0 })
}

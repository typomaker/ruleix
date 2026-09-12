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
	// Keep every exact intersection alongside the learned child order. Repeated
	// Local queries can share immutable containers with a scratch result instead
	// of cloning the first posting on every search.
	results []localAllResult
	valid   bool
}

type localAllResult struct {
	inputs []*roaring.Bitmap
	ids    []uint32
	keys   []any
	epoch  uint64
	bits   *roaring.Bitmap
}

func (p *localAllPlan) resetResults(pool *bitmapPool) {
	for i := range p.results {
		result := &p.results[i]
		if result.bits != nil {
			pool.put(result.bits)
		}
		*result = localAllResult{}
	}
	p.results = nil
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
	entries    [2]valueBitmapCacheEntry[V]
	count      uint8
	overflow   []*valueBitmapCacheEntry[V]
	comparable any
	observers  *cacheObservers
}

type valueBitmapCacheEntry[V any] struct {
	initialized bool
	hasValue    bool
	value       V
	bits        *roaring.Bitmap
}

func newValueBitmapCache[V any](pool *bitmapPool, id ...nodeID) *valueBitmapCache[V] {
	if len(id) != 0 {
		return &valueBitmapCache[V]{observers: pool.observersFor(id[0])}
	}
	return &valueBitmapCache[V]{observers: pool.observers.clone()}
}

func (c *valueBitmapCache[V]) lookup(value optionalValue[V], equal func(V, V) bool) (*roaring.Bitmap, bool) {
	hasValue := value.ok
	for i := range len(c.entries) {
		entry := &c.entries[i]
		if entry.initialized && entry.hasValue == hasValue && (!hasValue || equal(entry.value, value.value)) {
			c.observers.hit()
			return entry.bits, true
		}
	}
	for _, entry := range c.overflow {
		if entry.hasValue == hasValue && (!hasValue || equal(entry.value, value.value)) {
			c.observers.hit()
			return entry.bits, true
		}
	}
	c.observers.miss()
	return nil, false
}

func (c *valueBitmapCache[V]) peek(value optionalValue[V], equal func(V, V) bool) (*roaring.Bitmap, bool) {
	hasValue := value.ok
	for i := range len(c.entries) {
		entry := &c.entries[i]
		if entry.initialized && entry.hasValue == hasValue && (!hasValue || equal(entry.value, value.value)) {
			return entry.bits, true
		}
	}
	for _, entry := range c.overflow {
		if entry.hasValue == hasValue && (!hasValue || equal(entry.value, value.value)) {
			return entry.bits, true
		}
	}
	return nil, false
}

func (c *valueBitmapCache[V]) replace(value optionalValue[V], pool *bitmapPool) *roaring.Bitmap {
	pool.invalidateResultCache()
	var entry *valueBitmapCacheEntry[V]
	if int(c.count) < len(c.entries) {
		entry = &c.entries[c.count]
		c.count++
		entry.bits = pool.get()
	} else {
		entry = &valueBitmapCacheEntry[V]{bits: pool.get()}
		c.overflow = append(c.overflow, entry)
	}
	c.observers.admission()
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

func (c *valueBitmapCache[V]) reset(pool *bitmapPool) {
	for i := range int(c.count) {
		entry := &c.entries[i]
		if entry.bits != nil {
			pool.put(entry.bits)
		}
	}
	for _, entry := range c.overflow {
		pool.put(entry.bits)
	}
	observers := c.observers
	*c = valueBitmapCache[V]{observers: observers}
}

func (c *valueBitmapCache[V]) entryCount() int { return int(c.count) + len(c.overflow) }

func (c *valueBitmapCache[V]) entry(index int) *valueBitmapCacheEntry[V] {
	if index < int(c.count) {
		return &c.entries[index]
	}
	return c.overflow[index-int(c.count)]
}

func comparableValueCacheLookup[V comparable](c *valueBitmapCache[V], value optionalValue[V]) (*roaring.Bitmap, bool) {
	index, _ := c.comparable.(map[optionalValue[V]]*valueBitmapCacheEntry[V])
	if index != nil {
		entry, found := index[value]
		if found {
			c.observers.hit()
			return entry.bits, true
		}
		c.observers.miss()
		return nil, false
	}
	return c.lookup(value, func(a, b V) bool { return a == b })
}

func comparableValueCacheReplace[V comparable](
	c *valueBitmapCache[V], value optionalValue[V], pool *bitmapPool,
) *roaring.Bitmap {
	entryBits := c.replace(value, pool)
	index, _ := c.comparable.(map[optionalValue[V]]*valueBitmapCacheEntry[V])
	if index == nil {
		index = make(map[optionalValue[V]]*valueBitmapCacheEntry[V], c.entryCount())
		for i := 0; i < c.entryCount(); i++ {
			entry := c.entry(i)
			index[optionalValue[V]{value: entry.value, ok: entry.hasValue}] = entry
		}
		c.comparable = index
	}
	index[value] = c.entry(c.entryCount() - 1)
	return entryBits
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

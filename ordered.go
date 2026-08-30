package ruleix

import (
	"fmt"
	"math"
	"unsafe"

	"github.com/RoaringBitmap/roaring/v2"
)

// GreaterOrEqual matches query >= stored. A missing stored value is a wildcard.
//
// For example, a stored minimum total of 100 matches a query total of 150:
//
//	ruleix.GreaterOrEqual(
//		func(c Constraint) (int, bool) { return c.MinimumTotal, true },
//		cmp.Compare[int],
//	)
func GreaterOrEqual[T any, V any](get Getter[T, V], compare Compare[V]) Rule[T] {
	return newOrderedRule(get, compare, greaterThan, true)
}

// LessOrEqual matches query <= stored. A missing stored value is a wildcard.
//
// For example, a stored maximum total of 200 matches a query total of 150:
//
//	ruleix.LessOrEqual(
//		func(c Constraint) (int, bool) { return c.MaximumTotal, true },
//		cmp.Compare[int],
//	)
func LessOrEqual[T any, V any](get Getter[T, V], compare Compare[V]) Rule[T] {
	return newOrderedRule(get, compare, lessThan, true)
}

// Greater matches query > stored. A missing stored value is a wildcard.
//
// For example, a stored order-count threshold of 5 matches a query count of 6:
//
//	ruleix.Greater(
//		func(c Constraint) (int, bool) { return c.OrderCountThreshold, true },
//		cmp.Compare[int],
//	)
func Greater[T any, V any](get Getter[T, V], compare Compare[V]) Rule[T] {
	return newOrderedRule(get, compare, greaterThan, false)
}

// Less matches query < stored. A missing stored value is a wildcard.
//
// For example, a stored upper limit of 10 matches a query value of 9:
//
//	ruleix.Less(
//		func(c Constraint) (int, bool) { return c.UpperLimit, true },
//		cmp.Compare[int],
//	)
func Less[T any, V any](get Getter[T, V], compare Compare[V]) Rule[T] {
	return newOrderedRule(get, compare, lessThan, false)
}

func newOrderedRule[T any, V any](
	get Getter[T, V],
	compare Compare[V],
	dir direction,
	inclusive bool,
) *orderedRule[T, V] {
	return &orderedRule[T, V]{
		get:       get,
		dir:       dir,
		inclusive: inclusive,
		compare:   compare,
	}
}

type direction uint8

const (
	greaterThan direction = iota
	lessThan
)

type orderedRule[T any, V any] struct {
	nodeID    nodeID
	get       Getter[T, V]
	compare   Compare[V]
	dir       direction
	inclusive bool
	wildcard  *roaring.Bitmap
	index     orderedIndex[V]
}

type orderedLocalQueryKey[V any] struct {
	value V
	ok    bool
}

func (r *orderedRule[T, V]) runtimeNodeID() nodeID { return r.nodeID }

func (r *orderedRule[T, V]) compileLossy(limit uint64) (Rule[T], error) {
	return r.newLossyAllPlanner().compile(limit)
}

type orderedLossyAllPlanner[T any, V any] struct {
	representations []Rule[T]
	ladder          []lossyRepresentation[T]
	exact           Rule[T]
	prepare         func() []Rule[T]
	err             error
}

//nolint:gocognit,lll // Planning keeps representation construction directly beside its accounting.
func (r *orderedRule[T, V]) newLossyAllPlanner() lossyAllPlanner[T] {
	// Ordered exact accounting is conservative and stable: key encodings,
	// logical slots, aggregate postings, and wildcard payload.
	exact := uint64(24) + bitmapBytes(r.wildcard)
	items := r.wildcard.GetCardinality()
	distinct := uint64(0)
	var values []struct {
		key  uint64
		bits *roaring.Bitmap
	}
	for _, block := range r.index.blocks {
		exact += bitmapBytes(block.bits) + 8
		for _, item := range block.items {
			items += item.bits.GetCardinality()
			distinct++
			encoded, ok := canonicalScalar(nil, any(item.value))
			if !ok {
				continue
			}
			exact += uint64(len(encoded)) + 8 + bitmapBytes(item.bits)
			key, supported := orderedScalarKey(any(item.value))
			if supported {
				values = append(values, struct {
					key  uint64
					bits *roaring.Bitmap
				}{key, item.bits})
			}
		}
	}
	exactRepresentation := Rule[T](&inspectionDetailsRule[T]{child: r, details: representationDetails(exact, items, distinct, 0, false)})
	planner := &orderedLossyAllPlanner[T, V]{exact: exactRepresentation}
	if len(values) == 0 && r.index.buildStatistics().uniqueValues != 0 {
		planner.err = fmt.Errorf("ruleix: Lossy ordered comparison requires a supported scalar value type")
		return planner
	}
	if len(values) == 0 {
		planner.prepare = func() []Rule[T] {
			compiled := &lossyOrderedRule[T, V]{nodeID: r.nodeID, get: r.get, dir: r.dir, inclusive: r.inclusive, wildcard: r.wildcard}
			return []Rule[T]{&inspectionDetailsRule[T]{child: compiled, details: representationDetails(uint64(32)+bitmapBytes(r.wildcard), items, distinct, 0, true)}}
		}
		return planner
	}
	minKey, maxKey := values[0].key, values[0].key
	for _, value := range values[1:] {
		minKey = min(minKey, value.key)
		maxKey = max(maxKey, value.key)
	}
	planner.prepare = func() []Rule[T] {
		representations := make([]Rule[T], 0, lossyMaxBucketBits+1)
		for bits := uint(0); bits <= lossyMaxBucketBits; bits++ {
			count := uint64(1) << bits
			span := maxKey - minKey
			width := span/count + 1
			// A one-bucket representation of the complete uint64 domain has
			// mathematical width 2^64, which cannot be stored in uint64.
			// MaxUint64 plus a clamped bucket lookup represents that case.
			if width == 0 {
				width = math.MaxUint64
			}
			used := min(span/width+1, count)
			candidate := &lossyOrderedRule[T, V]{nodeID: r.nodeID, get: r.get, dir: r.dir, inclusive: r.inclusive, wildcard: r.wildcard, min: minKey, max: maxKey, width: width, buckets: make([]*roaring.Bitmap, used)}
			for _, value := range values {
				n := lossyOrderedBucket(value.key, minKey, width, used)
				if candidate.buckets[n] == nil {
					candidate.buckets[n] = roaring.New()
				}
				candidate.buckets[n].Or(value.bits)
			}
			usage := uint64(32) + bitmapBytes(r.wildcard) + uint64(len(candidate.buckets))*8
			for _, posting := range candidate.buckets {
				if posting != nil {
					usage += bitmapBytes(posting)
				}
			}
			details := representationDetails(usage, items, distinct, uint64(len(candidate.buckets)), true)
			representations = append(representations, &inspectionDetailsRule[T]{child: candidate, details: details})
		}
		return representations
	}
	return planner
}

func (p *orderedLossyAllPlanner[T, V]) compile(limit uint64) (Rule[T], error) {
	ladder, err := p.representationLadder()
	if err != nil {
		return nil, err
	}
	return selectLossyRepresentation(ladder, limit, "ruleix: Lossy ordered comparison cannot fit the memory limit")
}

func (p *orderedLossyAllPlanner[T, V]) representationLadder() ([]lossyRepresentation[T], error) {
	if p.err != nil {
		return nil, p.err
	}
	if p.ladder == nil {
		p.representations = p.prepare()
		p.prepare = nil
		p.ladder = buildLossyRepresentationLadder(p.exact, p.representations)
	}
	return p.ladder, nil
}

func (*orderedRule[T, V]) inspectionStrategy() string { return "ordered" }

func (*orderedRule[T, V]) rule() {}
func (r *orderedRule[T, V]) canonicalDescriptor() canonicalRuleDescriptor {
	return canonicalRuleDescriptor{
		representation: canonicalOrdered,
		schema:         r,
		operations: [5]canonicalOperationID{{
			representation: canonicalOrdered,
			owner:          r, queryBound: r, role: canonicalWholeValue,
			direction: r.dir, inclusive: r.inclusive,
			wildcard: canonicalMissingStoredMatches, comparator: canonicalOpaqueComparator,
		}},
		operationCount: 1,
	}
}
func (r *orderedRule[T, V]) newState(ids *nodeIDAllocator, hints *buildStatistics) Rule[T] {
	id := ids.allocate()
	return r.newStateWithID(id, hints.node(id).ordered)
}
func (r *orderedRule[T, V]) newStateWithID(id nodeID, hint orderedBuildStatistics) *orderedRule[T, V] {
	return &orderedRule[T, V]{
		nodeID: id, get: r.get, compare: r.compare, dir: r.dir, inclusive: r.inclusive,
		wildcard: roaring.New(), index: newOrderedIndexWithHint(r.compare, hint),
	}
}
func (*orderedRule[T, V]) validate(T) error { return nil }
func (r *orderedRule[T, V]) insert(v T, id uint32) {
	value, ok := r.get(v)
	if !ok {
		r.wildcard.Add(id)
		return
	}
	r.index.insert(value, id)
}
func (r *orderedRule[T, V]) cardinality(v T, _ *bitmapPool) uint64 {
	return r.estimateCardinality(v)
}
func (r *orderedRule[T, V]) estimateCardinality(v T) uint64 {
	n := r.wildcard.GetCardinality()
	value, ok := r.get(v)
	if !ok {
		return n
	}
	return n + r.index.estimateCardinality(value, r.dir == lessThan, r.inclusive)
}
func (r *orderedRule[T, V]) estimateCachedCardinality(v T, pool *bitmapPool) (uint64, bool) {
	if pool.local == nil {
		return 0, false
	}
	cache, _ := pool.local[int(r.nodeID)].ordered.(*valueBitmapCache[V])
	if cache == nil {
		return 0, false
	}
	bits, found := comparedValueCachePeek(cache, getOptional(r.get, v), r.compare)
	if !found {
		return 0, false
	}
	return bits.GetCardinality(), true
}
func (r *orderedRule[T, V]) lookupCachedBitmap(v T, pool *bitmapPool) (*roaring.Bitmap, bool) {
	if pool.local == nil {
		return nil, false
	}
	cache, _ := pool.local[int(r.nodeID)].ordered.(*valueBitmapCache[V])
	if cache == nil {
		return nil, false
	}
	value := getOptional(r.get, v)
	if _, found := comparedValueCachePeek(cache, value, r.compare); !found {
		return nil, false
	}
	return comparedValueCacheLookup(cache, value, r.compare)
}
func (r *orderedRule[T, V]) localQueryKey(v T) (any, uint64) {
	value, ok := r.get(v)
	return orderedLocalQueryKey[V]{value: value, ok: ok}, uint64(16 + unsafe.Sizeof(orderedLocalQueryKey[V]{}))
}
func (r *orderedRule[T, V]) localQueryKeyMatches(v T, key any) bool {
	want, ok := key.(orderedLocalQueryKey[V])
	if !ok {
		return false
	}
	value, present := r.get(v)
	return want.ok == present && (!present || r.compare(want.value, value) == 0)
}
func (r *orderedRule[T, V]) isCardinalityZero(v T) bool {
	return r.estimateCardinality(v) == 0
}
func (r *orderedRule[T, V]) search(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	value := getOptional(r.get, v)
	if pool.local == nil {
		r.addMatches(value, dst)
		return
	}

	node := &pool.local[int(r.nodeID)]
	cache, _ := node.ordered.(*valueBitmapCache[V])
	if cache == nil {
		cache = newValueBitmapCache[V](pool, r.nodeID)
		node.ordered = cache
	}
	if bits, found := comparedValueCacheLookup(cache, value, r.compare); found {
		dst.Or(bits)
		return
	}
	if !comparedValueCacheAdmit(cache, value, r.compare) {
		r.addMatches(value, dst)
		return
	}

	bits := cache.replace(value, pool)
	r.addMatches(value, bits)
	dst.Or(bits)
	cache.commit(bits, pool)
}
func (r *orderedRule[T, V]) matchesID(v T, id uint32) bool {
	if r.wildcard.Contains(id) {
		return true
	}
	value, ok := r.get(v)
	return ok && r.index.matches(value, r.dir == lessThan, r.inclusive, id)
}

func (r *orderedRule[T, V]) addMatches(value optionalValue[V], dst *roaring.Bitmap) {
	dst.Or(r.wildcard)
	if value.ok {
		r.index.walk(value.value, r.dir == lessThan, r.inclusive, dst.Or)
	}
}
func (r *orderedRule[T, V]) appendMatchingBitmaps(value optionalValue[V], dst []*roaring.Bitmap) []*roaring.Bitmap {
	if !r.wildcard.IsEmpty() {
		dst = append(dst, r.wildcard)
	}
	if value.ok {
		r.index.walk(value.value, r.dir == lessThan, r.inclusive, func(bits *roaring.Bitmap) {
			dst = append(dst, bits)
		})
	}
	return dst
}
func (r *orderedRule[T, V]) filterCandidates(v T, dst *roaring.Bitmap, _ *bitmapPool) {
	var inline [16]*roaring.Bitmap
	postings := r.appendMatchingBitmaps(getOptional(r.get, v), inline[:0])
	if len(postings) == 0 {
		dst.Clear()
		return
	}
	dst.AndAny(postings...)
}
func (*orderedRule[T, V]) exclude(T, *roaring.Bitmap, *bitmapPool) {}
func (r *orderedRule[T, V]) optimize(total uint64) Rule[T] {
	if r.wildcard.GetCardinality() == total {
		return newMatchAllRule[T](r.wildcard)
	}
	return r
}
func (r *orderedRule[T, V]) collectBuildStatistics(stats []nodeBuildStatistics) {
	stats[r.nodeID].ordered = r.index.buildStatistics()
}
func (r *orderedRule[T, V]) prepareSearch() {
	prepareBitmapForSearch(r.wildcard)
	r.index.prepareSearch()
}
func (r *orderedRule[T, V]) internBitmaps(interner *bitmapInterner) {
	interner.intern(&r.wildcard)
	r.index.internBitmaps(interner)
}

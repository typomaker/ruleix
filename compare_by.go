package ruleix

import (
	"fmt"
	"unsafe"

	"github.com/RoaringBitmap/roaring/v2"
)

// CompareBy evaluates the operator stored in each inserted rule against the
// concrete value supplied to Search. The query-side operator is ignored. A missing
// stored value is a wildcard and its operator is not validated. Build returns
// an error when a non-wildcard rule has no operator or an unsupported operator.
//
// For example, a stored value with OperatorGTE and value 5 matches a query
// whose value is 7:
//
//	ruleix.CompareBy(
//		func(c Constraint) (int, bool) { return c.Orders, true },
//		func(c Constraint) (ruleix.Operator, bool) { return c.Operator, true },
//		cmp.Compare[int],
//	)
func CompareBy[T any, V any](
	value Getter[T, V],
	operator Getter[T, Operator],
	compare Compare[V],
) Rule[T] {
	return &compareByRule[T, V]{value: value, operator: operator, compare: compare}
}

type compareByRule[T any, V any] struct {
	nodeID               nodeID
	value                Getter[T, V]
	operator             Getter[T, Operator]
	compare              Compare[V]
	wildcard             *roaring.Bitmap
	indexes              [5]*orderedIndex[V]
	hints                [5]orderedBuildStatistics
	quantizers           [5]*orderedQuantizer[V]
	boundaries           [5]*orderedBoundaryQuantizer[V]
	levels               [5]uint32
	eqMinimum, eqMaximum V
	eqHasRange           bool
}

type compareByLocalQueryKey[V any] struct {
	value    V
	hasValue bool
}

func (r *compareByRule[T, V]) runtimeNodeID() nodeID { return r.nodeID }

func (*compareByRule[T, V]) inspectionStrategy() string { return "compare-by" }
func (r *compareByRule[T, V]) refreshedStreamingDetails(details inspectionDetails) inspectionDetails {
	memory := uint64(24) + bitmapBytes(r.wildcard)
	items := r.wildcard.GetCardinality()
	var distinct uint64
	for _, index := range r.indexes {
		indexMemory, indexItems, indexDistinct, _ := orderedIndexLossyAccounting(index, roaring.New())
		memory += indexMemory
		items += indexItems
		distinct += indexDistinct
	}
	return representationDetails(memory, items, distinct, 0, false)
}

type quantizedCompareByRule[T any, V any] struct{ *compareByRule[T, V] }

func compareByDirection(operator Operator) direction {
	if operator == OperatorGT || operator == OperatorGTE {
		return greaterThan
	}
	return lessThan
}

func compareByStorageUpward(operator Operator) bool {
	return operator == OperatorLT || operator == OperatorLTE
}

func (*compareByRule[T, V]) rule() {}
func (r *compareByRule[T, V]) canonicalDescriptor() canonicalRuleDescriptor {
	descriptor := canonicalRuleDescriptor{
		representation: canonicalCompareBy,
		schema:         r,
		operationCount: uint8(len(r.indexes)),
	}
	directions := [...]direction{greaterThan, lessThan, lessThan, greaterThan, greaterThan}
	inclusive := [...]bool{true, false, true, false, true}
	for operator := range descriptor.operations {
		descriptor.operations[operator] = canonicalOperationID{
			representation: canonicalCompareBy,
			owner:          r, queryBound: r, role: canonicalStoredOperator,
			operator:  Operator(operator),
			direction: directions[operator], inclusive: inclusive[operator],
			wildcard: canonicalMissingStoredMatches, comparator: canonicalOpaqueComparator,
		}
	}
	return descriptor
}
func (r *compareByRule[T, V]) newState(ids *nodeIDAllocator, hints *buildStatistics) Rule[T] {
	id := ids.allocate()
	return &compareByRule[T, V]{
		nodeID:   id,
		value:    r.value,
		operator: r.operator,
		compare:  r.compare,
		wildcard: roaring.New(),
		hints:    hints.node(id).compareBy,
	}
}
func (r *compareByRule[T, V]) validate(v T) error {
	if _, ok := r.value(v); !ok {
		return nil
	}
	operator, ok := r.operator(v)
	if !ok {
		return fmt.Errorf("ruleix: CompareBy operator is nil")
	}
	if operator > OperatorGTE {
		return fmt.Errorf("ruleix: unsupported operator %d", operator)
	}
	return nil
}
func (r *compareByRule[T, V]) insert(v T, id uint32) {
	value, ok := r.value(v)
	if !ok {
		r.wildcard.Add(id)
		return
	}
	operator, _ := r.operator(v)
	if operator == OperatorEQ {
		if !r.eqHasRange {
			r.eqMinimum, r.eqMaximum, r.eqHasRange = value, value, true
		} else {
			if r.compare(value, r.eqMinimum) < 0 {
				r.eqMinimum = value
			}
			if r.compare(value, r.eqMaximum) > 0 {
				r.eqMaximum = value
			}
		}
	}
	index := r.indexes[operator]
	if index == nil {
		created := newOrderedIndexWithHint(r.compare, r.hints[operator])
		index = &created
		r.indexes[operator] = index
	}
	index.insert(r.storageValue(operator, value), id)
}

func (r *compareByRule[T, V]) storageValue(operator Operator, value V) V {
	if quantizer := r.quantizers[operator]; quantizer != nil {
		return quantizer.rounded(value, r.levels[operator], compareByStorageUpward(operator))
	}
	if r.boundaries[operator] != nil {
		return roundedOrderedBoundary(r.indexes[operator], value, compareByStorageUpward(operator))
	}
	return value
}

func (r *compareByRule[T, V]) searchValue(operator Operator, value V) V {
	if operator == OperatorEQ {
		return r.storageValue(operator, value)
	}
	if quantizer := r.quantizers[operator]; quantizer != nil {
		return quantizer.rounded(value, r.levels[operator], compareByDirection(operator) == greaterThan)
	}
	if r.boundaries[operator] != nil {
		return roundedOrderedBoundary(r.indexes[operator], value, compareByDirection(operator) == greaterThan)
	}
	return value
}

func (r *compareByRule[T, V]) equalityBits(value V) *roaring.Bitmap {
	index := r.indexes[OperatorEQ]
	if index == nil {
		return nil
	}
	if r.levels[OperatorEQ] == 0 {
		return index.exact(value)
	}
	if !r.eqHasRange || r.compare(value, r.eqMinimum) < 0 || r.compare(value, r.eqMaximum) > 0 {
		return nil
	}
	return index.exact(r.searchValue(OperatorEQ, value))
}
func (r *compareByRule[T, V]) each(v T, visit func(*roaring.Bitmap)) {
	value, ok := r.value(v)
	visit(r.wildcard)
	if !ok {
		return
	}
	if r.indexes[OperatorEQ] != nil {
		if bits := r.equalityBits(value); bits != nil {
			visit(bits)
		}
	}
	// query < stored / query <= stored
	if index := r.indexes[OperatorLT]; index != nil {
		index.walk(r.searchValue(OperatorLT, value), true, false, visit)
	}
	if index := r.indexes[OperatorLTE]; index != nil {
		index.walk(r.searchValue(OperatorLTE, value), true, true, visit)
	}
	// query > stored / query >= stored
	if index := r.indexes[OperatorGT]; index != nil {
		index.walk(r.searchValue(OperatorGT, value), false, false, visit)
	}
	if index := r.indexes[OperatorGTE]; index != nil {
		index.walk(r.searchValue(OperatorGTE, value), false, true, visit)
	}
}
func (r *compareByRule[T, V]) appendMatchingBitmaps(v T, dst []*roaring.Bitmap) []*roaring.Bitmap {
	value, ok := r.value(v)
	dst = append(dst, r.wildcard)
	if !ok {
		return dst
	}
	if r.indexes[OperatorEQ] != nil {
		if bits := r.equalityBits(value); bits != nil {
			dst = append(dst, bits)
		}
	}
	if index := r.indexes[OperatorLT]; index != nil {
		index.walk(r.searchValue(OperatorLT, value), true, false, func(bits *roaring.Bitmap) { dst = append(dst, bits) })
	}
	if index := r.indexes[OperatorLTE]; index != nil {
		index.walk(r.searchValue(OperatorLTE, value), true, true, func(bits *roaring.Bitmap) { dst = append(dst, bits) })
	}
	if index := r.indexes[OperatorGT]; index != nil {
		index.walk(r.searchValue(OperatorGT, value), false, false, func(bits *roaring.Bitmap) { dst = append(dst, bits) })
	}
	if index := r.indexes[OperatorGTE]; index != nil {
		index.walk(r.searchValue(OperatorGTE, value), false, true, func(bits *roaring.Bitmap) { dst = append(dst, bits) })
	}
	return dst
}
func (r *compareByRule[T, V]) cardinality(v T, _ *bitmapPool) uint64 {
	return r.estimateCardinality(v)
}
func (r *compareByRule[T, V]) estimateCardinality(v T) uint64 {
	n := r.wildcard.GetCardinality()
	value, ok := r.value(v)
	if !ok {
		return n
	}
	if r.indexes[OperatorEQ] != nil {
		if bits := r.equalityBits(value); bits != nil {
			n += bits.GetCardinality()
		}
	}
	if index := r.indexes[OperatorLT]; index != nil {
		n += index.estimateCardinality(r.searchValue(OperatorLT, value), true, false)
	}
	if index := r.indexes[OperatorLTE]; index != nil {
		n += index.estimateCardinality(r.searchValue(OperatorLTE, value), true, true)
	}
	if index := r.indexes[OperatorGT]; index != nil {
		n += index.estimateCardinality(r.searchValue(OperatorGT, value), false, false)
	}
	if index := r.indexes[OperatorGTE]; index != nil {
		n += index.estimateCardinality(r.searchValue(OperatorGTE, value), false, true)
	}
	return n
}
func (r *compareByRule[T, V]) estimateCachedCardinality(v T, pool *bitmapPool) (uint64, bool) {
	if pool.local == nil {
		return 0, false
	}
	cache, _ := pool.local[int(r.nodeID)].compareBy.(*valueBitmapCache[V])
	if cache == nil {
		return 0, false
	}
	bits, found := comparedValueCachePeek(cache, getOptional(r.value, v), r.compare)
	if !found {
		return 0, false
	}
	return bits.GetCardinality(), true
}
func (r *compareByRule[T, V]) lookupCachedBitmap(v T, pool *bitmapPool) (*roaring.Bitmap, bool) {
	if pool.local == nil {
		return nil, false
	}
	cache, _ := pool.local[int(r.nodeID)].compareBy.(*valueBitmapCache[V])
	if cache == nil {
		return nil, false
	}
	value := getOptional(r.value, v)
	if _, found := comparedValueCachePeek(cache, value, r.compare); !found {
		return nil, false
	}
	return comparedValueCacheLookup(cache, value, r.compare)
}
func (r *compareByRule[T, V]) localQueryKey(v T) (any, uint64) {
	value, hasValue := r.value(v)
	key := compareByLocalQueryKey[V]{value: value, hasValue: hasValue}
	return key, uint64(16 + unsafe.Sizeof(key))
}
func (r *compareByRule[T, V]) localQueryKeyMatches(v T, key any) bool {
	want, ok := key.(compareByLocalQueryKey[V])
	if !ok {
		return false
	}
	value, hasValue := r.value(v)
	return want.hasValue == hasValue && (!hasValue || r.compare(want.value, value) == 0)
}
func (r *compareByRule[T, V]) isCardinalityZero(v T) bool {
	return r.estimateCardinality(v) == 0
}
func (r *compareByRule[T, V]) search(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	value := getOptional(r.value, v)
	if pool.local == nil {
		r.each(v, dst.Or)
		return
	}
	node := &pool.local[int(r.nodeID)]
	cache, _ := node.compareBy.(*valueBitmapCache[V])
	if cache == nil {
		cache = newValueBitmapCache[V](pool, r.nodeID)
		node.compareBy = cache
	}
	if bits, found := comparedValueCacheLookup(cache, value, r.compare); found {
		dst.Or(bits)
		return
	}
	if !comparedValueCacheAdmit(cache, value, r.compare) {
		r.each(v, dst.Or)
		return
	}
	bits := cache.replace(value, pool)
	r.each(v, bits.Or)
	dst.Or(bits)
	cache.commit(bits, pool)
}
func (r *compareByRule[T, V]) matchesID(v T, id uint32) bool {
	found := false
	r.each(v, func(bits *roaring.Bitmap) {
		if !found && bits.Contains(id) {
			found = true
		}
	})
	return found
}
func (r *compareByRule[T, V]) filterCandidates(v T, dst *roaring.Bitmap, _ *bitmapPool) {
	// CompareBy can contribute postings from five operator indexes. Keep the
	// common aggregate-block shape on the stack so candidate filtering does not
	// add slice-growth allocations to production-shaped Index searches.
	var inline [64]*roaring.Bitmap
	postings := r.appendMatchingBitmaps(v, inline[:0])
	if len(postings) == 0 {
		dst.Clear()
		return
	}
	dst.AndAny(postings...)
}
func (*compareByRule[T, V]) exclude(T, *roaring.Bitmap, *bitmapPool) {}
func (r *compareByRule[T, V]) optimize(total uint64) Rule[T] {
	if r.wildcard.GetCardinality() == total {
		return newMatchAllRule[T](r.wildcard)
	}
	return r
}
func (r *compareByRule[T, V]) collectBuildStatistics(stats []nodeBuildStatistics) {
	for operator, index := range r.indexes {
		if index != nil {
			stats[r.nodeID].compareBy[operator] = index.buildStatistics()
		}
	}
}
func (r *compareByRule[T, V]) prepareSearch() {
	prepareBitmapForSearch(r.wildcard)
	for _, index := range r.indexes {
		if index != nil {
			index.prepareSearch()
			index.prepareRangeSearch()
		}
	}
}
func (r *compareByRule[T, V]) internBitmaps(interner *bitmapInterner) {
	interner.intern(&r.wildcard)
	for _, index := range r.indexes {
		if index != nil {
			index.internBitmaps(interner)
		}
	}
}

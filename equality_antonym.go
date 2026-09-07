package ruleix

import (
	"github.com/RoaringBitmap/roaring/v2"
)

type strictEqualityAntonymClass[T any] struct {
	first    int
	members  []int
	operands []strictEqualityOperand[T]
}

type strictEqualityOperand[T any] interface {
	sharedWildcardEquality[T]
	equalityResultComponents[T]
	localQueryKeyProvider[T]
	runtimeNodeID() nodeID
}

// compileStrictEqualityAntonyms replaces wildcard-complement equality
// siblings with one immutable operand. Ordinary All nodes remain untouched.
func compileStrictEqualityAntonyms[T any](rule Rule[T]) Rule[T] {
	all, ok := rule.(*allRule[T])
	if !ok {
		return rule
	}
	for i, child := range all.children {
		all.children[i] = compileStrictEqualityAntonyms(child)
	}
	if !hasStrictEqualityAntonyms(all.children) {
		return all
	}
	classes := strictEqualityAntonymClasses(all.children)
	consumed := make([]bool, len(all.children))
	components := make(map[int]*strictEqualityAntonymRule[T])
	for first := 0; first < len(classes); first++ {
		if consumed[classes[first].first] {
			continue
		}
		for second := first + 1; second < len(classes); second++ {
			if consumed[classes[second].first] ||
				!strictWildcardComplements(classes[first].operands[0], classes[second].operands[0]) {
				continue
			}
			left, right := classes[first], classes[second]
			component := newStrictEqualityAntonymRule(all.children, left, right)
			emit := min(left.first, right.first)
			components[emit] = component
			for _, index := range append(append([]int(nil), left.members...), right.members...) {
				consumed[index] = true
			}
			break
		}
	}
	if len(components) == 0 {
		return all
	}
	children := make([]Rule[T], 0, len(all.children))
	for index, child := range all.children {
		if component := components[index]; component != nil {
			children = append(children, component)
		} else if !consumed[index] {
			children = append(children, child)
		}
	}
	all.children = children
	return all
}

func hasStrictEqualityAntonyms[T any](children []Rule[T]) bool {
	for first := 0; first < len(children); first++ {
		left := strictEqualityOperandOf(children[first])
		if left == nil {
			continue
		}
		for second := first + 1; second < len(children); second++ {
			right := strictEqualityOperandOf(children[second])
			if right != nil && strictWildcardComplements(left, right) {
				return true
			}
		}
	}
	return false
}

func strictEqualityAntonymClasses[T any](children []Rule[T]) []strictEqualityAntonymClass[T] {
	byWildcard := make(map[*roaring.Bitmap]int)
	var classes []strictEqualityAntonymClass[T]
	for index, child := range children {
		operand := strictEqualityOperandOf(child)
		if operand == nil {
			continue
		}
		wildcard := operand.sharedWildcard()
		classIndex, found := byWildcard[wildcard]
		if !found {
			classIndex = len(classes)
			byWildcard[wildcard] = classIndex
			classes = append(classes, strictEqualityAntonymClass[T]{first: index})
		}
		classes[classIndex].members = append(classes[classIndex].members, index)
		classes[classIndex].operands = append(classes[classIndex].operands, operand)
	}
	return classes
}

func newStrictEqualityAntonymRule[T any](
	children []Rule[T], left, right strictEqualityAntonymClass[T],
) *strictEqualityAntonymRule[T] {
	rule := &strictEqualityAntonymRule[T]{
		left: left.operands, right: right.operands,
		nodeID: left.operands[0].runtimeNodeID(),
	}
	if len(rule.left) == 1 && len(rule.right) == 1 {
		rule.leftOne, rule.rightOne = rule.left[0], rule.right[0]
	}
	for _, index := range append(append([]int(nil), left.members...), right.members...) {
		rule.rules = append(rule.rules, children[index])
	}
	for _, operand := range append(append([]strictEqualityOperand[T](nil), rule.left...), rule.right...) {
		rule.queryKeyProviders = append(rule.queryKeyProviders, operand)
	}
	return rule
}

func strictEqualityOperandOf[T any](rule Rule[T]) strictEqualityOperand[T] {
	operand, _ := unwrapExecutionRule(rule).(strictEqualityOperand[T])
	return operand
}

func strictWildcardComplements[T any](left, right sharedWildcardEquality[T]) bool {
	leftTotal := left.equalityUniverseCardinality()
	if leftTotal != right.equalityUniverseCardinality() {
		return false
	}
	leftBits, rightBits := left.sharedWildcard(), right.sharedWildcard()
	xorCardinality := leftBits.GetCardinality() + rightBits.GetCardinality() -
		2*leftBits.AndCardinality(rightBits)
	return xorCardinality == leftTotal
}

type strictEqualityAntonymRule[T any] struct {
	rules             []Rule[T]
	left, right       []strictEqualityOperand[T]
	leftOne, rightOne strictEqualityOperand[T]
	queryKeyProviders []localQueryKeyProvider[T]
	nodeID            nodeID
}

func (r *strictEqualityAntonymRule[T]) localQueryKeyProviders() []localQueryKeyProvider[T] {
	return r.queryKeyProviders
}

func (*strictEqualityAntonymRule[T]) rule()                                                 {}
func (r *strictEqualityAntonymRule[T]) newState(*nodeIDAllocator, *buildStatistics) Rule[T] { return r }
func (*strictEqualityAntonymRule[T]) validate(T) error                                      { return nil }
func (*strictEqualityAntonymRule[T]) insert(T, uint32)                                      {}
func (*strictEqualityAntonymRule[T]) exclude(T, *roaring.Bitmap, *bitmapPool)               {}
func (*strictEqualityAntonymRule[T]) collectBuildStatistics([]nodeBuildStatistics)          {}
func (r *strictEqualityAntonymRule[T]) prepareSearch() {
	for _, rule := range r.rules {
		prepareRuleSearch(rule)
	}
}

func (r *strictEqualityAntonymRule[T]) cardinality(value T, _ *bitmapPool) uint64 {
	return r.estimateCardinality(value)
}
func (r *strictEqualityAntonymRule[T]) estimateCardinality(value T) uint64 {
	if r.leftOne != nil {
		return r.leftOne.concreteMatchCardinality(value) + r.rightOne.concreteMatchCardinality(value)
	}
	return strictEqualitySideCardinality(r.left, value) + strictEqualitySideCardinality(r.right, value)
}
func (r *strictEqualityAntonymRule[T]) estimateCheapCardinality(value T) uint64 {
	return r.estimateCardinality(value)
}
func (r *strictEqualityAntonymRule[T]) isCheapCardinalityZero(value T) bool {
	return r.estimateCardinality(value) == 0
}
func (r *strictEqualityAntonymRule[T]) isCardinalityZero(value T) bool {
	return r.estimateCardinality(value) == 0
}
func (r *strictEqualityAntonymRule[T]) search(value T, dst *roaring.Bitmap, pool *bitmapPool) {
	if pool.local == nil {
		r.addMatches(value, dst, pool)
		return
	}
	cache := r.cache(pool)
	if bits, found := cache.lookup(r, value); found {
		dst.Or(bits)
		return
	}
	if !cache.admit(r, value) {
		r.addMatches(value, dst, pool)
		return
	}
	bits := cache.replace(r, value, pool)
	r.addMatches(value, bits, pool)
	dst.Or(bits)
	cache.commit(bits, pool)
}
func (r *strictEqualityAntonymRule[T]) matchesID(value T, id uint32) bool {
	if r.leftOne != nil {
		return r.leftOne.matchesConcreteID(value, id) || r.rightOne.matchesConcreteID(value, id)
	}
	return strictEqualitySideMatches(r.left, value, id) || strictEqualitySideMatches(r.right, value, id)
}
func (r *strictEqualityAntonymRule[T]) directIDWork() uint64 {
	if r.leftOne != nil {
		return 2 * allEqualityDirectIDWork
	}
	return uint64(len(r.left)+len(r.right)) * allEqualityDirectIDWork
}

func (r *strictEqualityAntonymRule[T]) addMatches(value T, dst *roaring.Bitmap, pool *bitmapPool) {
	if r.leftOne != nil {
		r.leftOne.addConcreteMatches(value, dst)
		r.rightOne.addConcreteMatches(value, dst)
		return
	}
	r.addSideMatches(r.left, value, dst, pool)
	r.addSideMatches(r.right, value, dst, pool)
}

func (r *strictEqualityAntonymRule[T]) addSideMatches(
	side []strictEqualityOperand[T], value T, dst *roaring.Bitmap, pool *bitmapPool,
) {
	if len(side) == 1 {
		side[0].addConcreteMatches(value, dst)
		return
	}
	if candidateIndex, candidateCardinality := strictEqualitySideCandidate(side, value); candidateIndex >= 0 {
		if candidateCardinality == 0 {
			return
		}
		if candidateCardinality <= allCheapDirectIDScanLimit {
			candidates := side[candidateIndex].concreteMatchSet(value)
			if strictEqualitySetAllMatches(candidates, side, candidateIndex, value) {
				candidates.addTo(dst)
				return
			}
			strictEqualityFilterSet(candidates, side, candidateIndex, value, dst)
			return
		}
	}
	bits := pool.get()
	side[0].addConcreteMatches(value, bits)
	for _, operand := range side[1:] {
		operand.intersectConcreteMatches(value, bits, pool)
		if bits.IsEmpty() {
			break
		}
	}
	dst.Or(bits)
	pool.put(bits)
}

func strictEqualitySetAllMatches[T any](
	set *equalitySet, side []strictEqualityOperand[T], candidateIndex int, value T,
) bool {
	matched := func(id uint32) bool {
		for index, operand := range side {
			if index != candidateIndex && !operand.matchesConcreteID(value, id) {
				return false
			}
		}
		return true
	}
	if set.bits != nil {
		iterator := set.bits.Iterator()
		for iterator.HasNext() {
			if !matched(iterator.Next()) {
				return false
			}
		}
		return true
	}
	if set.small != nil {
		for _, id := range set.small {
			if !matched(id) {
				return false
			}
		}
		return true
	}
	return matched(set.single)
}

func strictEqualityFilterSet[T any](
	set *equalitySet, side []strictEqualityOperand[T], candidateIndex int, value T, dst *roaring.Bitmap,
) {
	add := func(id uint32) bool {
		if strictEqualitySideMatchesExcept(side, candidateIndex, value, id) {
			dst.Add(id)
		}
		return true
	}
	if set.bits != nil {
		set.bits.Iterate(add)
		return
	}
	if set.small != nil {
		for _, id := range set.small {
			add(id)
		}
		return
	}
	add(set.single)
}

func strictEqualitySideMatchesExcept[T any](
	side []strictEqualityOperand[T], skipped int, value T, id uint32,
) bool {
	for index, operand := range side {
		if index != skipped && !operand.matchesConcreteID(value, id) {
			return false
		}
	}
	return true
}

func strictEqualitySideCandidate[T any](side []strictEqualityOperand[T], value T) (int, uint64) {
	candidateIndex, cardinality := -1, ^uint64(0)
	for index, operand := range side {
		operandCardinality := operand.concreteMatchCardinality(value)
		if operandCardinality < cardinality {
			cardinality, candidateIndex = operandCardinality, index
		}
	}
	return candidateIndex, cardinality
}

func strictEqualitySideCardinality[T any](side []strictEqualityOperand[T], value T) uint64 {
	cardinality := ^uint64(0)
	for _, operand := range side {
		cardinality = min(cardinality, operand.concreteMatchCardinality(value))
	}
	return cardinality
}

func strictEqualitySideMatches[T any](side []strictEqualityOperand[T], value T, id uint32) bool {
	for _, operand := range side {
		if !operand.matchesConcreteID(value, id) {
			return false
		}
	}
	return true
}

func (r *strictEqualityAntonymRule[T]) cache(pool *bitmapPool) *strictEqualityAntonymCache[T] {
	node := &pool.local[int(r.nodeID)]
	cache, _ := node.equality.(*strictEqualityAntonymCache[T])
	if cache == nil {
		cache = &strictEqualityAntonymCache[T]{observers: pool.observersFor(r.nodeID)}
		node.equality = cache
	}
	return cache
}

func (r *strictEqualityAntonymRule[T]) lookupCachedBitmap(
	value T, pool *bitmapPool,
) (*roaring.Bitmap, bool) {
	if pool.local == nil {
		return nil, false
	}
	cache, _ := pool.local[int(r.nodeID)].equality.(*strictEqualityAntonymCache[T])
	if cache == nil {
		return nil, false
	}
	return cache.peek(r, value)
}

func (r *strictEqualityAntonymRule[T]) lookupPlanningBitmap(value T) (*roaring.Bitmap, bool) {
	if r.leftOne != nil {
		_, left, leftDirect := r.leftOne.lookupEqualityResultComponents(value)
		_, right, rightDirect := r.rightOne.lookupEqualityResultComponents(value)
		if !leftDirect || !rightDirect || left != nil && right != nil {
			return nil, false
		}
		if left != nil {
			return left, true
		}
		if right != nil {
			return right, true
		}
		return nil, false
	}
	left, leftEmpty, leftDirect := strictEqualitySidePlanningBitmap(r.left, value)
	right, rightEmpty, rightDirect := strictEqualitySidePlanningBitmap(r.right, value)
	if !leftDirect || !rightDirect {
		return nil, false
	}
	if rightEmpty && left != nil {
		return left, true
	}
	if leftEmpty && right != nil {
		return right, true
	}
	return nil, false
}

func strictEqualitySidePlanningBitmap[T any](
	side []strictEqualityOperand[T], value T,
) (bits *roaring.Bitmap, empty, direct bool) {
	for _, operand := range side {
		_, posting, postingDirect := operand.lookupEqualityResultComponents(value)
		if !postingDirect {
			return nil, false, false
		}
		if posting == nil {
			return nil, true, true
		}
		bits = posting
	}
	if len(side) != 1 {
		bits = nil
	}
	return bits, false, true
}

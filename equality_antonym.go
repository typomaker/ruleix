package ruleix

import (
	"sort"

	"github.com/RoaringBitmap/roaring/v2"
)

type strictEqualityAntonymCandidate[T any] struct {
	first, second int
	savedBytes    uint64
	left, right   strictEqualityOperand[T]
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
	candidates := strictEqualityAntonymCandidates(all.children)
	if len(candidates) == 0 {
		return all
	}
	partners := make([]int, len(all.children))
	pairs := make(map[int]*strictEqualityAntonymRule[T])
	for _, candidate := range candidates {
		if partners[candidate.first] != 0 || partners[candidate.second] != 0 {
			continue
		}
		partners[candidate.first] = candidate.second + 1
		partners[candidate.second] = candidate.first + 1
		pair := &strictEqualityAntonymRule[T]{
			leftRule: all.children[candidate.first], rightRule: all.children[candidate.second],
			left: candidate.left, right: candidate.right, nodeID: candidate.left.runtimeNodeID(),
		}
		pair.queryKeyProvider = [2]localQueryKeyProvider[T]{pair.left, pair.right}
		pairs[candidate.first] = pair
	}
	children := make([]Rule[T], 0, len(all.children)-len(pairs))
	for index, child := range all.children {
		partner := partners[index] - 1
		if pair := pairs[index]; pair != nil {
			children = append(children, pair)
		} else if partner < 0 || partner > index {
			children = append(children, child)
		}
	}
	all.children = children
	return all
}

func strictEqualityAntonymCandidates[T any](children []Rule[T]) []strictEqualityAntonymCandidate[T] {
	var candidates []strictEqualityAntonymCandidate[T]
	for first := 0; first < len(children); first++ {
		left := strictEqualityOperandOf(children[first])
		if left == nil {
			continue
		}
		for second := first + 1; second < len(children); second++ {
			right := strictEqualityOperandOf(children[second])
			if right == nil || !strictWildcardComplements(left, right) {
				continue
			}
			candidates = append(candidates, strictEqualityAntonymCandidate[T]{
				first: first, second: second, left: left, right: right,
				savedBytes: left.sharedWildcard().GetSerializedSizeInBytes() +
					right.sharedWildcard().GetSerializedSizeInBytes(),
			})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].savedBytes > candidates[j].savedBytes
	})
	return candidates
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
	leftRule, rightRule Rule[T]
	left, right         strictEqualityOperand[T]
	queryKeyProvider    [2]localQueryKeyProvider[T]
	nodeID              nodeID
}

type strictEqualityAntonymQueryKey struct{ left, right any }

func (r *strictEqualityAntonymRule[T]) localQueryKeyProviders() []localQueryKeyProvider[T] {
	return r.queryKeyProvider[:]
}

func (*strictEqualityAntonymRule[T]) rule()                                                 {}
func (r *strictEqualityAntonymRule[T]) newState(*nodeIDAllocator, *buildStatistics) Rule[T] { return r }
func (*strictEqualityAntonymRule[T]) validate(T) error                                      { return nil }
func (*strictEqualityAntonymRule[T]) insert(T, uint32)                                      {}
func (*strictEqualityAntonymRule[T]) exclude(T, *roaring.Bitmap, *bitmapPool)               {}
func (*strictEqualityAntonymRule[T]) collectBuildStatistics([]nodeBuildStatistics)          {}
func (r *strictEqualityAntonymRule[T]) prepareSearch() {
	prepareRuleSearch(r.leftRule)
	prepareRuleSearch(r.rightRule)
}

func (r *strictEqualityAntonymRule[T]) cardinality(value T, _ *bitmapPool) uint64 {
	return r.estimateCardinality(value)
}
func (r *strictEqualityAntonymRule[T]) estimateCardinality(value T) uint64 {
	return r.left.concreteMatchCardinality(value) + r.right.concreteMatchCardinality(value)
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
		r.addMatches(value, dst)
		return
	}
	cache := r.cache(pool)
	if bits, found := cache.lookup(r, value); found {
		dst.Or(bits)
		return
	}
	if !cache.admit(r, value) {
		r.addMatches(value, dst)
		return
	}
	bits := cache.replace(r, value, pool)
	r.addMatches(value, bits)
	dst.Or(bits)
	cache.commit(bits, pool)
}
func (r *strictEqualityAntonymRule[T]) matchesID(value T, id uint32) bool {
	return r.left.matchesConcreteID(value, id) || r.right.matchesConcreteID(value, id)
}
func (*strictEqualityAntonymRule[T]) directIDWork() uint64 { return 2 * allEqualityDirectIDWork }

func (r *strictEqualityAntonymRule[T]) addMatches(value T, dst *roaring.Bitmap) {
	r.left.addConcreteMatches(value, dst)
	r.right.addConcreteMatches(value, dst)
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
	_, left, leftDirect := r.left.lookupEqualityResultComponents(value)
	_, right, rightDirect := r.right.lookupEqualityResultComponents(value)
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

func (r *strictEqualityAntonymRule[T]) localQueryKey(value T) (any, uint64) {
	_, leftBytes := r.left.localQueryKey(value)
	_, rightBytes := r.right.localQueryKey(value)
	return r.queryKey(value), leftBytes + rightBytes + 16
}
func (r *strictEqualityAntonymRule[T]) localQueryKeyMatches(value T, key any) bool {
	stored, ok := key.(strictEqualityAntonymQueryKey)
	return ok && r.left.localQueryKeyMatches(value, stored.left) &&
		r.right.localQueryKeyMatches(value, stored.right)
}

func (r *strictEqualityAntonymRule[T]) queryKey(value T) strictEqualityAntonymQueryKey {
	left, _ := r.left.localQueryKey(value)
	right, _ := r.right.localQueryKey(value)
	return strictEqualityAntonymQueryKey{left: left, right: right}
}

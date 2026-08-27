package ruleix

import "github.com/RoaringBitmap/roaring/v2"

// executionCapability describes work a compiled rule can perform directly.
// Keep this a bit set so an All descriptor stays compact and immutable.
type executionCapability uint8

const (
	executionExactPosting executionCapability = 1 << iota
	executionCheapEstimate
	executionEstimate
	executionMatchID
	executionFilterCandidates
	executionOrderedStream
	executionMaterialize
)

// executionCostClass is deliberately coarse in the shadow model. Later
// planner changes may refine these classes from benchmark evidence without
// changing the representation capability contract.
type executionCostClass uint8

const (
	executionCostUnavailable executionCostClass = iota
	executionCostConstant
	executionCostPerCandidate
	executionCostPerPosting
	executionCostOrderedWalk
)

type ruleExecutionDescriptor struct {
	capabilities executionCapability
	facts        ruleExecutionFacts
	acquire      executionCostClass
	estimate     executionCostClass
	matchID      executionCostClass
	filter       executionCostClass
	stream       executionCostClass
	materialize  executionCostClass
}

type wildcardBehavior uint8

const (
	wildcardNone wildcardBehavior = iota
	wildcardMatchesQueries
	wildcardComposed
)

// ruleExecutionFacts are compiled once after Build has finished inserting
// rules. They deliberately contain no query values or mutable observations.
type ruleExecutionFacts struct {
	postingCount        uint32
	minPostingSize      uint64
	maxPostingSize      uint64
	totalPostingSize    uint64
	wildcardCardinality uint64
	wildcard            wildcardBehavior
}

type executionFactsProvider interface {
	executionFacts() ruleExecutionFacts
}

func addExecutionPosting(facts *ruleExecutionFacts, size uint64) {
	if facts.postingCount == 0 || size < facts.minPostingSize {
		facts.minPostingSize = size
	}
	facts.postingCount++
	facts.maxPostingSize = max(facts.maxPostingSize, size)
	facts.totalPostingSize += size
}

func mergeExecutionFacts(left, right ruleExecutionFacts) ruleExecutionFacts {
	merged := ruleExecutionFacts{
		postingCount:        left.postingCount + right.postingCount,
		maxPostingSize:      max(left.maxPostingSize, right.maxPostingSize),
		totalPostingSize:    left.totalPostingSize + right.totalPostingSize,
		wildcardCardinality: left.wildcardCardinality + right.wildcardCardinality,
		wildcard:            wildcardComposed,
	}
	if left.postingCount == 0 {
		merged.minPostingSize = right.minPostingSize
	} else if right.postingCount == 0 {
		merged.minPostingSize = left.minPostingSize
	} else {
		merged.minPostingSize = min(left.minPostingSize, right.minPostingSize)
	}
	return merged
}

func describeRuleExecution[T any](rule Rule[T]) ruleExecutionDescriptor {
	rule = unwrapExecutionRule(rule)
	d := ruleExecutionDescriptor{
		capabilities: executionMaterialize,
		materialize:  executionCostPerPosting,
	}
	if provider, ok := any(rule).(executionFactsProvider); ok {
		d.facts = provider.executionFacts()
	}
	if _, ok := rule.(planningBitmapProvider[T]); ok {
		d.capabilities |= executionExactPosting
		d.acquire = executionCostConstant
	}
	if _, ok := rule.(cheapCardinalityEstimator[T]); ok {
		d.capabilities |= executionCheapEstimate | executionEstimate
		d.estimate = executionCostConstant
	} else if _, ok := rule.(cardinalityEstimator[T]); ok {
		d.capabilities |= executionEstimate
		d.estimate = executionCostOrderedWalk
	}
	if _, ok := rule.(ruleIDMatcher[T]); ok {
		d.capabilities |= executionMatchID
		d.matchID = executionCostPerCandidate
	}
	if _, ok := rule.(candidateFilter[T]); ok {
		d.capabilities |= executionFilterCandidates
		d.filter = executionCostPerPosting
	}
	if _, ok := rule.(orderedResultStreamer[T]); ok {
		d.capabilities |= executionOrderedStream
		d.stream = executionCostOrderedWalk
	}
	return d
}

func unwrapExecutionRule[T any](rule Rule[T]) Rule[T] {
	for {
		switch wrapped := rule.(type) {
		case *inspectedRuntimeRule[T]:
			rule = wrapped.child
		case *inspectionDetailsRule[T]:
			rule = wrapped.child
		case *lossyRule[T]:
			rule = wrapped.child
		default:
			return rule
		}
	}
}

type shadowExecutionOperation uint8

const (
	shadowMaterialize shadowExecutionOperation = iota
	shadowConsumePosting
	shadowValidateCandidates
	shadowFilterCandidates
	shadowStreamResult
)

type shadowExecutionDecision struct {
	child       int
	operation   shadowExecutionOperation
	cardinality uint64
}

// shouldValidateCandidates compares direct ID checks with acquiring and
// intersecting the remaining child results. The units are intentionally
// coarse: a membership check costs 16 serialized bitmap bytes, while consuming
// an existing posting costs its exact serialized size. This distinguishes a
// dense run container from an equally cardinal sparse bitmap without scanning
// either posting. Exact query facts in ranked take precedence over estimates.
// If either side cannot be costed conservatively, retain the benchmark-selected
// limit as the fallback.
func (r *allRule[T]) shouldValidateCandidates(ranked []rankedBitmap) bool {
	if len(ranked) == 0 || ranked[0].card == ^uint64(0) {
		return false
	}
	candidates := ranked[0].card
	validationCost := uint64(0)
	bitmapCost := uint64(0)
	for _, child := range ranked[1:] {
		descriptor := r.executionDescriptor(child.childIdx, r.children[child.childIdx])
		if descriptor.capabilities&executionMatchID == 0 {
			return candidates <= allCandidateScanLimit
		}
		validationCost = saturatingAdd(validationCost, saturatingMul(candidates, 16))

		if child.bits == nil {
			return candidates <= allCandidateScanLimit
		}
		bitmapCost = saturatingAdd(bitmapCost, child.bits.GetSerializedSizeInBytes())
	}
	return validationCost < bitmapCost ||
		(validationCost == bitmapCost && candidates <= allCandidateScanLimit)
}

func saturatingAdd(left, right uint64) uint64 {
	if ^uint64(0)-left < right {
		return ^uint64(0)
	}
	return left + right
}

func saturatingMul(left, right uint64) uint64 {
	if left != 0 && right > ^uint64(0)/left {
		return ^uint64(0)
	}
	return left * right
}

// shadowDecision computes a proposed first operation for tests and benchmarks.
// Production Search deliberately does not call it while the cost model is
// being validated.
func (r *allRule[T]) shadowDecision(value T, pool *bitmapPool) shadowExecutionDecision {
	best := shadowExecutionDecision{child: -1, operation: shadowMaterialize, cardinality: ^uint64(0)}
	for i, child := range r.children {
		d := r.executionDescriptor(i, child)
		cardinality := ^uint64(0)
		operation := shadowMaterialize
		if d.capabilities&executionExactPosting != 0 {
			if bits, ok := planningBitmap(child, value); ok {
				cardinality = bits.GetCardinality()
				operation = shadowConsumePosting
			}
		}
		if bits, ok := cachedBitmap(child, value, pool); ok {
			cardinality = bits.GetCardinality()
			operation = shadowConsumePosting
		} else if cardinality == ^uint64(0) && d.capabilities&executionCheapEstimate != 0 {
			if estimate, ok := cheapCardinality(child, value); ok {
				cardinality = estimate
			}
		}
		if cardinality < best.cardinality {
			best = shadowExecutionDecision{child: i, operation: operation, cardinality: cardinality}
		}
	}
	if best.child < 0 {
		return best
	}
	if best.cardinality <= allCandidateScanLimit {
		canValidate := true
		for i, child := range r.children {
			descriptor := r.executionDescriptor(i, child)
			if i != best.child && descriptor.capabilities&executionMatchID == 0 {
				canValidate = false
				break
			}
		}
		if canValidate {
			best.operation = shadowValidateCandidates
		}
	}
	return best
}

func cachedBitmap[T any](rule Rule[T], value T, pool *bitmapPool) (*roaring.Bitmap, bool) {
	provider, ok := rule.(cachedBitmapProvider[T])
	if !ok {
		return nil, false
	}
	return provider.lookupCachedBitmap(value, pool)
}

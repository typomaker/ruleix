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
	acquire      executionCostClass
	estimate     executionCostClass
	matchID      executionCostClass
	filter       executionCostClass
	stream       executionCostClass
	materialize  executionCostClass
}

func describeRuleExecution[T any](rule Rule[T]) ruleExecutionDescriptor {
	rule = unwrapExecutionRule(rule)
	d := ruleExecutionDescriptor{
		capabilities: executionMaterialize,
		materialize:  executionCostPerPosting,
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
		} else if cardinality == ^uint64(0) && d.capabilities&executionEstimate != 0 {
			if estimate, ok := estimateCardinalityForPlan(child, value, pool); ok {
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

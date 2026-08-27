package ruleix

// executionCapability is the immutable description of operations an All
// child can execute directly. Nil operation slots are deliberately explicit:
// callers must materialize complete once for the batch instead of discovering
// an interface (or accidentally materializing) for every candidate ID.
type executionCapability[T any] struct {
	posting  planningBitmapProvider[T]
	directID ruleIDMatcher[T]
	filter   candidateFilter[T]
}

func describeExecutionCapability[T any](rule Rule[T]) executionCapability[T] {
	operationRule := unwrapExecutionRule(rule)
	result := executionCapability[T]{}
	result.posting, _ = operationRule.(planningBitmapProvider[T])
	result.filter, _ = operationRule.(candidateFilter[T])
	if nested, ok := operationRule.(*allRule[T]); ok {
		if nested.planningPrepared {
			for i := range nested.children {
				if !nested.supportsDirectIDMatch(i) {
					return result
				}
			}
		} else {
			for _, child := range nested.children {
				if !supportsRuleIDMatch(child) {
					return result
				}
			}
		}
		result.directID = nested
		return result
	}
	result.directID, _ = operationRule.(ruleIDMatcher[T])
	return result
}

func (r *allRule[T]) executionCapability(index int) *executionCapability[T] {
	if len(r.execution) == len(r.children) {
		return &r.execution[index]
	}
	capability := describeExecutionCapability(r.children[index])
	return &capability
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

func supportsRuleIDMatch[T any](rule Rule[T]) bool {
	_, ok := unwrapExecutionRule(rule).(ruleIDMatcher[T])
	return ok
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
	return r.shouldValidateRemaining(ranked[0].card, ranked[1:])
}

// shouldValidateRemaining replans after an operation has produced an exact
// candidate cardinality. Keeping the remaining slice separate from the
// candidate source lets execution reconsider the bitmap/ID-check boundary
// after every narrowing step instead of following the initial ranking.
func (r *allRule[T]) shouldValidateRemaining(candidates uint64, remaining []rankedBitmap) bool {
	if len(remaining) == 0 {
		return false
	}
	validationCost := uint64(0)
	bitmapCost := uint64(0)
	for _, child := range remaining {
		if !r.supportsDirectIDMatch(child.childIdx) {
			return candidates <= allCandidateScanLimit
		}
		validationCost = saturatingAdd(validationCost, saturatingMul(candidates, allDirectIDWork))

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

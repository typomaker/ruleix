package ruleix

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

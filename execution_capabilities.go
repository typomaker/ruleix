package ruleix

// allChildExecution describes the operations one All child can execute
// directly. Nil operation slots are deliberately explicit: callers must
// materialize the complete child result once for the batch instead of
// discovering an interface (or accidentally materializing) for every ID.
type allChildExecution[T any] struct {
	posting      planningBitmapProvider[T]
	cached       cachedBitmapProvider[T]
	directID     ruleIDMatcher[T]
	directIDWork uint64
	filter       candidateFilter[T]
	queryKey     localQueryKeyProvider[T]
}

func describeAllChildExecution[T any](rule Rule[T]) allChildExecution[T] {
	operationRule := unwrapExecutionRule(rule)
	result := allChildExecution[T]{}
	// Preserve the outer rule here: cache-capable inspector wrappers delegate
	// through their observer, while the immutable operation slots below may be
	// safely resolved from the unwrapped representation.
	result.cached, _ = rule.(cachedBitmapProvider[T])
	result.posting, _ = operationRule.(planningBitmapProvider[T])
	result.filter, _ = operationRule.(candidateFilter[T])
	result.queryKey, _ = operationRule.(localQueryKeyProvider[T])
	result.directID, result.directIDWork = describeDirectIDExecution(operationRule)
	return result
}

func describeDirectIDExecution[T any](rule Rule[T]) (ruleIDMatcher[T], uint64) {
	if nested, ok := rule.(*allRule[T]); ok {
		if !nested.allChildrenSupportDirectID() {
			return nil, 0
		}
		return nested, allUnmaterializedDirectIDWork
	}
	matcher, ok := rule.(ruleIDMatcher[T])
	if !ok {
		return nil, 0
	}
	work := uint64(allUnmaterializedDirectIDWork)
	if estimator, ok := rule.(directIDWorkEstimator); ok {
		work = estimator.directIDWork()
	}
	return matcher, work
}

func (r *allRule[T]) allChildrenSupportDirectID() bool {
	if r.planningPrepared {
		for i := range r.children {
			if !r.supportsDirectIDMatch(i) {
				return false
			}
		}
		return true
	}
	for _, child := range r.children {
		if !supportsRuleIDMatch(child) {
			return false
		}
	}
	return true
}

func (r *allRule[T]) childExecution(index int) *allChildExecution[T] {
	if len(r.execution) == len(r.children) {
		return &r.execution[index]
	}
	execution := describeAllChildExecution(r.children[index])
	return &execution
}

func (r *allRule[T]) directIDOperationWork(index int, materialized bool) uint64 {
	if materialized {
		return allDirectIDWork
	}
	work := r.childExecution(index).directIDWork
	if work == 0 {
		return allUnmaterializedDirectIDWork
	}
	return work
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
// an existing posting uses its exact serialized size and an unmaterialized
// result uses its conservative cardinality estimate. This distinguishes a
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
	if candidates <= allCandidateScanLimit {
		for _, child := range remaining {
			if !r.supportsDirectIDMatch(child.childIdx) {
				return false
			}
		}
		return true
	}
	validationCost := uint64(0)
	bitmapCost := uint64(0)
	for _, child := range remaining {
		if !r.supportsDirectIDMatch(child.childIdx) {
			return candidates <= allCandidateScanLimit
		}
		directWork := r.directIDOperationWork(child.childIdx, child.bits != nil)
		if child.bits == nil && candidates > allCheapDirectIDScanLimit {
			directWork = allUnmaterializedDirectIDWork
		}
		validationCost = saturatingAdd(validationCost, saturatingMul(candidates, directWork))

		work, known := rankedBitmapNarrowWork(child)
		if !known {
			return candidates <= allCandidateScanLimit
		}
		bitmapCost = saturatingAdd(bitmapCost, work)
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

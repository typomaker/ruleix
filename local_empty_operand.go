package ruleix

// The root owns candidate-mask scratch; a negative first index records a
// rejecting root operand while source is nil. No query identity is cached.
func (r *allRule[T]) retryEmptyOperand(pool *bitmapPool, value T) bool {
	m := pool.candidateMask
	if m == nil || pool.observeRuntime || r.executionCounters != nil || m.first >= 0 || m.first == emptyRankInProgress {
		return false
	}
	i := -m.first - 1
	if r.emptyKeys&(uint8(1)<<uint(i)) != 0 {
		if r.children[i].(emptyKeyOperand[T]).isEmptyKey(value) {
			return true
		}
		m.first = 0
		return false
	}
	estimate, known := cheapCardinality(r.children[i], value)
	if known && estimate == 0 {
		return true
	}
	if !known {
		if cheapCardinalityIsZero(r.children[i], value) {
			return true
		}
		if posting, found := r.lookupPlanningBitmap(i, r.children[i], value); found && posting.IsEmpty() {
			return true
		}
	}
	m.first = 0
	return false
}

// This sentinel cannot encode a valid child index on any supported int width.
const emptyRankInProgress = -int(^uint(0)>>1) - 1

// Only the root arms this marker, and ranking estimates do not recurse into
// child search. Clear it before materializing or validating any child.
func rememberEmptyRank(pool *bitmapPool, child int) {
	if m := pool.candidateMask; m != nil && m.first == emptyRankInProgress {
		m.first = -child - 1
	}
}

// A proof checked against this query can use the existing result-return path.
// This object is read-only and retains no input or per-Local query state.
var emptyOperandResult localAllResult

func (r *allRule[T]) loadEmptyOperandResult(pool *bitmapPool, value T) *localAllResult {
	m := pool.candidateMask
	if m == nil || m.first >= 0 || m.first == emptyRankInProgress || r.executionCounters != nil {
		return nil
	}
	i := -m.first - 1
	if r.emptyKeys&(uint8(1)<<uint(i)) != 0 {
		if r.children[i].(emptyKeyOperand[T]).isEmptyKey(value) {
			return &emptyOperandResult
		}
		m.first = 0
		return nil
	}
	if r.retryEmptyOperand(pool, value) {
		return &emptyOperandResult
	}
	return nil
}

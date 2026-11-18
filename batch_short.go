package ruleix

import "unsafe"

type batchShortSource[T any] interface {
	batchPostingSource[T]
	batchShortBound() uint64
}

// A short batch uses this exact source for every offer, so the bound must hold
// for all of its postings, including values absent from the first condition.
func (r *eqRule[T, V, K]) batchShortBound() uint64 {
	if r.wildcard == nil || !r.supportsEmptyKeyCheck() {
		return batchCandidateLimit + 1
	}
	var maximum uint64
	for i := range r.values.sets {
		maximum = max(maximum, r.values.sets[i].cardinality())
		if maximum > batchCandidateLimit {
			return maximum
		}
	}
	return maximum
}

// Build already proved native masks and bounded every posting of this source.
// Short calls therefore skip speculative anchor selection and wide fallback.
func (ix *Index[C, ID]) searchShortBatch(root *allRule[C], local *localContext[C, ID],
	values []C, found [][]ID, work *batchWork,
) bool {
	anchor := int(root.shortBatchAnchor) - 1
	source := root.children[anchor].(batchPostingSource[C])
	scratch := batchScratchPool.Get().(*batchScratch)
	defer func() { clear(scratch.postings[:]); batchScratchPool.Put(scratch) }()
	clear(scratch.used[:])
	postings := scratch.postings[:len(values)]
	source.batchPostings(values, postings, work)
	if postings[0] == nil {
		allEmpty := true
		for _, posting := range postings {
			allEmpty = allEmpty && posting == nil
		}
		if allEmpty {
			return false
		}
	} else if shortBatchAllEmpty(root, values, anchor) {
		return false
	}

	if work != nil {
		work.peakScratchBytes = max(work.peakScratchBytes, uint64(unsafe.Sizeof(batchScratch{})))
	}
	matched := false
	used, rows := scratch.used[:], scratch.rows[:]
	for first := range values {
		if used[first] || postings[first] == nil {
			continue
		}
		n := 0
		for i := first; i < len(values); i++ {
			if !used[i] && postings[i] == postings[first] {
				used[i], rows[n] = true, i
				n++
			}
		}
		matched = ix.searchBatchGroup(
			root, local, values, found, rows[:n], postings[first], anchor, batchTyped, scratch, work,
		) || matched
	}
	return matched
}

// A bounded source does not reveal an absent key in another operand. Check a
// first-condition miss across the whole short batch before decoding candidates.
func shortBatchAllEmpty[T any](root *allRule[T], values []T, anchor int) bool {
	for child, rule := range root.children {
		if child == anchor || root.emptyKeys&(uint8(1)<<child) == 0 {
			continue
		}
		proof := rule.(emptyKeyOperand[T])
		if !proof.isEmptyKey(values[0]) {
			continue
		}
		allEmpty := true
		for _, value := range values[1:] {
			if !proof.isEmptyKey(value) {
				allEmpty = false
				break
			}
		}
		if allEmpty {
			return true
		}
	}
	return false
}

package ruleix

import (
	"sync"
	"unsafe"
)

type batchOperandPlan struct {
	keys    [batchWidth]any
	reps    [batchWidth]int
	classes [batchWidth]uint8
	results [batchWidth]uint64
	count   int
}

func batchClassify[T any](
	p *batchOperandPlan, provider localQueryKeyProvider[T], values []T, rows []int, active []uint64, work *batchWork,
) {
	for i, row := range rows {
		if active != nil && active[i] == 0 {
			continue
		}
		class := p.count - 1
		if provider != nil {
			for ; class >= 0; class-- {
				if work != nil {
					work.keyComparisons++
				}
				if provider.localQueryKeyMatches(values[row], p.keys[class]) {
					break
				}
			}
		} else {
			class = -1
		}
		if class < 0 {
			class = p.count
			p.count++
			p.reps[class] = row
			if provider != nil {
				p.keys[class], _ = provider.localQueryKey(values[row])
				if work != nil {
					work.keyCaptures++
				}
			}
		}
		p.classes[i] = uint8(class)
	}
}

func batchMask[T any](root *allRule[T], child int, value T, ids []uint32, active uint64, work *batchWork) uint64 {
	if work != nil {
		work.maskEvaluations++
	}
	if operand, ok := root.children[child].(candidateMaskOperand[T]); ok {
		return operand.applyCandidateMask(value, ids, active)
	}
	matcher := root.execution[child].directID
	var matches uint64
	for i, id := range ids {
		bit := uint64(1) << i
		if active&bit == 0 {
			continue
		}
		if work != nil {
			work.candidateChecks++
		}
		if matcher.matchesID(value, id) {
			matches |= bit
		}
	}
	return matches
}

var batchFullPlanPool = sync.Pool{New: func() any { return new([8]batchOperandPlan) }}

func batchOperandMasks[T any](
	root *allRule[T], values []T, rows []int, ids []uint32, masks []uint64,
	anchor int, strategy batchStrategy, work *batchWork,
) {
	if strategy == batchLazy {
		batchLazyMasks(root, values, rows, ids, masks, anchor, work)
		return
	}
	var full []batchOperandPlan
	if strategy == batchFull {
		if len(root.children) <= 8 {
			plans := batchFullPlanPool.Get().(*[8]batchOperandPlan)
			full = plans[:len(root.children)]
			defer func() {
				clear(plans[:])
				batchFullPlanPool.Put(plans)
			}()
		} else {
			full = make([]batchOperandPlan, len(root.children))
		}
		for child := range root.children {
			if child != anchor {
				batchClassify(&full[child], root.execution[child].queryKey, values, rows, nil, work)
			}
		}
	}
	if work != nil {
		work.peakScratchBytes = max(work.peakScratchBytes,
			uint64(unsafe.Sizeof(batchScratch{}))+
				uint64(unsafe.Sizeof(batchOperandPlan{}))*uint64(max(1, cap(full))))
	}
	for pass := range 2 {
		for child := range root.children {
			_, cheap := root.children[child].(candidateMaskSource[T])
			if child == anchor || cheap != (pass == 0) {
				continue
			}
			var plan batchOperandPlan
			p := &plan
			if full != nil {
				p = &full[child]
			} else {
				batchClassify(p, root.execution[child].queryKey, values, rows, masks, work)
			}
			if !batchApplyPlan(root, child, values, rows, ids, masks, p, strategy, work) {
				return
			}
		}
	}
}

type batchLazyEntry struct {
	key    any
	result uint64
	ready  bool
}

func batchLazyMasks[T any](
	root *allRule[T], values []T, rows []int, ids []uint32, masks []uint64, anchor int, work *batchWork,
) {
	var inline [8]batchLazyEntry
	plans := inline[:]
	if len(root.children) > len(inline) {
		plans = make([]batchLazyEntry, len(root.children))
	}
	if work != nil {
		work.peakScratchBytes = max(work.peakScratchBytes,
			uint64(unsafe.Sizeof(batchScratch{}))+uint64(unsafe.Sizeof(batchLazyEntry{}))*uint64(len(plans)))
	}
	for i, row := range rows {
		for pass := range 2 {
			for child, rule := range root.children {
				_, cheap := rule.(candidateMaskSource[T])
				if child == anchor || cheap != (pass == 0) || masks[i] == 0 {
					continue
				}
				masks[i] = batchLazyMask(root, child, values[row], ids, masks[i], &plans[child], work)
			}
		}
	}
}

// Candidate-first execution broadcasts each unique predicate result to its
// offer class. Results are still appended independently in internal ID order.
func batchCandidateMasks[T any](
	root *allRule[T], values []T, rows []int, ids []uint32, masks []uint64, anchor int, work *batchWork,
) {
	if work != nil {
		work.peakScratchBytes = max(work.peakScratchBytes, uint64(unsafe.Sizeof(batchOperandPlan{})))
	}
	var inline [8]int
	order := inline[:0]
	if len(root.children) > len(inline) {
		order = make([]int, 0, len(root.children))
	}
	for pass := range 2 {
		for child, rule := range root.children {
			_, cheap := rule.(candidateMaskSource[T])
			if cheap == (pass == 0) {
				order = append(order, child)
			}
		}
	}
	for _, child := range order {
		if child == anchor {
			continue
		}
		var plan batchOperandPlan
		batchClassify(&plan, root.execution[child].queryKey, values, rows, masks, work)
		for slot, id := range ids {
			bit := uint64(1) << slot
			for class := range plan.count {
				if work != nil {
					work.candidateChecks++
				}
				if root.execution[child].directID.matchesID(values[plan.reps[class]], id) {
					plan.results[class] |= bit
				}
			}
		}
		alive := false
		for i := range rows {
			masks[i] &= plan.results[plan.classes[i]]
			alive = alive || masks[i] != 0
		}
		if !alive {
			return
		}
	}
}

func batchApplyPlan[T any](
	root *allRule[T], child int, values []T, rows []int, ids []uint32, masks []uint64,
	p *batchOperandPlan, strategy batchStrategy, work *batchWork,
) bool {
	all := batchAllBits(len(ids))
	if strategy != batchCommon || p.count == 1 {
		for class := range p.count {
			p.results[class] = batchMask(root, child, values[p.reps[class]], ids, all, work)
		}
	}
	alive := false
	for i := range rows {
		if strategy == batchCommon && p.count > 1 && masks[i] != 0 {
			masks[i] = batchMask(root, child, values[rows[i]], ids, masks[i], work)
		} else {
			masks[i] &= p.results[p.classes[i]]
		}
		alive = alive || masks[i] != 0
	}
	return alive
}

func batchLazyMask[T any](
	root *allRule[T], child int, value T, ids []uint32, active uint64, p *batchLazyEntry, work *batchWork,
) uint64 {
	provider := root.execution[child].queryKey
	same := p.ready && provider != nil && provider.localQueryKeyMatches(value, p.key)
	if work != nil && p.ready && provider != nil {
		work.keyComparisons++
	}
	if !same {
		p.result = batchMask(root, child, value, ids, batchAllBits(len(ids)), work)
		if provider != nil {
			p.key, _ = provider.localQueryKey(value)
			if work != nil {
				work.keyCaptures++
			}
		}
		p.ready = true
	}
	return active & p.result
}

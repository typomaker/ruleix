package ruleix

import (
	"math/bits"
	"sort"
)

type localCandidateMask struct {
	// The published equality set is immutable. Its identity is checked
	// against every offer before reusing the candidate slots.
	source *equalitySet
	ids    [2 * allCandidateScanLimit]uint32
	first  int
	count  uint8
}

type candidateMaskOperand[T any] interface {
	applyCandidateMask(T, []uint32, uint64) uint64
}

type candidateMaskSource[T any] interface {
	candidateMaskPosting(T) *equalitySet
}

func (r *allRule[T]) rememberCandidateMask(pool *bitmapPool, v T, first rankedBitmap) {
	if pool.observeRuntime || r.executionCounters != nil || first.card > uint64(len(pool.candidateMask.ids)) {
		return
	}
	source, ok := r.children[first.childIdx].(candidateMaskSource[T])
	if !ok {
		return
	}
	for i, child := range r.children {
		if i == first.childIdx {
			continue
		}
		if _, ok := child.(candidateMaskOperand[T]); !ok {
			return
		}
	}
	posting := source.candidateMaskPosting(v)
	if posting == nil {
		return
	}
	card := posting.cardinality()
	if card <= allCandidateScanLimit || card > uint64(len(pool.candidateMask.ids)) {
		return
	}
	m := pool.candidateMask
	m.source, m.first, m.count = posting, first.childIdx, uint8(card)
	if posting.bits == nil {
		copy(m.ids[:], posting.small)
	} else {
		n := 0
		posting.bits.Iterate(func(id uint32) bool {
			m.ids[n] = id
			n++
			return true
		})
	}
}

func (r *eqRule[T, V, K]) candidateMaskPosting(v T) *equalitySet {
	if r.coarsen != nil || !r.wildcard.IsEmpty() {
		return nil
	}
	if value, found := r.get(v); found {
		return r.values.get(r.equalityKey(value))
	}
	return nil
}

func appendCandidateMaskMatches[T any, ID comparable](r *allRule[T], v T, m *localCandidateMask, values []ID, dst *[]ID) bool {
	posting := r.children[m.first].(candidateMaskSource[T]).candidateMaskPosting(v)
	if posting != m.source {
		m.source = nil
		return false
	}
	ids := m.ids[:m.count]
	mask := (uint64(1) << m.count) - 1
	for i, child := range r.children {
		if i != m.first {
			mask = child.(candidateMaskOperand[T]).applyCandidateMask(v, ids, mask)
			if mask == 0 {
				return true
			}
		}
	}
	result := *dst
	for mask != 0 {
		slot := bits.TrailingZeros64(mask)
		mask &= mask - 1
		result = append(result, values[ids[slot]])
	}
	*dst = result
	return true
}

func (r *eqRule[T, V, K]) applyCandidateMask(v T, ids []uint32, mask uint64) uint64 {
	set, wildcard := r.resolveCandidatePosting(v)
	survivors := uint64(0)
	for mask != 0 {
		slot := bits.TrailingZeros64(mask)
		bit := uint64(1) << slot
		mask &^= bit
		id := ids[slot]
		if wildcard.Contains(id) || (set != nil && set.contains(id)) {
			survivors |= bit
		}
	}
	return survivors
}

func (r *smallBetweenRule[T, V]) applyCandidateMask(v T, ids []uint32, mask uint64) uint64 {
	from, until := r.from, r.until
	// The compiled dictionary uses one-based endpoint positions. Resolve
	// the query's allowed position range once per side for the whole mask.
	var minUpper, maxLower uint16
	if query, ok := from.get(v); ok {
		items := from.index.blocks[0].items
		maxLower = uint16(sort.Search(len(items), func(i int) bool {
			return from.compare(items[i].value, query) > 0
		}))
	}
	if query, ok := until.get(v); ok {
		items := until.index.blocks[0].items
		minUpper = uint16(sort.Search(len(items), func(i int) bool {
			return until.compare(items[i].value, query) >= 0
		}) + 1)
	} else {
		minUpper = 1 << 8
	}
	for active := mask; active != 0; active &= active - 1 {
		slot := bits.TrailingZeros64(active)
		id := ids[slot]
		var pair uint16
		bounds := r.bounds
		if uint64(id) < uint64(len(bounds.keys)) {
			pair = bounds.values[bounds.keys[id]]
		}
		lower, upper := uint16(uint8(pair)), pair>>8
		if ((lower == 0 || lower > maxLower) && !from.wildcard.Contains(id)) ||
			(upper < minUpper && !until.wildcard.Contains(id)) {
			mask &^= uint64(1) << slot
		}
	}
	return mask
}

package ruleix

import (
	"math/bits"
	"sort"
)

type cachedBetweenOperand[T any] interface {
	applyCachedBetweenMask(T, []uint32, uint64, int, *localAllIntersection) uint64
}

// The first eligible broad operand borrows the existing intersection scratch.
// Each uint32 stores two candidate bound pairs. Source changes and Local.Close
// clear this storage through the ordinary candidate-mask lifecycle.
func (r *smallBetweenRule[T, V]) applyCachedBetweenMask(
	v T,
	ids []uint32,
	mask uint64,
	child int,
	state *localAllIntersection,
) uint64 {
	if !r.from.wildcard.IsEmpty() || !r.until.wildcard.IsEmpty() {
		return r.applyCandidateMask(v, ids, mask)
	}
	if !state.projecting {
		*state = localAllIntersection{projecting: true, count: uint8(child + 1)}
		for slot, id := range ids {
			var pair uint16
			if uint64(id) < uint64(len(r.bounds.keys)) {
				pair = r.bounds.values[r.bounds.keys[id]]
			}
			state.ids[slot/2] |= uint32(pair) << (16 * (slot % 2))
		}
	}
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

	var matches uint64
	if state.count == uint8(child+1) {
		for active := mask; active != 0; active &= active - 1 {
			slot := bits.TrailingZeros64(active)
			pair := uint16(state.ids[slot/2] >> (16 * (slot % 2)))
			lower, upper := uint16(uint8(pair)), pair>>8
			if lower != 0 && lower <= maxLower && upper >= minUpper {
				matches |= uint64(1) << slot
			}
		}
		return matches
	}
	// These operands have no wildcard postings, so failed bounds are final.
	for active := mask; active != 0; active &= active - 1 {
		slot := bits.TrailingZeros64(active)
		id := ids[slot]
		var pair uint16
		if uint64(id) < uint64(len(r.bounds.keys)) {
			pair = r.bounds.values[r.bounds.keys[id]]
		}
		lower, upper := uint16(uint8(pair)), pair>>8
		if lower != 0 && lower <= maxLower && upper >= minUpper {
			matches |= uint64(1) << slot
		}
	}
	return matches
}

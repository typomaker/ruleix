//go:build arm64 && !purego

package ruleix

import (
	"math/bits"
	"sort"
)

func (r *smallBetweenRule[T, V]) applyVectorCandidateMask(v T, ids []uint32, mask uint64) uint64 {
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
	var data packedBetweenLanes
	lowerWildcard, upperWildcard := !from.wildcard.IsEmpty(), !until.wildcard.IsEmpty()
	for pending := mask; pending != 0; pending &= pending - 1 {
		slot := bits.TrailingZeros64(pending)
		id := ids[slot]
		var pair uint16
		if uint64(id) < uint64(len(r.bounds.keys)) {
			pair = r.bounds.values[r.bounds.keys[id]]
		}
		data.lower[slot], data.upper[slot] = byte(pair), byte(pair>>8)
		if lowerWildcard && from.wildcard.Contains(id) {
			data.lowerWild[slot] = 255
		}
		if upperWildcard && until.wildcard.Contains(id) {
			data.upperWild[slot] = 255
		}
	}
	return neonBetweenMask(&data, uint64(maxLower), uint64(minUpper-1)) & mask
}

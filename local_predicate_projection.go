package ruleix

import "math/bits"

const projectionDisabled uint32 = 1 << 9

func (r *smallBetweenRule[T, V]) applyPackedEqualityMask(v T, ids []uint32, mask uint64, _ int, _ *localAllIntersection) uint64 {
	return r.applyCandidateMask(v, ids, mask)
}

func (r *eqRule[T, V, K]) applyPackedEqualityMask(v T, ids []uint32, mask uint64, child int, state *localAllIntersection) uint64 {
	if (state.projecting && state.ids[child]&projectionDisabled != 0) || r.coarsen != nil || len(r.values.sets) > 4095 {
		return r.applyCandidateMask(v, ids, mask)
	}
	var set *equalitySet
	var ordinal uint16
	if value, ok := r.get(v); ok {
		key := r.equalityKey(value)
		if r.values.offsets != nil {
			if offset, ok := r.values.offsets[key]; ok {
				ordinal = uint16(offset + 1)
				set = &r.values.sets[offset]
			}
		} else {
			for offset := range int(r.values.count) {
				if r.values.keys[offset] == key {
					ordinal = uint16(offset + 1)
					set = &r.values.sets[offset]
					break
				}
			}
		}
	}
	if !state.projecting {
		*state = localAllIntersection{projecting: true}
	}
	// Zero denotes an unused slot. An ordinal of zero is a missing
	// concrete posting and matches only this operand's wildcard.
	key := uint32(child+1)<<12 | uint32(ordinal)
	const ready = uint32(1 << 8)
	entry := state.ids[child]
	same := entry>>16 == key
	if !same && entry != 0 && entry&ready == 0 {
		state.ids[child] = projectionDisabled
	}
	if same && entry&ready != 0 {
		return mask & uint64(uint8(entry))
	}
	active := uint16(mask)
	if same {
		active = uint16(1)<<len(ids) - 1
	}
	var matches uint16
	for ; active != 0; active &= active - 1 {
		slot := bits.TrailingZeros16(active)
		id := ids[slot]
		if r.wildcard.Contains(id) || set != nil && set.contains(id) {
			matches |= uint16(1) << slot
		}
	}
	if same {
		state.ids[child] = key<<16 | ready | uint32(matches)
	} else if state.ids[child]&projectionDisabled == 0 {
		state.ids[child] = key << 16
	}
	return mask & uint64(matches)
}

package ruleix

import "slices"

// betweenSmallBounds stores one dictionary ordinal per ID. A dictionary
// value packs the earliest lower and latest upper endpoint positions.
type betweenSmallBounds struct {
	keys   []uint8
	values []uint16
}

func prepareSmallBetweenBounds[T any, V any](from, until *orderedRule[T, V]) *betweenSmallBounds {
	if len(from.index.blocks) != 1 || len(until.index.blocks) != 1 {
		return nil
	}
	for _, side := range []*orderedRule[T, V]{from, until} {
		block := &side.index.blocks[0]
		if side.build != nil && side.build.quantized {
			return nil
		}
		if len(block.items) < 8 || len(block.items) > 255 ||
			block.bits.IsEmpty() || block.bits.Maximum() < 255 || block.bits.Maximum() > 16_383 {
			return nil
		}
	}
	first, last := smallBetweenPositions(from), smallBetweenPositions(until)
	keys := make([]uint8, max(len(first), len(last)))
	values := []uint16{0}
	interned := map[uint16]uint8{0: 0}
	for id := range keys {
		var a, b uint8
		if id < len(first) {
			a = first[id]
		}
		if id < len(last) {
			b = last[id]
		}
		pair := uint16(a) | uint16(b)<<8
		key, ok := interned[pair]
		if !ok {
			if len(values) == 256 {
				return nil
			}
			key = uint8(len(values))
			values = append(values, pair)
			interned[pair] = key
		}
		keys[id] = key
	}
	return &betweenSmallBounds{keys, values}
}

func smallBetweenPositions[T any, V any](side *orderedRule[T, V]) []uint8 {
	block := &side.index.blocks[0]
	positions := make([]uint8, int(block.bits.Maximum())+1)
	for i, item := range block.items {
		slot := uint8(i + 1)
		item.bits.Iterate(func(id uint32) bool {
			if side.dir == lessThan || positions[id] == 0 {
				positions[id] = slot
			}
			return true
		})
	}
	return positions
}

func matchesSmallBetweenSide[T any, V any](side *orderedRule[T, V], position uint8, v T, id uint32) bool {
	if side.wildcard.Contains(id) {
		return true
	}
	value, ok := side.get(v)
	if !ok || position == 0 {
		return false
	}
	stored := side.index.blocks[0].items[position-1].value
	order := side.compare(stored, value)
	if side.dir == greaterThan {
		return order <= 0
	}
	return order >= 0
}

// smallBetweenRule keeps the ordinary posting operations and specializes only
// direct candidate validation.
type smallBetweenRule[T any, V any] struct {
	*betweenRule[T, V]
	bounds *betweenSmallBounds
}

func (r *smallBetweenRule[T, V]) matchesID(v T, id uint32) bool {
	var pair uint16
	if uint64(id) < uint64(len(r.bounds.keys)) {
		pair = r.bounds.values[r.bounds.keys[id]]
	}
	return matchesSmallBetweenSide(r.from, uint8(pair), v, id) &&
		matchesSmallBetweenSide(r.until, uint8(pair>>8), v, id)
}

func (r *betweenRule[T, V]) withSmallBounds() Rule[T] {
	bounds := prepareSmallBetweenBounds(r.from, r.until)
	if bounds == nil {
		return r
	}
	for _, side := range []*orderedRule[T, V]{r.from, r.until} {
		block := &side.index.blocks[0]
		// Trim append-grown storage before the original postings are interned.
		block.items = slices.Clone(block.items)
		block.bits = block.bits.Clone()
		for _, item := range block.items {
			item.bits = item.bits.Clone()
		}
	}
	return &smallBetweenRule[T, V]{betweenRule: r, bounds: bounds}
}

func prepareSmallBetweenChildren[T any](root *allRule[T]) {
	for i, child := range root.children {
		root.children[i] = prepareSmallBetweenChild(child)
	}
}

func prepareSmallBetweenChild[T any](rule Rule[T]) Rule[T] {
	switch child := rule.(type) {
	case *allRule[T]:
		prepareSmallBetweenChildren(child)
	case *inspectedRuntimeRule[T]:
		child.child = prepareSmallBetweenChild(child.child)
	case interface{ withSmallBounds() Rule[T] }:
		return child.withSmallBounds()
	}
	return rule
}

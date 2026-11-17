package ruleix

import (
	"math"
	"unsafe"

	"github.com/RoaringBitmap/roaring/v2"
)

type betweenCompact struct {
	from, until betweenCompactSide
}

const betweenCompactBlockSize = 2 * orderedBlockSize

const (
	betweenExclusiveBlock = 1 << iota
	betweenContiguousBlock
)

type betweenCompactSide struct {
	positions []uint32
	singles   []uint32
	flags     []uint8
}

func (r *betweenRule[T, V]) compactBoundsEligible() bool {
	return len(r.from.index.blocks) >= 8 && len(r.until.index.blocks) >= 8 &&
		len(r.from.index.blocks) < 1<<24 && len(r.until.index.blocks) < 1<<24
}

type betweenBoundsCompactor interface {
	compactBoundsEligible() bool
	compactBounds(refs, work []uint32)
}

func appendBetweenCompactors[T any](rule Rule[T], dst []betweenBoundsCompactor, underAll bool) []betweenBoundsCompactor {
	switch typed := rule.(type) {
	case *inspectedRuntimeRule[T]:
		return appendBetweenCompactors(typed.child, dst, underAll)
	case *allRule[T]:
		for _, child := range typed.children {
			dst = appendBetweenCompactors(child, dst, true)
		}
	default:
		if compact, ok := rule.(betweenBoundsCompactor); underAll && ok && compact.compactBoundsEligible() {
			dst = append(dst, compact)
		}
	}
	return dst
}

func (r *betweenRule[T, V]) compactBounds(refs, work []uint32) {
	if r.compact != nil {
		return
	}
	defer clear(work)
	// Count the singleton bitmaps whose last owners would be removed. Keep
	// singleton blocks intact because their aggregate aliases their posting.
	var slots, blocks int
	var accountedSavings uint64
	for _, side := range [...]*orderedRule[T, V]{r.from, r.until} {
		blocks += len(side.index.blocks)
		slots += len(side.index.blocks) * betweenCompactBlockSize
		for _, block := range side.index.blocks {
			if !compactSingletonBlock(block) {
				continue
			}
			for _, item := range block.items {
				work[item.bits.Minimum()]++
				accountedSavings += bitmapBytes(item.bits)
			}
		}
	}
	bytes := uint64(unsafe.Sizeof(betweenCompact{})) + (uint64(slots)+uint64(len(refs))*2)*4 + uint64(blocks)
	var release uint64
	for id, count := range work {
		if count != 0 && count == refs[id] {
			// A live singleton has at least one separately allocated Bitmap
			// object. Its header is a conservative lower bound.
			release += uint64(unsafe.Sizeof(roaring.Bitmap{}))
		}
	}
	if bytes > release || bytes > accountedSavings {
		return
	}
	r.from.prepareSearch()
	r.until.prepareSearch()
	r.compact = &betweenCompact{
		from:  compactBetweenSide(r.from, len(refs)),
		until: compactBetweenSide(r.until, len(refs)),
	}
}

func compactSingletonBlock[V any](block orderedBlock[V]) bool {
	if len(block.items) <= 1 {
		return false
	}
	for _, item := range block.items {
		if item.bits.GetCardinality() != 1 {
			return false
		}
	}
	return true
}

func compactBetweenSide[T any, V any](side *orderedRule[T, V], count int) betweenCompactSide {
	c := betweenCompactSide{
		positions: make([]uint32, count),
		singles:   make([]uint32, len(side.index.blocks)*betweenCompactBlockSize),
		flags:     make([]uint8, len(side.index.blocks)),
	}
	for bi, block := range side.index.blocks {
		compact := compactSingletonBlock(block)
		for pi, item := range block.items {
			position := uint32(bi<<8|pi) + 1
			item.bits.Iterate(func(id uint32) bool {
				if side.dir == lessThan || c.positions[id] == 0 {
					c.positions[id] = position
				}
				return true
			})
			if compact {
				c.singles[bi*betweenCompactBlockSize+pi] = item.bits.Minimum()
				item.bits = nil
			}
		}
	}
	for bi, block := range side.index.blocks {
		if block.items[0].bits != nil {
			continue
		}
		singles := c.singles[bi*betweenCompactBlockSize:]
		first := uint64(singles[0])
		flags := uint8(betweenExclusiveBlock | betweenContiguousBlock)
		for pi, id := range singles[:len(block.items)] {
			if c.positions[id] != uint32(bi<<8|pi)+1 || side.wildcard.Contains(id) {
				flags = 0
				break
			}
			if uint64(id) != first+uint64(pi) {
				flags &^= betweenContiguousBlock
			}
		}
		c.flags[bi] = flags
	}
	return c
}

func compactMatches[T any, V any](side *orderedRule[T, V], positions []uint32, v T, id uint32) bool {
	if side.wildcard.Contains(id) {
		return true
	}
	value, ok := side.get(v)
	position := positions[id]
	if !ok || position == 0 {
		return false
	}
	position--
	item := side.index.blocks[position>>8].items[position&255]
	order := side.compare(item.value, value)
	if side.dir == greaterThan {
		return order <= 0
	}
	return order >= 0
}

func (r *betweenRule[T, V]) compactSide(rule *orderedRule[T, V]) *betweenCompactSide {
	if rule == r.from {
		return &r.compact.from
	}
	return &r.compact.until
}

func (r *betweenRule[T, V]) matchesSide(side *orderedRule[T, V], v T, id uint32) bool {
	if r.compact != nil {
		return compactMatches(side, r.compactSide(side).positions, v, id)
	}
	return side.matchesID(v, id)
}

// betweenRange describes a partial boundary block and the full blocks.
type betweenRange struct {
	boundary, low, high, first, last int
}

func betweenSideRange[T any, V any](side betweenSearchSide[T, V]) betweenRange {
	index := &side.rule.index
	if !side.value.ok || len(index.blocks) == 0 {
		return betweenRange{}
	}
	bi := index.blockFor(side.value.value)
	block := &index.blocks[bi]
	if side.rule.dir == lessThan {
		cut := index.searchBlock(block, side.value.value)
		return betweenRange{boundary: bi, low: cut, high: len(block.items), first: bi + 1, last: len(index.blocks)}
	}
	lo, hi := 0, len(block.items)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if index.compare(block.items[mid].value, side.value.value) <= 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return betweenRange{boundary: bi, high: lo, last: bi}
}

func (r *betweenRule[T, V]) estimateSide(rule *orderedRule[T, V], v T) uint64 {
	if r.compact == nil {
		return rule.estimateCardinality(v)
	}
	rangePlan := betweenSideRange(betweenSearchSide[T, V]{rule: rule, value: getOptional(rule.get, v)})
	n := rule.wildcard.GetCardinality() + rule.index.blockPrefix[rangePlan.last] - rule.index.blockPrefix[rangePlan.first]
	for pos := rangePlan.low; pos < rangePlan.high; pos++ {
		bits := rule.index.blocks[rangePlan.boundary].items[pos].bits
		if bits == nil {
			n++
		} else {
			n += bits.GetCardinality()
		}
	}
	return n
}

// addSide starts with an empty destination owned by Between.
func (r *betweenRule[T, V]) addSide(side betweenSearchSide[T, V], dst *roaring.Bitmap) {
	if r.compact == nil {
		side.rule.addMatches(side.value, dst)
		return
	}
	plan := betweenSideRange(side)
	dst.Or(side.rule.wildcard)
	r.visitCompactPostings(side.rule, plan, dst.Or)
	r.trimCompactBoundary(side.rule, plan, dst)
}

func (r *betweenRule[T, V]) intersectSide(side betweenSearchSide[T, V], dst *roaring.Bitmap, postings []*roaring.Bitmap) {
	if r.compact == nil {
		postings = side.rule.appendMatchingBitmaps(side.value, postings)
		if len(postings) == 0 {
			dst.Clear()
		} else {
			dst.AndAny(postings...)
		}
		return
	}
	if !side.rule.wildcard.IsEmpty() {
		postings = append(postings, side.rule.wildcard)
	}
	plan := betweenSideRange(side)
	r.visitCompactPostings(side.rule, plan, func(bits *roaring.Bitmap) {
		postings = append(postings, bits)
	})
	if len(postings) == 0 {
		dst.Clear()
		return
	}
	dst.AndAny(postings...)
	r.trimCompactBoundary(side.rule, plan, dst)
}

func (r *betweenRule[T, V]) visitCompactPostings(rule *orderedRule[T, V], plan betweenRange, visit func(*roaring.Bitmap)) {
	if plan.high > plan.low {
		block := &rule.index.blocks[plan.boundary]
		if block.items[0].bits == nil || plan.low == 0 && plan.high == len(block.items) {
			visit(block.bits)
		} else {
			for pos := plan.low; pos < plan.high; pos++ {
				visit(block.items[pos].bits)
			}
		}
	}
	index := &rule.index
	index.walkBlockRange(plan.first, plan.last, visit)
}

// A compact boundary contributes its entire aggregate. Remove only excluded
// IDs whose best position also fails the query, so repeated IDs and wildcards
// in other postings continue to match.
func (r *betweenRule[T, V]) trimCompactBoundary(rule *orderedRule[T, V], plan betweenRange, dst *roaring.Bitmap) {
	if plan.low == plan.high {
		return
	}
	block := &rule.index.blocks[plan.boundary]
	if block.items[0].bits != nil {
		return
	}
	side := r.compactSide(rule)
	singles := side.singles[plan.boundary*betweenCompactBlockSize:]
	start, end := 0, plan.low
	ascending := rule.dir == lessThan
	if !ascending {
		start, end = plan.high, len(block.items)
	}
	if start == end {
		return
	}
	flags := side.flags[plan.boundary]
	if flags&betweenContiguousBlock != 0 {
		dst.RemoveRange(uint64(singles[start]), uint64(singles[end-1])+1)
		return
	}
	if flags&betweenExclusiveBlock != 0 {
		for _, id := range singles[start:end] {
			dst.Remove(id)
		}
		return
	}
	cut := uint32(plan.boundary<<8|end) + 1
	if !ascending {
		cut = uint32(plan.boundary<<8|start) + 1
	}
	for _, id := range singles[start:end] {
		position := side.positions[id]
		if ((ascending && position < cut) || (!ascending && position >= cut)) && !rule.wildcard.Contains(id) {
			dst.Remove(id)
		}
	}
}

func prepareBetweenCompaction[T any](root Rule[T], count int, interner *bitmapInterner) []betweenBoundsCompactor {
	if count >= 256 && uint64(count) <= math.MaxUint32 {
		if compactors := appendBetweenCompactors(root, nil, false); len(compactors) != 0 {
			interner.singletonRefs = make([]uint32, count)
			return compactors
		}
	}
	return nil
}

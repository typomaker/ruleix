package ruleix

import (
	"sort"

	"github.com/RoaringBitmap/roaring/v2"
)

func (*eqRule[T, V]) streamingLossyAccumulator() {}

func (r *eqRule[T, V]) inspectionMode() RuleMode {
	if r.quantizer.level != 0 {
		return RuleModeLossy
	}
	return RuleModeExact
}

func (r *eqRule[T, V]) fitStreamingLimit(limit uint64) {
	for r.streamingEqualityDetails(inspectionDetails{}).MemoryUsageBytes > limit && r.quantizer.level < equalityTerminalLevel {
		r.rebuildEqualityNext()
	}
}

func (r *eqRule[T, V]) fitStreamingNext() {
	if r.quantizer.level < equalityTerminalLevel {
		r.rebuildEqualityNext()
	}
}

func (r *eqRule[T, V]) nextStreamingUsage() (uint64, bool) {
	usage, _, ok := r.prepareStreamingNext()
	return usage, ok
}

func (r *eqRule[T, V]) prepareStreamingNext() (uint64, func(), bool) {
	if r.quantizer.level >= equalityTerminalLevel {
		return 0, nil, false
	}
	clone := *r
	clone.rebuildEqualityNext()
	usage := clone.streamingEqualityDetails(inspectionDetails{}).MemoryUsageBytes
	return usage, func() { r.quantizer, r.values = clone.quantizer, clone.values }, true
}

func (r *eqRule[T, V]) rebuildEqualityNext() {
	if r.quantizer.level >= equalityTerminalLevel {
		return
	}
	nextQuantizer := newEqualityQuantizer(r.quantizer.level + 1)
	old := make(postingGeneration[uint64], len(r.values.sets))
	r.values.visit(func(key uint64, set *equalitySet) {
		bits := roaring.New()
		set.addTo(bits)
		old[key] = bits
	})
	keys := make([]uint64, 0, len(old))
	for key := range old {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	next, _, ok := rebuildPostingGenerationInOrder(old, keys, len(old), 8, func(key uint64) uint64 {
		return r.quantizer.coarsen(key, nextQuantizer)
	})
	if !ok {
		return
	}
	values := newEqualityIndex[uint64](len(next))
	keys = keys[:0]
	for key := range next {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, key := range keys {
		values.addSet(key, &equalitySet{bits: next[key]})
	}
	r.quantizer, r.values = nextQuantizer, values
}

func (r *eqRule[T, V]) lookupPlanningBitmap(v T) (*roaring.Bitmap, bool) {
	if !r.wildcard.IsEmpty() {
		return nil, false
	}
	value, ok := r.get(v)
	if !ok {
		return r.wildcard, true
	}
	set := r.values.get(r.equalityKey(value))
	if set == nil {
		return r.wildcard, true
	}
	if set.bits == nil {
		return nil, false
	}
	return set.bits, true
}

func (r *eqRule[T, V]) estimateCachedCardinality(v T, pool *bitmapPool) (uint64, bool) {
	bits, found := r.lookupCachedBitmap(v, pool)
	if !found {
		return 0, false
	}
	return bits.GetCardinality(), true
}

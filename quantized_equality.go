package ruleix

import (
	"sort"
	"unsafe"

	"github.com/RoaringBitmap/roaring/v2"
)

func (*eqRule[T, V, K]) streamingLossyAccumulator() {}

func (r *eqRule[T, V, K]) inspectionMode() RuleMode {
	if r.quantizer.level != 0 {
		return RuleModeLossy
	}
	return RuleModeExact
}

func (r *eqRule[T, V, K]) fitStreamingLimit(limit uint64) {
	for r.streamingEqualityDetails(inspectionDetails{}).MemoryUsageBytes > limit && r.quantizer.level < equalityTerminalLevel {
		r.rebuildEqualityNext()
	}
}

func (r *eqRule[T, V, K]) fitStreamingNext() {
	if r.quantizer.level < equalityTerminalLevel {
		r.rebuildEqualityNext()
	}
}

func (r *eqRule[T, V, K]) nextStreamingUsage() (uint64, bool) {
	usage, _, ok := r.prepareStreamingNext()
	return usage, ok
}

func (r *eqRule[T, V, K]) prepareStreamingNext() (uint64, func(), bool) {
	if r.coarsen == nil || r.less == nil || r.quantizer.level >= equalityTerminalLevel {
		return 0, nil, false
	}
	clone := *r
	clone.rebuildEqualityNext()
	usage := clone.streamingEqualityDetails(inspectionDetails{}).MemoryUsageBytes
	return usage, func() { r.quantizer, r.values = clone.quantizer, clone.values }, true
}

func (r *eqRule[T, V, K]) rebuildEqualityNext() {
	if r.coarsen == nil || r.less == nil || r.quantizer.level >= equalityTerminalLevel {
		return
	}
	nextQuantizer := newEqualityQuantizer(r.quantizer.level + 1)
	old := make(postingGeneration[K], len(r.values.sets))
	r.values.visit(func(key K, set *equalitySet) {
		bits := roaring.New()
		set.addTo(bits)
		old[key] = bits
	})
	keys := make([]K, 0, len(old))
	for key := range old {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return r.less(keys[i], keys[j]) })
	next, _, ok := rebuildPostingGenerationInOrder(old, keys, len(old), uint64(unsafe.Sizeof(*new(K))), func(key K) K {
		return r.coarsen(key, nextQuantizer)
	})
	if !ok {
		return
	}
	values := newEqualityIndex[K](len(next))
	keys = keys[:0]
	for key := range next {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return r.less(keys[i], keys[j]) })
	for _, key := range keys {
		values.addSet(key, &equalitySet{bits: next[key]})
	}
	r.quantizer, r.values = nextQuantizer, values
}

func (r *eqRule[T, V, K]) lookupPlanningBitmap(v T) (*roaring.Bitmap, bool) {
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

func (r *eqRule[T, V, K]) estimateCachedCardinality(v T, pool *bitmapPool) (uint64, bool) {
	bits, found := r.lookupCachedBitmap(v, pool)
	if !found {
		return 0, false
	}
	return bits.GetCardinality(), true
}

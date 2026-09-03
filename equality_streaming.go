package ruleix

import "unsafe"

type streamingFirstGenerationFactory[T any] interface {
	prepareStreamingFirstGeneration() (uint64, Rule[T], bool)
}

func (*eqRule[T, V, K]) validateStreamingLossy() error {
	_, err := compileEqualityCodec[V]()
	return err
}

func equalitySetBytes(s *equalitySet) uint64 {
	if s.bits != nil {
		return bitmapBytes(s.bits)
	}
	if s.small != nil {
		return uint64(len(s.small)) * 4
	}
	return 4
}

func (r *eqRule[T, V, K]) streamingEqualityDetails(details inspectionDetails) inspectionDetails {
	usage := uint64(40) + bitmapBytes(r.wildcard)
	items := r.wildcard.GetCardinality()
	distinct := uint64(0)
	r.values.visit(func(_ K, set *equalitySet) {
		usage += uint64(unsafe.Sizeof(*new(K))) + equalitySetBytes(set)
		items += set.cardinality()
		distinct++
	})
	details.MemoryUsageBytes, details.MemoryUsageAvailable = usage, true
	details.Items, details.ItemsAvailable = items, true
	if r.quantizer.level == 0 {
		details.DistinctValues, details.DistinctValuesAvailable = distinct, true
	} else {
		details.GranularityValue, details.GranularityAvailable = distinct, true
	}
	return details
}

// prepareStreamingFirstGeneration builds an independent level-one generation.
// Exact equality stores V directly, so this is the only transition that hashes
// semantic keys; later generations coarsen their existing uint64 keys.
func (r *eqRule[T, V, K]) prepareStreamingFirstGeneration() (uint64, Rule[T], bool) {
	if r.firstGeneration == nil {
		return 0, nil, false
	}
	return r.firstGeneration(r)
}

func prepareExactEqualityFirstGeneration[T any, V comparable](
	r *eqRule[T, V, V],
) (uint64, Rule[T], bool) {
	if r.quantizer.level != 0 || r.codecErr != nil || r.codec.hash == nil {
		return 0, nil, false
	}
	quantizer := newEqualityQuantizer(1)
	codec := r.codec
	candidate := &eqRule[T, V, uint64]{
		nodeID: r.nodeID, get: r.get, wildcard: r.wildcard, codec: codec,
		quantizer: quantizer,
		encode:    hashedEqualityEncoder(codec),
		coarsen:   coarsenEqualityHash,
		less:      lessEqualityHash,
		values:    newEqualityIndex[uint64](len(r.values.sets)),
	}
	r.values.visit(func(key V, set *equalitySet) {
		candidate.values.addSet(quantizer.key(codec.hash(key)), set)
	})
	usage := candidate.streamingEqualityDetails(inspectionDetails{}).MemoryUsageBytes
	return usage, candidate, true
}

func hashedEqualityEncoder[V comparable](codec equalityCodec[V]) func(V, equalityQuantizer) uint64 {
	return func(value V, quantizer equalityQuantizer) uint64 {
		return quantizer.key(codec.hash(value))
	}
}

func coarsenEqualityHash(key uint64, quantizer equalityQuantizer) uint64 {
	return quantizer.key(key)
}

func lessEqualityHash(left, right uint64) bool { return left < right }

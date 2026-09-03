package ruleix

import "unsafe"

type streamingFirstGenerationFactory[T any] interface {
	prepareStreamingFirstGeneration() (uint64, Rule[T], bool)
}

func (*eqRule[T, V]) validateStreamingLossy() error {
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

func (r *eqRule[T, V]) streamingEqualityDetails(details inspectionDetails) inspectionDetails {
	usage := uint64(40) + bitmapBytes(r.wildcard)
	items := r.wildcard.GetCardinality()
	distinct := uint64(0)
	r.values.visit(func(_ uint64, set *equalitySet) {
		usage += uint64(unsafe.Sizeof(uint64(0))) + equalitySetBytes(set)
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

// prepareStreamingFirstGeneration builds an independent level-one generation
// by rounding the full integer physical keys already stored at level zero.
func (r *eqRule[T, V]) prepareStreamingFirstGeneration() (uint64, Rule[T], bool) {
	if r.quantizer.level != 0 || r.codecErr != nil || r.codec.hash == nil {
		return 0, nil, false
	}
	quantizer := newEqualityQuantizer(1)
	candidate := &eqRule[T, V]{
		nodeID: r.nodeID, get: r.get, wildcard: r.wildcard, codec: r.codec,
		quantizer: quantizer, values: newEqualityIndex[uint64](len(r.values.sets)),
	}
	r.values.visit(func(key uint64, set *equalitySet) { candidate.values.addSet(quantizer.key(key), set) })
	usage := candidate.streamingEqualityDetails(inspectionDetails{}).MemoryUsageBytes
	return usage, candidate, true
}

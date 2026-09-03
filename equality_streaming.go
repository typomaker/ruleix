package ruleix

import (
	"cmp"
	"slices"
	"unsafe"
)

type streamingFirstGenerationFactory[T any] interface {
	prepareStreamingFirstGeneration() (uint64, Rule[T], bool)
}

func (*eqRule[T, V]) validateStreamingLossy() error {
	_, err := compileEqualityCodec[V]()
	return err
}

func equalityBucketCounts(maxBits uint) []uint64 {
	counts := make([]uint64, 0, int(maxBits)*4+1)
	for bit := maxBits; bit > 0; bit-- {
		upper := uint64(1) << bit
		for numerator := uint64(8); numerator >= 5; numerator-- {
			count := upper / 8 * numerator
			if len(counts) == 0 || counts[len(counts)-1] != count {
				counts = append(counts, count)
			}
		}
	}
	return append(counts, 1)
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
	r.values.visit(func(key equalityPhysicalKey[V], set *equalitySet) {
		keyBytes := uint64(unsafe.Sizeof(key))
		if !key.bucketed {
			keyBytes += comparableValueBytes(any(key.exact))
		}
		usage += keyBytes + equalitySetBytes(set)
		items += set.cardinality()
		distinct++
	})
	details.MemoryUsageBytes, details.MemoryUsageAvailable = usage, true
	details.Items, details.ItemsAvailable = items, true
	if r.quantizer.bucketCount == 0 {
		details.DistinctValues, details.DistinctValuesAvailable = distinct, true
	} else {
		details.GranularityValue, details.GranularityAvailable = distinct, true
	}
	return details
}

// prepareStreamingFirstGeneration builds an independent level-one generation
// of the same rule and index types. Only bucket keys are copied, so publishing
// it releases every exact payload held by the old generation.
func (r *eqRule[T, V]) prepareStreamingFirstGeneration() (uint64, Rule[T], bool) {
	if r.quantizer.bucketCount != 0 {
		return 0, nil, false
	}
	codec, err := compileEqualityCodec[V]()
	if err != nil {
		return 0, nil, false
	}
	quantizer := newEqualityQuantizer(equalityBucketCounts(lossyMaxBucketBits)[0])
	type exactPosting struct {
		hash   uint64
		offset uint32
		set    *equalitySet
	}
	postings := make([]exactPosting, 0, len(r.values.sets))
	r.values.visit(func(key equalityPhysicalKey[V], set *equalitySet) {
		postings = append(postings, exactPosting{codec.hash(key.exact), uint32(len(postings)), set})
	})
	slices.SortFunc(postings, func(a, b exactPosting) int {
		if a.hash != b.hash {
			return cmp.Compare(a.hash, b.hash)
		}
		return cmp.Compare(a.offset, b.offset)
	})
	candidate := &eqRule[T, V]{
		nodeID: r.nodeID, get: r.get, wildcard: r.wildcard,
		quantizer: quantizer, codec: codec,
		values: newEqualityIndex[equalityPhysicalKey[V]](min(len(postings), int(quantizer.bucketCount))),
	}
	for _, posting := range postings {
		candidate.values.addSet(bucketEqualityKey[V](quantizer.key(posting.hash)), posting.set)
	}
	usage := candidate.streamingEqualityDetails(inspectionDetails{}).MemoryUsageBytes
	return usage, candidate, true
}

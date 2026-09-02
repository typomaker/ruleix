package ruleix

import "sort"

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

func (r *eqRule[T, V]) streamingExactDetails(details inspectionDetails) inspectionDetails {
	usage := uint64(24) + bitmapBytes(r.wildcard)
	items := r.wildcard.GetCardinality()
	distinct := uint64(0)
	r.values.visit(func(value V, set *equalitySet) {
		usage += comparableValueBytes(any(value)) + 16 + equalitySetBytes(set)
		items += set.cardinality()
		distinct++
	})
	details.MemoryUsageBytes, details.MemoryUsageAvailable = usage, true
	details.Items, details.ItemsAvailable = items, true
	details.DistinctValues, details.DistinctValuesAvailable = distinct, true
	return details
}

// prepareStreamingFirstGeneration lazily performs the identity-to-level-one
// transition. It builds one independent generation and retains no future
// representations; publication by streamingAdaptiveLeaf releases exact keys.
func (r *eqRule[T, V]) prepareStreamingFirstGeneration() (uint64, Rule[T], bool) {
	codec, err := compileEqualityCodec[V]()
	if err != nil {
		return 0, nil, false
	}
	counts := equalityBucketCounts(lossyMaxBucketBits)
	quantizer := newEqualityQuantizer(counts[0])
	type exactPosting struct {
		hash   uint64
		offset uint32
		set    *equalitySet
	}
	postings := make([]exactPosting, 0, len(r.values.sets))
	r.values.visit(func(value V, set *equalitySet) {
		postings = append(postings, exactPosting{codec.hash(value), uint32(len(postings)), set})
	})
	sort.Slice(postings, func(i, j int) bool {
		if postings[i].hash != postings[j].hash {
			return postings[i].hash < postings[j].hash
		}
		return postings[i].offset < postings[j].offset
	})
	candidate := &quantizedEqualityRule[T, V]{
		nodeID: r.nodeID, get: r.get, wildcard: r.wildcard,
		quantizer: quantizer, codec: codec,
		values: newEqualityIndex[uint64](min(len(postings), int(quantizer.bucketCount))),
	}
	for _, posting := range postings {
		candidate.values.addSet(quantizer.key(posting.hash), posting.set)
	}
	usage := candidate.refreshedStreamingDetails(inspectionDetails{}).MemoryUsageBytes
	return usage, candidate, true
}

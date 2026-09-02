package ruleix

import "sort"

type streamingFirstGenerationFactory[T any] interface {
	prepareStreamingFirstGeneration() (uint64, Rule[T], bool)
}

func (r *eqRule[T, V]) streamingExactDetails(details inspectionDetails) inspectionDetails {
	usage := uint64(24) + bitmapBytes(r.wildcard)
	items := r.wildcard.GetCardinality()
	r.values.visit(func(value V, set *equalitySet) {
		usage += comparableValueBytes(any(value)) + 16 + equalitySetBytes(set)
		items += set.cardinality()
	})
	details.MemoryUsageBytes, details.MemoryUsageAvailable = usage, true
	details.Items, details.ItemsAvailable = items, true
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

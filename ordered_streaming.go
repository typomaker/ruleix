package ruleix

type orderedTransformerKind uint8

const (
	orderedIdentityTransformer orderedTransformerKind = iota
	orderedFixedTransformer
	orderedBoundaryTransformer
)

// orderedKeyTransformer is mandatory state on every ordered rule. Level zero
// is identity for every kind; later levels are selected only by Lossy's build
// controller and never change the physical rule or index type.
type orderedKeyTransformer[V any] struct {
	kind  orderedTransformerKind
	fixed orderedQuantizer[V]
}

func (t orderedKeyTransformer[V]) key(
	index *orderedIndex[V], value V, level uint32, upward bool,
) V {
	if level == 0 {
		return value
	}
	switch t.kind {
	case orderedFixedTransformer:
		return t.fixed.rounded(value, level, upward)
	case orderedBoundaryTransformer:
		return roundedOrderedBoundary(index, value, upward)
	default:
		return value
	}
}

func (r *orderedRule[T, V]) streamingLossyAccumulator() {}

func (r *orderedRule[T, V]) storageValue(value V) V {
	return r.transform.key(&r.index, value, r.level, r.dir == lessThan)
}

func (r *orderedRule[T, V]) searchValue(value V) V {
	return r.transform.key(&r.index, value, r.level, r.dir == greaterThan)
}

func (r *orderedRule[T, V]) fitStreamingLimit(limit uint64) {
	for r.refreshedStreamingDetails(inspectionDetails{}).MemoryUsageBytes > limit {
		_, apply, ok := r.prepareStreamingNext()
		if !ok {
			break
		}
		apply()
	}
}

func (r *orderedRule[T, V]) fitStreamingNext() {
	if _, apply, ok := r.prepareStreamingNext(); ok {
		apply()
	}
}

func (r *orderedRule[T, V]) nextStreamingUsage() (uint64, bool) {
	usage, _, ok := r.prepareStreamingNext()
	return usage, ok
}

func (r *orderedRule[T, V]) prepareStreamingNext() (uint64, func(), bool) {
	if r.level == 0 ||
		r.transform.kind == orderedBoundaryTransformer && r.index.buildStatistics().uniqueValues <= 1 ||
		r.transform.kind == orderedFixedTransformer && r.level >= r.transform.fixed.terminalLevel() {
		return 0, nil, false
	}
	next := newOrderedIndex(r.compare)
	if r.transform.kind == orderedBoundaryTransformer {
		next = rebuildOrderedBoundaries(&r.index, r.dir)
	} else {
		for _, block := range r.index.blocks {
			for _, item := range block.items {
				boundary := r.transform.key(&r.index, item.value, r.level+1, r.dir == lessThan)
				next.insertPosting(boundary, item.bits)
			}
		}
	}
	details := (&orderedRule[T, V]{index: next, wildcard: r.wildcard}).
		refreshedStreamingDetails(inspectionDetails{})
	return details.MemoryUsageBytes, func() {
		r.index = next
		r.level++
	}, true
}

// prepareStreamingFirstGeneration validates that the user comparator agrees
// with the numeric/time total order before opting into its fixed quantizer.
func (r *orderedRule[T, V]) prepareStreamingFirstGeneration() (uint64, Rule[T], bool) {
	if r.level != 0 {
		return 0, nil, false
	}
	quantizer, ok := compileOrderedQuantizer[V]()
	if !ok {
		return r.prepareBoundaryFirstGeneration()
	}
	var previous V
	havePrevious := false
	for _, block := range r.index.blocks {
		for _, item := range block.items {
			comparatorReversed := havePrevious && r.compare(previous, item.value) >= 0
			encodingReversed := havePrevious && quantizer.encode(previous) > quantizer.encode(item.value)
			if comparatorReversed || encodingReversed {
				return r.prepareBoundaryFirstGeneration()
			}
			previous, havePrevious = item.value, true
		}
	}
	candidate := &orderedRule[T, V]{
		nodeID: r.nodeID, get: r.get, compare: r.compare, dir: r.dir, inclusive: r.inclusive,
		wildcard: r.wildcard, index: newOrderedIndex(r.compare),
		transform: orderedKeyTransformer[V]{kind: orderedFixedTransformer, fixed: quantizer}, level: 1,
	}
	for _, block := range r.index.blocks {
		for _, item := range block.items {
			candidate.index.insertPosting(candidate.storageValue(item.value), item.bits)
		}
	}
	details := candidate.refreshedStreamingDetails(inspectionDetails{})
	return details.MemoryUsageBytes, candidate, true
}

func (r *orderedRule[T, V]) prepareBoundaryFirstGeneration() (uint64, Rule[T], bool) {
	if r.index.buildStatistics().uniqueValues <= 1 {
		return 0, nil, false
	}
	candidate := &orderedRule[T, V]{
		nodeID: r.nodeID, get: r.get, compare: r.compare, dir: r.dir, inclusive: r.inclusive,
		wildcard: r.wildcard, index: newOrderedIndex(r.compare),
		transform: orderedKeyTransformer[V]{kind: orderedBoundaryTransformer}, level: 1,
	}
	candidate.index = rebuildOrderedBoundaries(&r.index, r.dir)
	details := candidate.refreshedStreamingDetails(inspectionDetails{})
	return details.MemoryUsageBytes, candidate, true
}

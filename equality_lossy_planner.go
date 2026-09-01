package ruleix

import (
	"github.com/RoaringBitmap/roaring/v2"
)

func (r *eqRule[T, V]) compileLossy(limit uint64) (Rule[T], error) {
	return r.newLossyAllPlanner().compile(limit)
}

type equalityLossyAllPlanner[T any, V comparable] struct {
	representations []Rule[T]
	ladder          []lossyRepresentation[T]
	exact           Rule[T]
	prepare         func() []Rule[T]
	err             error
}

//nolint:gocognit // Planning evaluates representations in one allocation-aware pass.
func (r *eqRule[T, V]) newLossyAllPlanner() lossyAllPlanner[T] {
	// V1 exact accounting: wildcard payload, 24 bytes of strategy/source
	// metadata, and for each occupied bucket an 8-byte key, 16-byte logical
	// slot (including its source ID and alignment), and payload.
	exact := uint64(24) + bitmapBytes(r.wildcard)
	items := r.wildcard.GetCardinality()
	distinct := uint64(0)
	codec, codecErr := compileEqualityCodec[V]()
	type hashedSet struct {
		hash uint64
		set  *equalitySet
	}
	hashed := make([]hashedSet, 0, len(r.values.sets))
	addHash := func(value V, set *equalitySet) {
		if codecErr == nil {
			hashed = append(hashed, hashedSet{hash: codec.hash(value), set: set})
		}
	}
	if r.values.offsets == nil {
		for n := range int(r.values.count) {
			items += equalitySetCardinality(&r.values.sets[n])
			distinct++
			addHash(r.values.keys[n], &r.values.sets[n])
			exact += comparableValueBytes(any(r.values.keys[n])) + 16 + equalitySetBytes(&r.values.sets[n])
		}
	} else {
		for value, offset := range r.values.offsets {
			items += equalitySetCardinality(&r.values.sets[offset])
			distinct++
			addHash(value, &r.values.sets[offset])
			exact += comparableValueBytes(any(value)) + 16 + equalitySetBytes(&r.values.sets[offset])
		}
	}
	exactRepresentation := Rule[T](&inspectionDetailsRule[T]{
		child:   r,
		details: representationDetails(exact, items, distinct, 0, false),
	})
	planner := &equalityLossyAllPlanner[T, V]{exact: exactRepresentation, err: codecErr}
	if codecErr != nil {
		return planner
	}
	planner.prepare = func() []Rule[T] {
		bucketCounts := equalityBucketCounts(lossyMaxBucketBits)
		representations := make([]Rule[T], 0, len(bucketCounts))
		var sameValuePairs float64
		for _, value := range hashed {
			count := equalitySetCardinality(value.set)
			sameValuePairs += float64(count) * float64(count)
		}
		concreteItems := items - r.wildcard.GetCardinality()
		allDifferentValuePairs := float64(concreteItems)*float64(concreteItems) - sameValuePairs
		// buildLossyRepresentationLadder accepts the builders' natural
		// coarse-to-fine order and reverses it after the exact level.
		for index := len(bucketCounts) - 1; index >= 0; index-- {
			bucketCount := bucketCounts[index]
			quantizer := newEqualityQuantizer(bucketCount)
			candidate := &quantizedEqualityRule[T, V]{
				nodeID: r.nodeID, get: r.get, wildcard: r.wildcard,
				quantizer: quantizer, codec: codec, values: newEqualityIndex[uint64](int(bucketCount)),
			}
			for _, value := range hashed {
				bucket := quantizer.key(value.hash)
				posting := roaring.New()
				value.set.addTo(posting)
				candidate.values.addSet(bucket, &equalitySet{bits: posting})
			}
			usage := uint64(40) + bitmapBytes(candidate.wildcard)
			var collidingDifferentValuePairs float64
			for index := range candidate.values.sets {
				posting := &candidate.values.sets[index]
				usage += 24 + equalitySetBytes(posting)
				count := float64(posting.cardinality())
				collidingDifferentValuePairs += count * count
			}
			details := representationDetails(usage, items, distinct, uint64(len(candidate.values.sets)), true)
			if allDifferentValuePairs > 0 {
				collidingDifferentValuePairs -= sameValuePairs
				details.EstimatedFalsePositiveRateValue = collidingDifferentValuePairs / allDifferentValuePairs
				details.EstimatedFalsePositiveRateAvailable = true
			}
			representations = append(representations, &inspectionDetailsRule[T]{child: candidate, details: details})
		}
		return representations
	}
	return planner
}

// equalityBucketCounts returns four nested precision levels per power-of-two
// interval. Each intermediate level merges one more quarter of the upper
// level's base classes pairwise, so every class has exactly one parent.
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

func (p *equalityLossyAllPlanner[T, V]) compile(limit uint64) (Rule[T], error) {
	ladder, err := p.representationLadder()
	if err != nil {
		return nil, err
	}
	return selectLossyRepresentation(ladder, limit, "ruleix: Lossy equality cannot fit the memory limit")
}

func (p *equalityLossyAllPlanner[T, V]) representationLadder() ([]lossyRepresentation[T], error) {
	if p.err != nil {
		return nil, p.err
	}
	if p.ladder == nil {
		p.representations = p.prepare()
		p.prepare = nil
		p.ladder = buildLossyRepresentationLadder(p.exact, p.representations)
	}
	return p.ladder, nil
}

func equalitySetCardinality(s *equalitySet) uint64 {
	if s.bits != nil {
		return s.bits.GetCardinality()
	}
	if s.small != nil {
		return uint64(len(s.small))
	}
	return 1
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

// stored value is a wildcard and matches every concrete query value; a missing

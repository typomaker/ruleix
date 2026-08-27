package ruleix

const (
	// Work units are serialized bitmap bytes. They deliberately approximate
	// memory traffic instead of elapsed time, keeping decisions deterministic.
	allDirectIDWork        = 16
	allMaterializeIDWork   = 64
	allMaterializeBaseWork = 64
)

// estimatedBitmapWork is used only when the query cannot expose an immutable
// posting. The estimate includes the temporary payload and a fixed acquisition
// charge. Exact postings always override it with their serialized size.
func estimatedBitmapWork(cardinality uint64) (uint64, bool) {
	if cardinality == ^uint64(0) {
		return 0, false
	}
	return saturatingAdd(allMaterializeBaseWork, saturatingMul(cardinality, allMaterializeIDWork)), true
}

func rankedBitmapWork(ranked rankedBitmap) (uint64, bool) {
	if ranked.bits != nil {
		return ranked.bits.GetSerializedSizeInBytes(), true
	}
	return estimatedBitmapWork(ranked.card)
}

func rankedBitmapNarrowWork(ranked rankedBitmap) (uint64, bool) {
	work, known := rankedBitmapWork(ranked)
	if !known {
		return 0, false
	}
	// An intersection reads both the posting and the current candidates.
	return saturatingMul(work, 2), true
}

// selectNextBitmapOperation replans the next narrowing operation from facts
// available after the previous exact operation. The scan is allocation-free;
// exact postings use their serialized payload while unmaterialized children
// include their estimated acquisition cost. Unknown work stays behind every
// costed operation and otherwise preserves schema/ranking order.
func selectNextBitmapOperation(remaining []rankedBitmap) int {
	best := 0
	bestWork, bestKnown := rankedBitmapWork(remaining[0])
	for i := 1; i < len(remaining); i++ {
		work, known := rankedBitmapWork(remaining[i])
		if known && (!bestKnown || work < bestWork) {
			best, bestWork, bestKnown = i, work, true
		}
	}
	return best
}

// allSourceTotalWork scores acquiring one child as the candidate source and
// then completing the remaining children by their cheapest known exact
// operation. Unknown operations make the score unavailable, preserving the
// measured eight-candidate fallback rather than inventing precision.
func (r *allRule[T]) allSourceTotalWork(source rankedBitmap, children []rankedBitmap) (uint64, bool) {
	total, ok := rankedBitmapWork(source)
	if !ok {
		return 0, false
	}
	for _, child := range children {
		if child.childIdx == source.childIdx {
			continue
		}
		bitmap, known := rankedBitmapNarrowWork(child)
		if !known {
			return 0, false
		}
		best := bitmap
		if r.supportsDirectIDMatch(child.childIdx) {
			best = min(best, saturatingMul(source.card, allDirectIDWork))
		}
		total = saturatingAdd(total, best)
	}
	return total, true
}

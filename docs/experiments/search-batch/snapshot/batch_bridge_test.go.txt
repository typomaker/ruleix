package ruleix

func batchExperimentStrategy(name string) batchStrategy {
	switch name {
	case "Local":
		return batchLocal
	case "Common":
		return batchCommon
	case "Full":
		return batchFull
	case "Staged":
		return batchStaged
	case "Lazy":
		return batchLazy
	case "Subgroups":
		return batchSubgroups
	case "Candidates":
		return batchCandidates
	case "Typed", "Auto":
		return batchTyped
	default:
		panic("unknown batch experiment strategy")
	}
}

// SearchBatchExperiment exposes strategies only to the external test package.
// It is absent from ordinary builds and adds no supported public API.
func (ix *Index[C, ID]) SearchBatchExperiment(conditions []C, found [][]ID, name string) map[string]uint64 {
	var work batchWork
	ix.searchBatch(conditions, found, batchExperimentStrategy(name), &work)
	return map[string]uint64{
		"anchor-lookups": work.anchorLookups, "key-captures": work.keyCaptures,
		"key-comparisons": work.keyComparisons, "mask-evaluations": work.maskEvaluations,
		"candidate-checks": work.candidateChecks, "groups": work.groups,
		"fallback-offers": work.fallbackOffers, "scratch-B": work.peakScratchBytes,
	}
}

// SearchBatchVariant avoids counter and metric-map costs in timed experiments.
func (ix *Index[C, ID]) SearchBatchVariant(conditions []C, found [][]ID, name string) {
	ix.searchBatch(conditions, found, batchExperimentStrategy(name), nil)
}

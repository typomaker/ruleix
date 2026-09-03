package ruleix

type buildFinalizer interface{ finalizeBuild() }

func cloneOrderedRuleBuildState(state *orderedRuleBuildState) *orderedRuleBuildState {
	if state == nil {
		return nil
	}
	clone := *state
	return &clone
}

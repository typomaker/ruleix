package ruleix

// quantizationLevels is the build-time contract for a nested sequence of key
// transformations. Level zero is deliberately absent from steps: it is the
// identity transformation. Each later step accepts a key produced by the
// preceding level, so rebuilding never requires the discarded exact key.
//
// The concrete equality and ordered quantizers are migrated to this contract
// by the implementation steps that follow the contract milestone.
type quantizationLevels[K any] struct {
	steps []func(K) K
}

func (q quantizationLevels[K]) key(exact K, level uint32) K {
	key := exact
	for index := uint32(0); index < level && index < uint32(len(q.steps)); index++ {
		key = q.steps[index](key)
	}
	return key
}

func (q quantizationLevels[K]) next(current K, level uint32) (K, bool) {
	if level >= uint32(len(q.steps)) {
		return current, false
	}
	return q.steps[level](current), true
}

func (q quantizationLevels[K]) terminalLevel() uint32 { return uint32(len(q.steps)) }

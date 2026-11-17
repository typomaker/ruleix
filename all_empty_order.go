package ruleix

// Encode up to eight immutable child indices in existing All padding.
func (r *allRule[T]) prepareEmptyOrder() {
	r.emptyOrder = 0
	if len(r.children) > 8 {
		return
	}
	position := uint(0)
	for pass := range 2 {
		for i, child := range r.children {
			_, cheap := unwrapExecutionRule(child).(cheapCardinalityEstimator[T])
			if cheap != (pass == 0) {
				continue
			}
			r.emptyOrder |= uint32(i+1) << (4 * position)
			position++
		}
	}
	identity := uint32(0)
	for i := range r.children {
		identity |= uint32(i+1) << (4 * uint(i))
	}
	if r.emptyOrder == identity {
		r.emptyOrder = 0
	}
}

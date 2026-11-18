package ruleix

// batchContext follows Local acquisition and observation policy, but its owned
// lifetime lets SearchBatch keep the handle on the stack. Existing Local's
// allocation and public lifecycle remain unchanged.
func (ix *Index[C, ID]) batchContext() Local[C, ID] {
	localOrdinal := ix.localTelemetry.Add(1)
	sampled := localOrdinal%64 == 0
	observed := (ix.rootMetrics != nil || ix.localInspectors != nil) && sampled
	pools := &ix.locals
	if observed {
		pools = &ix.observedLocals
	}
	pool, _ := pools.Get().(*bitmapPool)
	if pool == nil {
		if observed {
			pool = newLocalBitmapPool(ix.nodes, ix.localInspectors)
		} else {
			pool = newLocalBitmapPool(ix.nodes)
		}
	}
	if observed {
		pool.bindRootInspector(ix.rootMetrics)
	}
	return Local[C, ID]{index: ix, pool: pool, observed: observed}
}

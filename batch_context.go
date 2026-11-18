package ruleix

// batchContext acquires a request-owned context without allocating its handle.
func (ix *Index[C, ID]) batchContext() localContext[C, ID] {
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
	return localContext[C, ID]{index: ix, pool: pool, observed: observed}
}

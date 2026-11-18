package ruleix

// These test-only bridges preserve single-query kernel and lifecycle controls.
// They are absent from the package API in normal builds.
type TestContext[C any, ID comparable] = localContext[C, ID]

func (ix *Index[C, ID]) SearchOne(value C, dst *[]ID) bool {
	if dst == nil {
		panic("ruleix: nil search destination")
	}
	return ix.search(value, dst, ix.pool)
}

func (ix *Index[C, ID]) TestLocal() *localContext[C, ID] {
	context := ix.batchContext()
	return &context
}

func (local *localContext[C, ID]) SearchOne(value C, dst *[]ID) bool {
	local.requireOpen()
	if dst == nil {
		panic("ruleix: nil search destination")
	}
	return local.index.search(value, dst, local.pool)
}

func (local *localContext[C, ID]) Close() { local.close() }

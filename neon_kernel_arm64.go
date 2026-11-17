//go:build arm64 && !purego

package ruleix

//go:noescape
func neonBetweenMask(data *packedBetweenLanes, lower, upper uint64) uint64

//go:noescape
func neonSimpleBetweenMask(data *[8]uint32, lower, upper uint64) uint64

const packedBetweenAvailable = true

// Compressing the source ordinal to the index's uint32 node-ID domain leaves
// room for the scratch owner within the existing candidate-mask allocation.
type localCandidatePlatform struct {
	state *localAllIntersection
}

func (m *localCandidateMask) rememberPackedState(pool *bitmapPool) {
	m.platform.state = &pool.partial
}
func (m *localCandidateMask) packedState() *localAllIntersection {
	return m.platform.state
}

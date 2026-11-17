//go:build !arm64 || purego

package ruleix

func (r *smallBetweenRule[T, V]) applyVectorCandidateMask(v T, ids []uint32, mask uint64) uint64 {
	return r.applyCandidateMask(v, ids, mask)
}

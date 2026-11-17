//go:build !arm64 || purego

package ruleix

func neonBetweenMask(data *packedBetweenLanes, lower, upper uint64) uint64 {
	return goBetweenMask(data, lower, upper)
}
func neonSimpleBetweenMask(data *[8]uint32, lower, upper uint64) uint64 {
	var result uint64
	for slot := range 16 {
		pair := uint16(data[slot/2] >> (16 * (slot % 2)))
		lo, hi := uint64(byte(pair)), uint64(byte(pair>>8))
		if lo != 0 && lo <= lower && hi > upper {
			result |= 1 << slot
		}
	}
	return result
}

const packedBetweenAvailable = false

type localCandidatePlatform struct{}

func (m *localCandidateMask) rememberPackedState(_ *bitmapPool)  {}
func (m *localCandidateMask) packedState() *localAllIntersection { return nil }

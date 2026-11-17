package ruleix

// packedBetweenLanes holds candidate-order bounds. Wildcard bytes are 0 or 0xff.
type packedBetweenLanes struct {
	lower, upper, lowerWild, upperWild [16]byte
}

func goBetweenMask(data *packedBetweenLanes, lower, upper uint64) uint64 {
	var result uint64
	for slot := range 16 {
		if ((data.lower[slot] != 0 && uint64(data.lower[slot]) <= lower) || data.lowerWild[slot] != 0) &&
			(uint64(data.upper[slot]) > upper || data.upperWild[slot] != 0) {
			result |= 1 << slot
		}
	}
	return result
}

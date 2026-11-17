package ruleix

import (
	"math/rand/v2"
	"testing"
)

func TestNEONBetweenMask(t *testing.T) {
	rng := rand.New(rand.NewPCG(13, 29))
	var data packedBetweenLanes
	for trial := range 100000 {
		for slot := range 16 {
			data.lower[slot] = byte(rng.Uint32())
			data.upper[slot] = byte(rng.Uint32())
			data.lowerWild[slot] = 0
			data.upperWild[slot] = 0
			if rng.IntN(5) == 0 {
				data.lowerWild[slot] = 255
			}
			if rng.IntN(5) == 0 {
				data.upperWild[slot] = 255
			}
		}
		lower, upper := uint64(rng.IntN(256)), uint64(rng.IntN(256))
		want := goBetweenMask(&data, lower, upper)
		if got := neonBetweenMask(&data, lower, upper); got != want {
			t.Fatalf("trial %d lower %d upper %d got %x want %x", trial, lower, upper, got, want)
		}
	}
}

func TestNEONSimpleBetweenMask(t *testing.T) {
	rng := rand.New(rand.NewPCG(31, 47))
	var data packedBetweenLanes
	var pairs [8]uint32
	for trial := range 50000 {
		for lane := range 16 {
			data.lower[lane] = byte(rng.Uint32())
			data.upper[lane] = byte(rng.Uint32())
			pair := uint16(data.lower[lane]) | uint16(data.upper[lane])<<8
			shift := uint(16 * (lane % 2))
			pairs[lane/2] = pairs[lane/2]&^(uint32(65535)<<shift) | uint32(pair)<<shift
		}
		lower, upper := uint64(rng.IntN(256)), uint64(rng.IntN(256))
		want := goBetweenMask(&data, lower, upper)
		if got := neonSimpleBetweenMask(&pairs, lower, upper); got != want {
			t.Fatalf("trial %d got %x want %x", trial, got, want)
		}
	}
}

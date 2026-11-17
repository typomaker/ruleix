package ruleix

import (
	"testing"
	"unsafe"
)

func TestPackedBetweenCacheMatchesScalar(t *testing.T) {
	const count = 320
	rows := make([]betweenIDFixture, count)
	ids := make([]int, count)
	for id := range rows {
		rows[id] = betweenIDFixture{category: id % 16, from: id % 16, until: 20 + id%12}
		ids[id] = id
	}
	_, root, _, child := buildBetweenIDFixture(t, rows, ids, false, 0, false)
	operand := root.execution[child].directID.(*smallBetweenRule[betweenIDFixture, int])
	candidates := []uint32{0, 1, 7, 11, 35, 44, 77, 78, 90, 100, 101, 110, 111, 300, 319, 900}
	for _, owner := range []int{0, 3, 7} {
		state := &localAllIntersection{}
		for step := range 2048 {
			query := betweenIDFixture{from: step%40 - 4, until: step*7%45 - 3, missingFrom: step%7 == 0, missingUntil: step%11 == 0}
			active := uint64(uint16(step * 137))
			want := operand.applyCandidateMask(query, candidates, active)
			got := operand.applyCachedBetweenMask(query, candidates, active, owner, state)
			if got != want {
				t.Fatalf("owner %d step %d got %x want %x", owner, step, got, want)
			}
			// Another operand uses the ordinary scalar matcher while the first owns scratch.
			got = operand.applyCachedBetweenMask(query, candidates, active, (owner+1)%8, state)
			if got != want {
				t.Fatalf("different owner step %d got %x want %x", step, got, want)
			}
		}
	}
	// Wildcard operands retain ordinary scalar matching.
	rows[0].missingFrom = true
	rows[1].missingUntil = true
	_, wildRoot, _, wildChild := buildBetweenIDFixture(t, rows, ids, false, 0, false)
	wild := wildRoot.execution[wildChild].directID.(*smallBetweenRule[betweenIDFixture, int])
	state := &localAllIntersection{}
	query := betweenIDFixture{from: 12, until: 27}
	if got, want := wild.applyCachedBetweenMask(query, candidates, 65535, 0, state), wild.applyCandidateMask(query, candidates, 65535); got != want {
		t.Fatalf("wildcard got %x want %x", got, want)
	}
}

// Keep the portable prepared path within the original candidate-mask footprint.
func TestPackedCandidateStorageFootprint(t *testing.T) {
	type originalMask struct {
		source *equalitySet
		ids    [16]uint32
		first  int
		count  uint8
	}
	if got, limit := unsafe.Sizeof(localCandidateMask{}), unsafe.Sizeof(originalMask{}); got > limit {
		t.Fatalf("candidate mask grew from %d to %d bytes", limit, got)
	}
}

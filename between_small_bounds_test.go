package ruleix

import (
	"slices"
	"testing"
)

func TestSmallBetweenBoundsPreserveDirectMatching(t *testing.T) {
	const count = 320
	stored := make([]betweenIDFixture, count, count+2)
	ids := make([]int, count, count+2)
	for i := range stored {
		stored[i] = betweenIDFixture{i % 16, i % 16, 20 + i%12, i%7 == 0, i%11 == 0}
		ids[i] = i
	}
	stored = append(stored,
		betweenIDFixture{category: 1, from: -4, until: 21},
		betweenIDFixture{category: 1, from: 8, until: 36},
	)
	ids = append(ids, 1, 1)
	stored[count-1] = betweenIDFixture{category: 15, missingFrom: true, missingUntil: true}

	index, root, rule, child := buildBetweenIDFixture(t, stored, ids, false, 0, false)
	matcher, ok := root.execution[child].directID.(*smallBetweenRule[betweenIDFixture, int])
	if !ok {
		t.Fatal("expected compact endpoint dictionary")
	}
	local := index.TestLocal()
	defer local.Close()
	for i := -5; i < 36; i++ {
		query := betweenIDFixture{(i + 5) % 16, i, i + 19, i%5 == 0, i%7 == 0}
		for id := range uint32(count) {
			want := rule.from.matchesID(query, id) && rule.until.matchesID(query, id)
			if got := matcher.matchesID(query, id); got != want {
				t.Fatalf("query %+v changed match for ID %d", query, id)
			}
		}
		var want, got []int
		index.SearchOne(query, &want)
		local.SearchOne(query, &got)
		if !slices.Equal(got, want) {
			t.Fatalf("query %+v Local=%v Index=%v", query, got, want)
		}
	}

	_, limited, _, child := buildBetweenIDFixture(t, stored, ids, true, 2<<20, false)
	if _, ok := limited.execution[child].directID.(*smallBetweenRule[betweenIDFixture, int]); ok {
		t.Fatal("bounded Lossy build acquired unaccounted endpoint metadata")
	}
}

func TestSmallBetweenBoundsLeavesLargePairDictionariesUnchanged(t *testing.T) {
	const count = 17 * 17
	stored := make([]betweenIDFixture, count)
	ids := make([]int, count)
	for id := range stored {
		stored[id] = betweenIDFixture{category: id % 5, from: id % 17, until: 20 + id/17}
		ids[id] = id
	}
	_, root, _, child := buildBetweenIDFixture(t, stored, ids, false, 0, false)
	if _, ok := root.execution[child].directID.(*smallBetweenRule[betweenIDFixture, int]); ok {
		t.Fatal("oversized pair dictionary should use ordinary bound lookup")
	}
}

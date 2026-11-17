package ruleix

import (
	"cmp"
	"slices"
	"testing"
	"unsafe"
)

type betweenIDFixture struct {
	category, from, until     int
	missingFrom, missingUntil bool
}

type betweenIDMatcher struct {
	rule        *betweenRule[betweenIDFixture, int]
	from, until []uint32
}

// A position encodes a block and an item. Zero indicates no concrete bound.
// The experiment uses fixtures with fewer than 2^24 blocks and 256 items per block.
func newBetweenIDMatcher(rule *betweenRule[betweenIDFixture, int], count int) *betweenIDMatcher {
	matcher := &betweenIDMatcher{rule: rule, from: make([]uint32, count), until: make([]uint32, count)}
	for _, side := range []struct {
		rule      *orderedRule[betweenIDFixture, int]
		positions []uint32
		minimum   bool
	}{{rule.from, matcher.from, true}, {rule.until, matcher.until, false}} {
		for bi, block := range side.rule.index.blocks {
			for pi, item := range block.items {
				position := uint32(bi<<8|pi) + 1
				item.bits.Iterate(func(id uint32) bool {
					if !side.minimum || side.positions[id] == 0 {
						side.positions[id] = position
					}
					return true
				})
			}
		}
	}
	return matcher
}

func (m *betweenIDMatcher) matchesID(v betweenIDFixture, id uint32) bool {
	return matchesBetweenIDBound(m.rule.from, m.from[id], v, id) &&
		matchesBetweenIDBound(m.rule.until, m.until[id], v, id)
}

func matchesBetweenIDBound(side *orderedRule[betweenIDFixture, int], position uint32, v betweenIDFixture, id uint32) bool {
	if side.wildcard.Contains(id) {
		return true
	}
	value, ok := side.get(v)
	if !ok || position == 0 {
		return false
	}
	position--
	item := side.index.blocks[position>>8].items[position&255]
	order := side.compare(item.value, value)
	if side.dir == greaterThan {
		return order <= 0
	}
	return order >= 0
}

func buildBetweenIDExperiment(tb testing.TB, stored []betweenIDFixture, ids []int, lossy bool) (*Index[betweenIDFixture, int], *allRule[betweenIDFixture], *betweenRule[betweenIDFixture, int], int) {
	tb.Helper()
	return buildBetweenIDFixture(tb, stored, ids, lossy, 64*1024, false)
}

func buildBetweenIDFixture(tb testing.TB, stored []betweenIDFixture, ids []int, lossy bool, limit uint64, compact bool) (*Index[betweenIDFixture, int], *allRule[betweenIDFixture], *betweenRule[betweenIDFixture, int], int) {
	tb.Helper()
	var between Rule[betweenIDFixture] = Between(
		func(v betweenIDFixture) (int, bool) { return v.from, !v.missingFrom },
		func(v betweenIDFixture) (int, bool) { return v.until, !v.missingUntil },
		cmp.Compare[int],
	)
	if lossy {
		between = Lossy(between, MemoryLimit(limit))
	}
	index, _, err := buildIndexPhysicalAliases(All(
		Include(func(v betweenIDFixture) (int, bool) { return v.category, true }),
		between,
	), Zip(stored, ids), false, nil, buildOptions{
		compilePhysicalAliases: true, compileStrictAntonyms: true, enableStreaming: true, disableBetweenCompaction: !compact,
	})
	if err != nil {
		tb.Fatal(err)
	}
	root := index.root.(*allRule[betweenIDFixture])
	for i, child := range root.children {
		switch rule := unwrapExecutionRule(child).(type) {
		case *betweenRule[betweenIDFixture, int]:
			return index, root, rule, i
		case *smallBetweenRule[betweenIDFixture, int]:
			return index, root, rule.betweenRule, i
		}
	}
	tb.Fatal("Between operand not found")
	return nil, nil, nil, 0
}

func TestBetweenIDExperiment(t *testing.T) {
	for _, lossy := range []bool{false, true} {
		stored := make([]betweenIDFixture, 800)
		ids := make([]int, len(stored))
		for i := range stored {
			stored[i] = betweenIDFixture{i % 200, i - 400, 400 - i, i%7 == 0, i%9 == 0}
			ids[i] = i % 400
		}
		index, root, rule, position := buildBetweenIDExperiment(t, stored, ids, lossy)
		old := root.execution[position].directID
		matcher := newBetweenIDMatcher(rule, 400)
		for i := -420; i <= 420; i += 3 {
			q := betweenIDFixture{(i + 420) % 200, i, -i, i%11 == 0, i%13 == 0}
			for id := range uint32(400) {
				if got, want := matcher.matchesID(q, id), old.matchesID(q, id); got != want {
					t.Fatalf("lossy=%t query=%+v id=%d got=%t want=%t", lossy, q, id, got, want)
				}
			}
			var want, got []int
			index.Search(q, &want)
			root.execution[position].directID = matcher
			index.Search(q, &got)
			local := index.Local()
			var localGot []int
			local.Search(q, &localGot)
			local.Close()
			root.execution[position].directID = old
			if !slices.Equal(got, want) || !slices.Equal(localGot, want) {
				t.Fatalf("lossy=%t query=%+v Index=%v Local=%v want=%v", lossy, q, got, localGot, want)
			}
		}
	}
}

var betweenIDExperimentSink any

// On Apple M1 Max and Go 1.27.3, five 150 ms runs at 32,768 intervals
// measured 4.74 → 0.20 us/op for Index with one candidate and 75.38 → 0.51
// us/op with eight. The table adds 262,200 retained bytes. See
// docs/between-id-experiment.md for commands, Local, and Build results.
func BenchmarkBetweenIDExperiment(b *testing.B) {
	const size = 32768
	for _, candidates := range []int{1, 8} {
		name := "One"
		if candidates == 8 {
			name = "Eight"
		}
		stored := make([]betweenIDFixture, size)
		ids := make([]int, size)
		for i := range stored {
			stored[i] = betweenIDFixture{category: i % (size / candidates), from: i, until: size + i}
			ids[i] = i
		}
		for _, path := range []string{"Index", "Local", "Build"} {
			if path == "Build" && candidates == 8 {
				continue
			}
			for _, variant := range []string{"Baseline", "Table"} {
				b.Run(name+"/"+path+"/"+variant, func(b *testing.B) {
					b.ReportAllocs()
					if path == "Build" {
						for b.Loop() {
							index, root, rule, position := buildBetweenIDExperiment(b, stored, ids, false)
							if variant == "Table" {
								root.execution[position].directID = newBetweenIDMatcher(rule, size)
							}
							betweenIDExperimentSink = index
						}
						return
					}
					index, root, rule, position := buildBetweenIDExperiment(b, stored, ids, false)
					if variant == "Table" {
						matcher := newBetweenIDMatcher(rule, size)
						root.execution[position].directID = matcher
					}
					search := index.Search
					if path == "Local" {
						local := index.Local()
						defer local.Close()
						search = local.Search
					}
					out := make([]int, 0, candidates)
					q := betweenIDFixture{from: 3 * size / 4, until: 5 * size / 4}
					search(q, &out)
					b.ResetTimer()
					if variant == "Table" {
						m := root.execution[position].directID.(*betweenIDMatcher)
						b.ReportMetric(float64(unsafe.Sizeof(*m)+uintptr(size*2)*unsafe.Sizeof(m.from[0])), "table-B/index")
					}
					for n := 0; n < b.N; n++ {
						q.category = n & 1023
						out = out[:0]
						search(q, &out)
					}
				})
			}
		}
	}
}

package ruleix

import (
	"cmp"
	"runtime"
	"slices"
	"testing"
)

func TestCompactBetweenMatchesBaseline(t *testing.T) {
	const count = 4096
	stored := make([]betweenIDFixture, count, count+64)
	ids := make([]int, count, count+64)
	for i := range stored {
		stored[i] = betweenIDFixture{i % 128, i - count/2, count + i, i%109 == 0, i%113 == 0}
		ids[i] = i
	}
	for i := range 64 {
		stored = append(stored, betweenIDFixture{i % 128, count + i, i, i%3 == 0, i%5 == 0})
		ids = append(ids, i)
	}
	stored = append(stored, stored[2049])
	ids = append(ids, 2048)
	for _, mode := range []struct {
		name                    string
		lossy, packed, shuffled bool
		limit                   uint64
	}{
		{"Exact", false, true, false, 0},
		{"Shuffled", false, true, true, 0},
		{"LossyHead", true, true, false, 2 << 20},
		{"LossyMerged", true, false, false, 64 << 10},
	} {
		t.Run(mode.name, func(t *testing.T) {
			checkCompactBetween(t, stored, ids, mode.lossy, mode.packed, mode.shuffled, mode.limit)
		})
	}
}

func TestBetweenCompactionKeepsSharedSingletons(t *testing.T) {
	const count = 4096
	stored := make([]betweenIDFixture, count)
	ids := make([]int, count)
	for i := range stored {
		stored[i] = betweenIDFixture{from: i, until: count + i}
		ids[i] = i
	}
	makeRule := func() Rule[betweenIDFixture] {
		return Between(
			func(v betweenIDFixture) (int, bool) { return v.from, true },
			func(v betweenIDFixture) (int, bool) { return v.until, true },
			cmp.Compare[int],
		)
	}
	index, err := New[betweenIDFixture, int](All(makeRule(), makeRule())).Build(Zip(stored, ids))
	if err != nil {
		t.Fatal(err)
	}
	children := index.root.(*allRule[betweenIDFixture]).children
	if len(children) != 2 {
		t.Fatalf("expected two independently compiled Between nodes, got %d", len(children))
	}
	left := children[0].(*betweenRule[betweenIDFixture, int])
	right := children[1].(*betweenRule[betweenIDFixture, int])
	if left.compact != nil || right.compact != nil {
		t.Fatal("node charged savings for a bitmap still held by another node")
	}
	if left.from.index.blocks[0].items[0].bits != right.from.index.blocks[0].items[0].bits {
		t.Fatal("expected singleton posting to be interned")
	}
}

// Apple M1 Max, Go 1.27.3, five 180 ms runs at 32,768 intervals:
// Index went from 5.33 to 0.21 us/op for one equality candidate and from
// 79.60 to 0.51 us/op for eight. See docs/between-compact-bounds.md.
func BenchmarkCompactBetween(b *testing.B) {
	const size = 32768
	for _, shape := range []struct {
		name       string
		candidates int
		shuffled   bool
	}{
		{"One", 1, false}, {"Eight", 8, false}, {"Broad", 8192, false}, {"BroadShuffled", 8192, true},
	} {
		stored := make([]betweenIDFixture, size)
		ids := make([]int, size)
		for i := range stored {
			key := i
			if shape.shuffled {
				key = (i * 8191) % size
			}
			stored[i] = betweenIDFixture{category: i % (size / shape.candidates), from: key, until: size + key}
			ids[i] = i
		}
		for _, path := range []string{"Index", "Local", "Build"} {
			if path == "Build" && shape.candidates != 1 {
				continue
			}
			for _, version := range []string{"Baseline", "Compact"} {
				b.Run(shape.name+"/"+path+"/"+version, func(b *testing.B) {
					benchmarkCompactBetweenCase(b, stored, ids, path, version, shape.candidates)
				})
			}
		}
	}
}

// Apple M1 Max, Go 1.27.3, median retained heap across three 3x runs:
// 10,754,995 bytes per baseline index and 6,562,851 per compact index.
func BenchmarkCompactBetweenRetained(b *testing.B) {
	const size = 32768
	stored := make([]betweenIDFixture, size)
	ids := make([]int, size)
	for i := range stored {
		stored[i] = betweenIDFixture{category: i, from: i, until: size + i}
		ids[i] = i
	}
	for _, version := range []string{"Baseline", "Compact"} {
		b.Run(version, func(b *testing.B) {
			var before, after runtime.MemStats
			indexes := make([]*Index[betweenIDFixture, int], b.N)
			runtime.GC()
			runtime.ReadMemStats(&before)
			for i := range indexes {
				index, _, _, _ := buildBetweenIDFixture(b, stored, ids, false, 0, version == "Compact")
				indexes[i] = index
			}
			runtime.GC()
			runtime.ReadMemStats(&after)
			runtime.KeepAlive(indexes)
			b.ReportMetric(float64(int64(after.HeapAlloc)-int64(before.HeapAlloc))/float64(b.N), "retained-B/index")
		})
	}
}

func checkCompactBetween(
	t *testing.T,
	stored []betweenIDFixture,
	ids []int,
	lossy, expectPacked, shuffled bool,
	limit uint64,
) {
	t.Helper()
	const count = 4096
	if shuffled {
		stored = slices.Clone(stored)
		for i := range count {
			stored[i].from = i*1023%count - count/2
			stored[i].until = count + i*1023%count
		}
	}
	baseline, _, original, _ := buildBetweenIDFixture(t, stored, ids, lossy, limit, false)
	candidate, _, packed, _ := buildBetweenIDFixture(t, stored, ids, lossy, limit, true)
	if (packed.compact != nil) != expectPacked {
		t.Fatalf("compaction active = %t, want %t", packed.compact != nil, expectPacked)
	}
	local := candidate.TestLocal()
	defer local.Close()
	for i := -2113; i < 6200; i += 19 {
		q := betweenIDFixture{(i + 2113) % 128, i, i + 4200, i%7 == 0, i%9 == 0}
		if got, want := packed.estimateCardinality(q), original.estimateCardinality(q); got != want {
			t.Fatalf("query %+v estimate %d, want %d", q, got, want)
		}
		for id := uint32((i + 2113) % 8); id < count; id += 8 {
			if packed.matchesID(q, id) != original.matchesID(q, id) {
				t.Fatalf("query %+v changed ID %d", q, id)
			}
		}
		var want, got, warm []int
		baseline.SearchOne(q, &want)
		candidate.SearchOne(q, &got)
		local.SearchOne(q, &warm)
		if !slices.Equal(got, want) || !slices.Equal(warm, want) {
			t.Fatalf("query %+v Index=%v Local=%v want=%v", q, got, warm, want)
		}
	}
}

func benchmarkCompactBetweenCase(
	b *testing.B,
	stored []betweenIDFixture,
	ids []int,
	path,
	version string,
	candidates int,
) {
	const size = 32768
	b.ReportAllocs()
	compact := version == "Compact"
	build := func() *Index[betweenIDFixture, int] {
		index, _, rule, _ := buildBetweenIDFixture(b, stored, ids, false, 0, compact)
		if (rule.compact != nil) != compact {
			b.Fatal("unexpected compaction decision")
		}
		return index
	}
	if path == "Build" {
		for b.Loop() {
			betweenIDExperimentSink = build()
		}
		return
	}
	index := build()
	search := index.SearchOne
	if path == "Local" {
		local := index.TestLocal()
		defer local.Close()
		search = local.SearchOne
	}
	out := make([]int, 0, candidates)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q := betweenIDFixture{category: i % min(size/candidates, 1024), from: 3*size/4 + i%61, until: 5*size/4 + i%59}
		out = out[:0]
		search(q, &out)
	}
}

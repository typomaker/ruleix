package ruleix

import (
	"cmp"
	"math"
	"slices"

	"github.com/RoaringBitmap/roaring/v2"
)

// postingGeneration is mutable build-only state. A successful rebuild returns
// an independent generation, allowing its owner to publish it with one
// assignment and release the old map afterwards.
type postingGeneration[K comparable] map[K]*roaring.Bitmap

// lossyBuildState owns the only mutable posting generation for one lossy
// list. Exact input and search keys pass through the same current level. The
// next operator migrations adapt this build-only holder to their existing
// physical indexes before publication.
type lossyBuildState[K cmp.Ordered] struct {
	generation    postingGeneration[K]
	levels        quantizationLevels[K]
	level         uint32
	entryBytes    uint64
	retainedBytes uint64
}

// lossyRebuildAccounting deliberately separates the candidate generation's
// temporary allocation from the retained bytes owned after publication.
type lossyRebuildAccounting struct {
	retainedBytes  uint64
	transientBytes uint64
}

func newLossyBuildState[K cmp.Ordered](levels quantizationLevels[K], entryBytes uint64) lossyBuildState[K] {
	return lossyBuildState[K]{
		generation: make(postingGeneration[K]),
		levels:     levels,
		entryBytes: entryBytes,
	}
}

func (s *lossyBuildState[K]) storageKey(exact K) K {
	return s.levels.key(exact, s.level)
}

func (s *lossyBuildState[K]) searchKey(exact K) K {
	return s.storageKey(exact)
}

// insert adds to the current physical generation only after its deterministic
// retained accounting has been checked. A failed insert leaves the state
// unchanged.
func (s *lossyBuildState[K]) insert(exact K, id uint32) bool {
	key := s.storageKey(exact)
	current := s.generation[key]
	next := roaring.New()
	if current != nil {
		next = current.Clone()
	}
	next.Add(id)
	before := uint64(0)
	if current != nil {
		before = bitmapBytes(current)
	}
	after := bitmapBytes(next)
	usage := s.retainedBytes
	if current == nil {
		var ok bool
		usage, ok = addLossyMemory(usage, s.entryBytes)
		if !ok {
			return false
		}
	}
	if after >= before {
		var ok bool
		usage, ok = addLossyMemory(usage, after-before)
		if !ok {
			return false
		}
	} else {
		usage -= before - after
	}
	s.generation[key] = next
	s.retainedBytes = usage
	return true
}

// rebuildNext constructs and accounts a complete independent generation. It
// publishes with one assignment only after every key has been transformed and
// accounted. On success, clearing the old map drops all bitmap references held
// by this state; on failure, the current generation and level stay untouched.
func (s *lossyBuildState[K]) rebuildNext() (lossyRebuildAccounting, bool) {
	if s.level >= s.levels.terminalLevel() {
		return lossyRebuildAccounting{}, false
	}
	next, usage, ok := rebuildPostingGeneration(
		s.generation, len(s.generation), s.entryBytes,
		func(key K) K {
			coarser, _ := s.levels.next(key, s.level)
			return coarser
		},
	)
	if !ok {
		return lossyRebuildAccounting{}, false
	}
	accounting := lossyRebuildAccounting{
		retainedBytes:  usage,
		transientBytes: usage,
	}
	old := s.generation
	s.generation, s.level, s.retainedBytes = next, s.level+1, usage
	for key := range old {
		delete(old, key)
	}
	return accounting, true
}

// rebuildPostingGeneration transforms every old key and merges postings that
// collide at the coarser precision. The returned accounting includes the map
// entry charge and serialized bitmap bytes. It never mutates old.
func rebuildPostingGeneration[K cmp.Ordered](
	old postingGeneration[K],
	capacity int,
	entryBytes uint64,
	coarsen func(K) K,
) (postingGeneration[K], uint64, bool) {
	keys := make([]K, 0, len(old))
	for key := range old {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return rebuildPostingGenerationInOrder(old, keys, capacity, entryBytes, coarsen)
}

func rebuildPostingGenerationInOrder[K comparable](
	old postingGeneration[K], keys []K, capacity int, entryBytes uint64, coarsen func(K) K,
) (postingGeneration[K], uint64, bool) {
	if capacity < 0 {
		return nil, 0, false
	}
	next := make(postingGeneration[K], min(len(old), capacity))
	for _, key := range keys {
		bits := old[key]
		coarser := coarsen(key)
		if merged := next[coarser]; merged != nil {
			merged.Or(bits)
		} else if bits != nil {
			next[coarser] = bits.Clone()
		}
	}

	usage := uint64(0)
	for _, bits := range next {
		if math.MaxUint64-usage < entryBytes {
			return nil, 0, false
		}
		usage += entryBytes
		bytes := bitmapBytes(bits)
		if math.MaxUint64-usage < bytes {
			return nil, 0, false
		}
		usage += bytes
	}
	return next, usage, true
}

package ruleix

import (
	"fmt"
	"iter"
	"math"
)

// Builder constructs immutable indexes from the same Rule schema. A Builder is
// not safe for concurrent calls to Build; callers that need them must provide
// their own synchronization. Indexes returned by completed builds are
// independent and safe for concurrent use.
type Builder[C any, ID comparable] struct {
	schema Rule[C]
	hints  buildStatistics
}

// Index maps query values to the unique IDs of all matching stored constraints.
// It is immutable after Build and safe for concurrent calls to Search and Visit.
type Index[C any, ID comparable] struct {
	root   Rule[C]
	values []ID
	pool   *bitmapPool
	nodes  int
}

// Local is a search context that keeps per-node cached results between calls.
// It must not be used concurrently by multiple goroutines.
type Local[C any, ID comparable] struct {
	index *Index[C, ID]
	pool  *bitmapPool
}

// New constructs a Builder from a strongly typed rule schema. New panics when
// schema is nil.
func New[C any, ID comparable](schema Rule[C]) *Builder[C, ID] {
	if schema == nil {
		panic("ruleix: nil schema")
	}
	return &Builder[C, ID]{schema: schema}
}

// Build consumes entries and returns an immutable, concurrently searchable
// Index. Constraints that share an external ID are combined under that ID,
// which is returned at most once by a search. Build must not be called
// concurrently on the same Builder; the library deliberately leaves
// synchronization of builds to the caller. A failed build does not prevent
// later calls.
func (b *Builder[C, ID]) Build(entries iter.Seq2[C, ID]) (*Index[C, ID], error) {
	ix, statistics, err := buildIndex[C, ID](b.schema, entries, true, &b.hints)
	if err != nil {
		return nil, err
	}
	// Publish statistics only after the input sequence has returned and the
	// complete index has passed validation. Failed builds must leave the last
	// successful hints intact for the next rebuild.
	b.hints = statistics
	return ix, nil
}

func buildIndex[C any, ID comparable](
	schema Rule[C],
	entries iter.Seq2[C, ID],
	collectStatistics bool,
	hints *buildStatistics,
) (*Index[C, ID], buildStatistics, error) {
	if entries == nil {
		return nil, buildStatistics{}, fmt.Errorf("ruleix: nil entry sequence")
	}
	// A schema contains only immutable getters, comparators, and structure.
	// Every build receives fresh mutable indexes, so even a future reusable
	// schema cannot modify an Index returned by an earlier build.
	ids := &nodeIDAllocator{}
	state := schema.newState(ids, hints)
	uniqueIDCapacity := 0
	if hints != nil {
		uniqueIDCapacity = capacityHint(hints.uniqueIDs)
	}
	ix := &Index[C, ID]{root: state, values: make([]ID, 0, uniqueIDCapacity), pool: newBitmapPool()}
	internalIDs := make(map[ID]uint32, uniqueIDCapacity)
	var buildErr error
	entryIndex := 0
	entries(func(constraint C, id ID) bool {
		if uint64(len(ix.values)) > math.MaxUint32 {
			buildErr = fmt.Errorf("ruleix: at most 2^32 rules are supported")
			return false
		}
		if err := ix.root.validate(constraint); err != nil {
			buildErr = fmt.Errorf("ruleix: entry %d: %w", entryIndex, err)
			return false
		}
		internalID, exists := internalIDs[id]
		if !exists {
			internalID = uint32(len(ix.values))
			internalIDs[id] = internalID
			ix.values = append(ix.values, id)
		}
		ix.root.insert(constraint, internalID)
		entryIndex++
		return true
	})
	if buildErr != nil {
		return nil, buildStatistics{}, buildErr
	}
	statistics := buildStatistics{entries: entryIndex, uniqueIDs: len(ix.values)}
	if collectStatistics {
		statistics.nodes = make([]nodeBuildStatistics, int(ids.next))
		ix.root.collectBuildStatistics(statistics.nodes)
	}
	ix.root = optimizeRule(ix.root, uint64(len(ix.values)))
	prepareRuleSearch(ix.root)
	ix.nodes = int(ids.next)
	return ix, statistics, nil
}

// Zip pairs equally sized constraint and ID slices into a sequence accepted by
// Builder.Build. It panics when the slice lengths differ. The slices are read
// when the returned sequence is consumed, not when Zip is called.
func Zip[C any, ID any](constraints []C, ids []ID) iter.Seq2[C, ID] {
	if len(constraints) != len(ids) {
		panic(fmt.Sprintf("ruleix: cannot zip %d constraints with %d IDs", len(constraints), len(ids)))
	}
	return func(yield func(C, ID) bool) {
		for i := range constraints {
			if !yield(constraints[i], ids[i]) {
				return
			}
		}
	}
}

// Search writes the unique IDs of every stored rule matching value into dst.
// It resets the destination length while reusing its capacity and updates the
// slice through its pointer if append allocates a larger backing array. Results
// preserve first-insertion order. Search panics when dst is nil.
func (ix *Index[C, ID]) Search(value C, dst *[]ID) {
	if dst == nil {
		panic("ruleix: nil search destination")
	}
	ix.search(value, dst, ix.pool)
}

// Local returns a search context that caches the two most recent intermediate
// bitmap results per filter node. It can reduce repeated work when adjacent
// searches share constraint values, at the cost of retaining those bitmaps for
// the lifetime of the Local.
//
// A Local is not safe for concurrent use. Create one per goroutine:
//
//	local := index.Local()
//	var matches []ID
//	for value := range values {
//		local.Search(value, &matches)
//	}
//
// The Index remains immutable and may be shared by all of those goroutines.
func (ix *Index[C, ID]) Local() *Local[C, ID] {
	return &Local[C, ID]{index: ix, pool: newLocalBitmapPool(ix.nodes)}
}

// Search writes matching IDs into dst while reusing this Local's cached state.
// Search panics when dst is nil.
func (local *Local[C, ID]) Search(value C, dst *[]ID) {
	if dst == nil {
		panic("ruleix: nil search destination")
	}
	local.index.search(value, dst, local.pool)
}

// Visit calls yield for matching IDs while reusing this Local's cached state.
// A nil yield function is a no-op.
func (local *Local[C, ID]) Visit(value C, yield func(ID) bool) {
	if yield == nil {
		return
	}
	visitMatches(local.index.root, local.index.values, local.pool, value, yield)
}

// Visit calls yield once for each unique matching ID in first-match order.
// Iteration stops immediately when yield returns false. A nil yield function is
// a no-op.
func (ix *Index[C, ID]) Visit(value C, yield func(ID) bool) {
	if yield == nil {
		return
	}
	visitMatches(ix.root, ix.values, ix.pool, value, yield)
}

func (ix *Index[C, ID]) search(value C, dst *[]ID, pool *bitmapPool) {
	bits := pool.get()
	defer pool.put(bits)
	ix.root.search(value, bits, pool)
	excluded := pool.get()
	defer pool.put(excluded)
	ix.root.exclude(value, excluded, pool)
	bits.AndNot(excluded)
	result := (*dst)[:0]
	it := bits.Iterator()
	for it.HasNext() {
		result = append(result, ix.values[it.Next()])
	}
	*dst = result
}

func visitMatches[C any, ID comparable](root Rule[C], values []ID, pool *bitmapPool, value C, yield func(ID) bool) {
	bits := pool.get()
	defer pool.put(bits)
	root.search(value, bits, pool)
	excluded := pool.get()
	defer pool.put(excluded)
	root.exclude(value, excluded, pool)
	bits.AndNot(excluded)
	it := bits.Iterator()
	for it.HasNext() {
		id := values[it.Next()]
		if !yield(id) {
			return
		}
	}
}

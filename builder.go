package ruleix

import (
	"fmt"
	"iter"
	"math"
)

// Builder constructs one immutable Index from a Rule schema. A Builder is
// single-use: its Build method may be called only once, whether that call
// succeeds or fails.
type Builder[C any, ID comparable] struct {
	schema Rule[C]
	built  bool
}

// Rebuilder constructs a new immutable Index from the same Rule schema on
// every call to Build. A Rebuilder is not safe for concurrent calls to Build;
// callers that need them must provide their own synchronization. Indexes
// returned by completed builds are independent and safe for concurrent use.
type Rebuilder[C any, ID comparable] struct {
	schema Rule[C]
}

// Index maps query values to the unique IDs of all matching stored constraints.
// It is immutable after Build and safe for concurrent calls to Search and Visit.
type Index[C any, ID comparable] struct {
	root   Rule[C]
	values []ID
	pool   *bitmapPool
}

// New constructs a Builder from a strongly typed rule schema. New panics when
// schema is nil.
func New[C any, ID comparable](schema Rule[C]) *Builder[C, ID] {
	if schema == nil {
		panic("ruleix: nil schema")
	}
	return &Builder[C, ID]{schema: schema}
}

// NewRebuilder constructs a reusable builder from a strongly typed rule
// schema. NewRebuilder panics when schema is nil.
func NewRebuilder[C any, ID comparable](schema Rule[C]) *Rebuilder[C, ID] {
	if schema == nil {
		panic("ruleix: nil schema")
	}
	return &Rebuilder[C, ID]{schema: schema}
}

// Build consumes entries and returns an immutable, concurrently searchable
// Index. Constraints that share an external ID are combined under that ID,
// which is returned at most once by a search. A Builder is single-use,
// including when validation fails.
func (b *Builder[C, ID]) Build(entries iter.Seq2[C, ID]) (*Index[C, ID], error) {
	if b.built {
		return nil, fmt.Errorf("ruleix: builder has already been used")
	}
	b.built = true
	return buildIndex[C, ID](b.schema, entries)
}

// Build consumes entries and returns a new immutable, concurrently searchable
// Index. Build must not be called concurrently on the same Rebuilder; the
// library deliberately leaves synchronization of builds to the caller. A
// failed build does not prevent later calls.
func (r *Rebuilder[C, ID]) Build(entries iter.Seq2[C, ID]) (*Index[C, ID], error) {
	return buildIndex[C, ID](r.schema, entries)
}

func buildIndex[C any, ID comparable](schema Rule[C], entries iter.Seq2[C, ID]) (*Index[C, ID], error) {
	if entries == nil {
		return nil, fmt.Errorf("ruleix: nil entry sequence")
	}
	// A schema contains only immutable getters, comparators, and structure.
	// Every build receives fresh mutable indexes, so even a future reusable
	// schema cannot modify an Index returned by an earlier build.
	state := schema.newState(&nodeIDAllocator{})
	ix := &Index[C, ID]{root: state, pool: newBitmapPool()}
	internalIDs := make(map[ID]uint32)
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
		return nil, buildErr
	}
	return ix, nil
}

// Zip pairs equally sized constraint and ID slices into a sequence accepted by
// Builder.Build. It returns an error when the slice lengths differ. The slices
// are read when the returned sequence is consumed, not when Zip is called.
func Zip[C any, ID any](constraints []C, ids []ID) (iter.Seq2[C, ID], error) {
	if len(constraints) != len(ids) {
		return nil, fmt.Errorf("ruleix: cannot zip %d constraints with %d IDs", len(constraints), len(ids))
	}
	return func(yield func(C, ID) bool) {
		for i := range constraints {
			if !yield(constraints[i], ids[i]) {
				return
			}
		}
	}, nil
}

// Search writes the unique IDs of every stored rule matching value into dst.
// It resets the destination length while reusing its capacity and updates the
// slice through its pointer if append allocates a larger backing array. Results
// preserve first-insertion order. Search panics when dst is nil.
func (ix *Index[C, ID]) Search(value C, dst *[]ID) {
	if dst == nil {
		panic("ruleix: nil search destination")
	}
	ix.searchLocked(value, dst)
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

func (ix *Index[C, ID]) searchLocked(value C, dst *[]ID) {
	bits := ix.pool.get()
	defer ix.pool.put(bits)
	ix.root.search(value, bits, ix.pool)
	excluded := ix.pool.get()
	defer ix.pool.put(excluded)
	ix.root.exclude(value, excluded, ix.pool)
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

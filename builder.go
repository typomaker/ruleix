package ruleix

import (
	"fmt"
	"iter"
	"math"
)

// Builder constructs one immutable Index from a rule schema.
type Builder[C any, ID comparable] struct {
	root  Rule[C]
	built bool
}

// Index maps matching constraints to result IDs. It is immutable after Build.
type Index[C any, ID comparable] struct {
	root   Rule[C]
	values []ID
	pool   *bitmapPool
}

// New constructs a builder from a strongly typed schema.
func New[C any, ID comparable](schema Rule[C]) *Builder[C, ID] {
	if schema == nil {
		panic("ruleix: nil schema")
	}
	return &Builder[C, ID]{root: schema}
}

// Build consumes entries and returns an immutable, concurrently searchable
// index. A Builder is single-use, including when validation fails.
func (b *Builder[C, ID]) Build(entries iter.Seq2[C, ID]) (*Index[C, ID], error) {
	if b.built {
		return nil, fmt.Errorf("ruleix: builder has already been used")
	}
	b.built = true
	if entries == nil {
		return nil, fmt.Errorf("ruleix: nil entry sequence")
	}
	ix := &Index[C, ID]{root: b.root, pool: newBitmapPool()}
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

// Zip pairs equally sized constraint and ID slices into a Build sequence.
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
// It updates the slice through its pointer, including when append allocates a
// larger backing array. Results preserve the first matching insertion order.
func (ix *Index[C, ID]) Search(value C, dst *[]ID) {
	if dst == nil {
		panic("ruleix: nil search destination")
	}
	ix.searchLocked(value, dst)
}

// Visit calls yield once for each unique matching ID in first-match order.
// Iteration stops immediately when yield returns false.
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

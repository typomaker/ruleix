package ruleix

import "github.com/RoaringBitmap/roaring/v2"

// bitmapInterner replaces equal immutable posting lists with one canonical
// bitmap. Checksum narrows candidates cheaply; Equals preserves correctness in
// the event of a collision.
type bitmapInterner struct {
	canonical  map[bitmapFingerprint]internedBitmap
	collisions map[bitmapFingerprint][]internedBitmap
	nextID     physicalSourceID
}

// physicalSourceID names one collision-checked immutable bitmap within a
// single Build. Zero means that a representation has no bitmap source.
type physicalSourceID uint32

type internedBitmap struct {
	bits *roaring.Bitmap
	id   physicalSourceID
}

type bitmapFingerprint struct {
	checksum    uint64
	cardinality uint64
}

func newBitmapInterner() *bitmapInterner {
	return &bitmapInterner{
		canonical: make(map[bitmapFingerprint]internedBitmap),
		nextID:    1,
	}
}

func (i *bitmapInterner) intern(target **roaring.Bitmap) {
	i.internSource(target)
}

func (i *bitmapInterner) internSource(target **roaring.Bitmap) physicalSourceID {
	bits := *target
	fingerprint := bitmapFingerprint{checksum: bits.Checksum(), cardinality: bits.GetCardinality()}
	return i.internSourceWithFingerprint(target, fingerprint)
}

func (i *bitmapInterner) internSourceWithFingerprint(
	target **roaring.Bitmap,
	fingerprint bitmapFingerprint,
) physicalSourceID {
	bits := *target
	canonical := i.canonical[fingerprint]
	if canonical.bits == nil {
		id := i.allocateSourceID()
		i.canonical[fingerprint] = internedBitmap{bits: bits, id: id}
		return id
	}
	if canonical.bits.Equals(bits) {
		*target = canonical.bits
		return canonical.id
	}
	for _, collision := range i.collisions[fingerprint] {
		if collision.bits.Equals(bits) {
			*target = collision.bits
			return collision.id
		}
	}
	if i.collisions == nil {
		i.collisions = make(map[bitmapFingerprint][]internedBitmap)
	}
	id := i.allocateSourceID()
	i.collisions[fingerprint] = append(i.collisions[fingerprint], internedBitmap{bits: bits, id: id})
	return id
}

func (i *bitmapInterner) allocateSourceID() physicalSourceID {
	id := i.nextID
	i.nextID++
	if id == 0 {
		panic("ruleix: physical bitmap source ID overflow")
	}
	return id
}

type bitmapInternable interface {
	internBitmaps(*bitmapInterner)
}

func internRuleBitmaps[T any](rule Rule[T]) {
	internRuleWith(newBitmapInterner(), rule)
}

func internRuleWith[T any](interner *bitmapInterner, rule Rule[T]) {
	if observed, ok := rule.(*inspectedRuntimeRule[T]); ok {
		internRuleWith(interner, observed.child)
		return
	}
	if all, ok := rule.(*allRule[T]); ok {
		for _, child := range all.children {
			internRuleWith(interner, child)
		}
		return
	}
	if internable, ok := rule.(bitmapInternable); ok {
		internable.internBitmaps(interner)
	}
}

// compileAllEqualityClasses assigns ordinals only to source pairs that occur
// in more than one physical child of the same All. All maps and collision
// bookkeeping are build-local and become unreachable before publication.
func compileAllEqualityClasses[T any](rule Rule[T]) {
	if observed, ok := rule.(*inspectedRuntimeRule[T]); ok {
		compileAllEqualityClasses(observed.child)
		return
	}
	all, ok := rule.(*allRule[T])
	if !ok {
		return
	}
	all.compiledEqualityClasses = true
	for _, child := range all.children {
		compileAllEqualityClasses(child)
	}
	type occurrence struct {
		owners map[int][]func(uint32)
	}
	occurrences := make(map[equalitySourcePair]*occurrence)
	for childIndex, child := range all.children {
		compiler, ok := unwrapExecutionRule(child).(equalityClassCompiler)
		if !ok {
			continue
		}
		compiler.visitEqualitySourceClasses(func(pair equalitySourcePair, assign func(uint32)) {
			entry := occurrences[pair]
			if entry == nil {
				entry = &occurrence{owners: make(map[int][]func(uint32))}
				occurrences[pair] = entry
			}
			entry.owners[childIndex] = append(entry.owners[childIndex], assign)
		})
	}
	for _, entry := range occurrences {
		if len(entry.owners) < 2 {
			continue
		}
		all.equalityClassCount++
		for _, assigns := range entry.owners {
			for _, assign := range assigns {
				assign(all.equalityClassCount)
			}
		}
	}
}

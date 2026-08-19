package ruleix

import "github.com/RoaringBitmap/roaring/v2"

// bitmapInterner replaces equal immutable posting lists with one canonical
// bitmap. Checksum narrows candidates cheaply; Equals preserves correctness in
// the event of a collision.
type bitmapInterner struct {
	canonical  map[bitmapFingerprint]*roaring.Bitmap
	collisions map[bitmapFingerprint][]*roaring.Bitmap
}

type bitmapFingerprint struct {
	checksum    uint64
	cardinality uint64
}

func newBitmapInterner() *bitmapInterner {
	return &bitmapInterner{canonical: make(map[bitmapFingerprint]*roaring.Bitmap)}
}

func (i *bitmapInterner) intern(target **roaring.Bitmap) {
	bits := *target
	fingerprint := bitmapFingerprint{checksum: bits.Checksum(), cardinality: bits.GetCardinality()}
	canonical := i.canonical[fingerprint]
	if canonical == nil {
		i.canonical[fingerprint] = bits
		return
	}
	if canonical.Equals(bits) {
		*target = canonical
		return
	}
	for _, collision := range i.collisions[fingerprint] {
		if collision.Equals(bits) {
			*target = collision
			return
		}
	}
	if i.collisions == nil {
		i.collisions = make(map[bitmapFingerprint][]*roaring.Bitmap)
	}
	i.collisions[fingerprint] = append(i.collisions[fingerprint], bits)
}

type bitmapInternable interface {
	internBitmaps(*bitmapInterner)
}

func internRuleBitmaps[T any](rule Rule[T]) {
	internRuleWith(newBitmapInterner(), rule)
}

func internRuleWith[T any](interner *bitmapInterner, rule Rule[T]) {
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

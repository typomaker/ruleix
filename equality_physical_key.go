package ruleix

// equalityPhysicalKey is the single storage key used by exact and quantized
// equality generations. A key contains either the semantic value at identity
// level zero or a hash-prefix bucket at every coarser level. In particular, a
// bucket key keeps the exact field at V's zero value so rebuilt generations do
// not retain strings, pointers, or other data from the exact generation.
type equalityPhysicalKey[V comparable] struct {
	exact    V
	bucket   uint64
	bucketed bool
}

func exactEqualityKey[V comparable](value V) equalityPhysicalKey[V] {
	return equalityPhysicalKey[V]{exact: value}
}

func bucketEqualityKey[V comparable](bucket uint64) equalityPhysicalKey[V] {
	return equalityPhysicalKey[V]{bucket: bucket, bucketed: true}
}

package ruleix

import (
	"fmt"
	"hash/maphash"
	"math/bits"
	"reflect"
	"unsafe"
)

// equalityCodecError reports that Build could not compile a stable semantic
// hash for an equality value. It is deliberately internal until the supported
// codec surface is stable enough to define a public error contract.
type equalityCodecError struct {
	typeName string
	reason   string
}

func (e *equalityCodecError) Error() string {
	return fmt.Sprintf("ruleix: cannot compile equality codec for %s: %s", e.typeName, e.reason)
}

// equalityCodec always produces the complete 64-bit hash. Lossy precision is
// represented separately by the immutable quantizer stored on the shared
// equality search rule.
type equalityCodec[V comparable] struct {
	hash func(V) uint64
}

// equalityQuantizer is compiled together with the equality codec during
// Build. Keeping the selected precision in this concrete value makes the
// search path a direct hash-to-key transformation with no policy lookup or
// interface dispatch.
type equalityQuantizer struct {
	bucketCount uint64
}

func newEqualityQuantizer(bucketCount uint64) equalityQuantizer {
	return equalityQuantizer{bucketCount: max(bucketCount, 1)}
}

func (q equalityQuantizer) key(hash uint64) uint64 {
	upper := equalityBucketUpper(q.bucketCount)
	high, _ := bits.Mul64(hash, upper)
	return reduceEqualityBaseBucket(high, upper, q.bucketCount)
}

func (q equalityQuantizer) coarsen(key uint64, next equalityQuantizer) uint64 {
	upper := equalityBucketUpper(q.bucketCount)
	merged := upper - q.bucketCount
	base := key + merged
	if key < merged {
		base = key * 2
	}
	for upper > equalityBucketUpper(next.bucketCount) {
		base /= 2
		upper /= 2
	}
	return reduceEqualityBaseBucket(base, upper, next.bucketCount)
}

func compileEqualityCodec[V comparable]() (equalityCodec[V], error) {
	var zero V
	if _, ok := any(zero).(string); ok {
		seed := maphash.MakeSeed()
		return equalityCodec[V]{hash: func(value V) uint64 {
			return maphash.String(seed, any(value).(string))
		}}, nil
	}
	if _, ok := hashScalar(any(zero)); ok {
		return equalityCodec[V]{hash: func(value V) uint64 {
			hash, _ := hashScalar(any(value))
			return hash
		}}, nil
	}

	typeOf := reflect.TypeOf((*V)(nil)).Elem()
	codec, ok := compileNamedScalarCodec[V](typeOf)
	if ok {
		return codec, nil
	}
	plan, reason := compileEqualityPointerCodec(typeOf)
	if plan != nil {
		return equalityCodec[V]{hash: func(value V) uint64 {
			return plan(unsafe.Pointer(&value))
		}}, nil
	}
	return equalityCodec[V]{}, &equalityCodecError{
		typeName: typeOf.String(),
		reason:   reason,
	}
}

// compileNamedScalarCodec uses reflection only to select and validate an
// underlying representation. The resulting closures use fixed typed loads;
// no reflect.Value or reflective plan survives into Search.
func compileNamedScalarCodec[V comparable](typeOf reflect.Type) (equalityCodec[V], bool) {
	switch typeOf.Kind() {
	case reflect.Bool:
		return equalityCodec[V]{hash: func(value V) uint64 {
			encoded := byte(0)
			if *(*bool)(unsafe.Pointer(&value)) {
				encoded = 1
			}
			return fnvHashByte(fnvHashByte(fnvOffset64, canonicalBool), encoded)
		}}, true
	case reflect.String:
		seed := maphash.MakeSeed()
		return equalityCodec[V]{hash: func(value V) uint64 {
			return maphash.String(seed, *(*string)(unsafe.Pointer(&value)))
		}}, true
	case reflect.Int:
		return integerEqualityCodec[V, int](canonicalInt), true
	case reflect.Int8:
		return integerEqualityCodec[V, int8](canonicalInt8), true
	case reflect.Int16:
		return integerEqualityCodec[V, int16](canonicalInt16), true
	case reflect.Int32:
		return integerEqualityCodec[V, int32](canonicalInt32), true
	case reflect.Int64:
		return integerEqualityCodec[V, int64](canonicalInt64), true
	case reflect.Uint:
		return integerEqualityCodec[V, uint](canonicalUint), true
	case reflect.Uint8:
		return integerEqualityCodec[V, uint8](canonicalUint8), true
	case reflect.Uint16:
		return integerEqualityCodec[V, uint16](canonicalUint16), true
	case reflect.Uint32:
		return integerEqualityCodec[V, uint32](canonicalUint32), true
	case reflect.Uint64:
		return integerEqualityCodec[V, uint64](canonicalUint64), true
	case reflect.Uintptr:
		return integerEqualityCodec[V, uintptr](canonicalUintptr), true
	case reflect.Float32:
		return equalityCodec[V]{hash: func(value V) uint64 {
			bits := canonicalFloat32Bits(*(*float32)(unsafe.Pointer(&value)))
			return avalancheTaggedEqualityHash(canonicalFloat32, uint64(bits))
		}}, true
	case reflect.Float64:
		return equalityCodec[V]{hash: func(value V) uint64 {
			bits := canonicalFloat64Bits(*(*float64)(unsafe.Pointer(&value)))
			return avalancheTaggedEqualityHash(canonicalFloat64, bits)
		}}, true
	default:
		return equalityCodec[V]{}, false
	}
}

// equalityPointerCodec hashes a value stored at ptr. Reflection is used only
// while compiling this function; published codecs contain fixed offsets and
// typed loads and therefore need neither reflect.Value nor allocation.
type equalityPointerCodec func(ptr unsafe.Pointer) uint64

func compileEqualityPointerCodec(typeOf reflect.Type) (equalityPointerCodec, string) {
	switch typeOf.Kind() {
	case reflect.Bool:
		return func(ptr unsafe.Pointer) uint64 {
			encoded := byte(0)
			if *(*bool)(ptr) {
				encoded = 1
			}
			return fnvHashByte(fnvHashByte(fnvOffset64, canonicalBool), encoded)
		}, ""
	case reflect.String:
		seed := maphash.MakeSeed()
		return func(ptr unsafe.Pointer) uint64 { return maphash.String(seed, *(*string)(ptr)) }, ""
	case reflect.Int:
		return pointerIntegerCodec[int](canonicalInt), ""
	case reflect.Int8:
		return pointerIntegerCodec[int8](canonicalInt8), ""
	case reflect.Int16:
		return pointerIntegerCodec[int16](canonicalInt16), ""
	case reflect.Int32:
		return pointerIntegerCodec[int32](canonicalInt32), ""
	case reflect.Int64:
		return pointerIntegerCodec[int64](canonicalInt64), ""
	case reflect.Uint:
		return pointerIntegerCodec[uint](canonicalUint), ""
	case reflect.Uint8:
		return pointerIntegerCodec[uint8](canonicalUint8), ""
	case reflect.Uint16:
		return pointerIntegerCodec[uint16](canonicalUint16), ""
	case reflect.Uint32:
		return pointerIntegerCodec[uint32](canonicalUint32), ""
	case reflect.Uint64:
		return pointerIntegerCodec[uint64](canonicalUint64), ""
	case reflect.Uintptr:
		return pointerIntegerCodec[uintptr](canonicalUintptr), ""
	case reflect.Float32:
		return func(ptr unsafe.Pointer) uint64 {
			return avalancheTaggedEqualityHash(canonicalFloat32, uint64(canonicalFloat32Bits(*(*float32)(ptr))))
		}, ""
	case reflect.Float64:
		return func(ptr unsafe.Pointer) uint64 {
			return avalancheTaggedEqualityHash(canonicalFloat64, canonicalFloat64Bits(*(*float64)(ptr)))
		}, ""
	case reflect.Complex64:
		return func(ptr unsafe.Pointer) uint64 {
			value := *(*complex64)(ptr)
			hash := fnvHashByte(fnvOffset64, 0xe0)
			hash = fnvHashUint64(hash, uint64(canonicalFloat32Bits(real(value))))
			return avalancheEqualityHash(fnvHashUint64(hash, uint64(canonicalFloat32Bits(imag(value)))))
		}, ""
	case reflect.Complex128:
		return func(ptr unsafe.Pointer) uint64 {
			value := *(*complex128)(ptr)
			hash := fnvHashByte(fnvOffset64, 0xe1)
			hash = fnvHashUint64(hash, canonicalFloat64Bits(real(value)))
			return avalancheEqualityHash(fnvHashUint64(hash, canonicalFloat64Bits(imag(value))))
		}, ""
	case reflect.Pointer, reflect.UnsafePointer, reflect.Chan:
		return func(ptr unsafe.Pointer) uint64 {
			return avalancheTaggedEqualityHash(0xe2, uint64(*(*uintptr)(ptr)))
		}, ""
	case reflect.Array:
		if typeOf.Elem().Kind() == reflect.Uint8 {
			length := typeOf.Len()
			return func(ptr unsafe.Pointer) uint64 {
				hash := fnvHashUint64(fnvHashByte(fnvOffset64, 0xf0), uint64(length))
				return avalancheEqualityHash(hashFixedBytes(hash, ptr, length))
			}, ""
		}
		child, reason := compileEqualityPointerCodec(typeOf.Elem())
		if child == nil {
			return nil, fmt.Sprintf("array element %s: %s", typeOf.Elem(), reason)
		}
		length, stride := typeOf.Len(), typeOf.Elem().Size()
		return func(ptr unsafe.Pointer) uint64 {
			hash := fnvHashUint64(fnvHashByte(fnvOffset64, 0xf1), uint64(length))
			for index := range length {
				hash = fnvHashUint64(hash, child(unsafe.Add(ptr, uintptr(index)*stride)))
			}
			return avalancheEqualityHash(hash)
		}, ""
	case reflect.Struct:
		children := make([]equalityPointerCodec, typeOf.NumField())
		offsets := make([]uintptr, typeOf.NumField())
		for index := range typeOf.NumField() {
			field := typeOf.Field(index)
			child, reason := compileEqualityPointerCodec(field.Type)
			if child == nil {
				return nil, fmt.Sprintf("field %s (%s): %s", field.Name, field.Type, reason)
			}
			children[index], offsets[index] = child, field.Offset
		}
		return func(ptr unsafe.Pointer) uint64 {
			hash := fnvHashUint64(fnvHashByte(fnvOffset64, 0xf2), uint64(len(children)))
			for index, child := range children {
				hash = fnvHashUint64(hash, child(unsafe.Add(ptr, offsets[index])))
			}
			return avalancheEqualityHash(hash)
		}, ""
	case reflect.Interface:
		return nil, "interfaces require dynamic-type inspection during search"
	default:
		return nil, fmt.Sprintf("unsupported underlying kind %s", typeOf.Kind())
	}
}

func hashFixedBytes(hash uint64, ptr unsafe.Pointer, length int) uint64 {
	bytes := unsafe.Slice((*byte)(ptr), length)
	// Common identifier/digest widths use unrolled eight-byte chunks. The
	// fallback remains bounded by the array length compiled during Build.
	switch length {
	case 8:
		return fnvHash8(hash, bytes)
	case 16:
		return fnvHash8(fnvHash8(hash, bytes[:8]), bytes[8:])
	case 20:
		hash = fnvHash8(fnvHash8(hash, bytes[:8]), bytes[8:16])
		bytes = bytes[16:]
	case 24:
		return fnvHash8(fnvHash8(fnvHash8(hash, bytes[:8]), bytes[8:16]), bytes[16:])
	case 32:
		return fnvHash8(fnvHash8(fnvHash8(fnvHash8(hash, bytes[:8]), bytes[8:16]), bytes[16:24]), bytes[24:])
	}
	for _, value := range bytes {
		hash = fnvHashByte(hash, value)
	}
	return hash
}

func fnvHash8(hash uint64, bytes []byte) uint64 {
	hash = fnvHashByte(hash, bytes[0])
	hash = fnvHashByte(hash, bytes[1])
	hash = fnvHashByte(hash, bytes[2])
	hash = fnvHashByte(hash, bytes[3])
	hash = fnvHashByte(hash, bytes[4])
	hash = fnvHashByte(hash, bytes[5])
	hash = fnvHashByte(hash, bytes[6])
	return fnvHashByte(hash, bytes[7])
}

func pointerIntegerCodec[I equalityInteger](tag byte) equalityPointerCodec {
	return func(ptr unsafe.Pointer) uint64 {
		return avalancheTaggedEqualityHash(tag, uint64(*(*I)(ptr)))
	}
}

type equalityInteger interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr
}

func integerEqualityCodec[V comparable, I equalityInteger](tag byte) equalityCodec[V] {
	return equalityCodec[V]{hash: func(value V) uint64 {
		return avalancheTaggedEqualityHash(tag, uint64(*(*I)(unsafe.Pointer(&value))))
	}}
}

package ruleix

import (
	"fmt"
	"hash/maphash"
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
// represented separately by the immutable shift stored on lossyEqualityRule.
type equalityCodec[V comparable] struct {
	hash func(V) uint64
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
	return equalityCodec[V]{}, &equalityCodecError{
		typeName: typeOf.String(),
		reason:   "no safe allocation-free semantic codec is available",
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
			return fnvHashTaggedUint64(canonicalFloat32, uint64(bits))
		}}, true
	case reflect.Float64:
		return equalityCodec[V]{hash: func(value V) uint64 {
			bits := canonicalFloat64Bits(*(*float64)(unsafe.Pointer(&value)))
			return fnvHashTaggedUint64(canonicalFloat64, bits)
		}}, true
	default:
		return equalityCodec[V]{}, false
	}
}

type equalityInteger interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr
}

func integerEqualityCodec[V comparable, I equalityInteger](tag byte) equalityCodec[V] {
	return equalityCodec[V]{hash: func(value V) uint64 {
		return fnvHashTaggedUint64(tag, uint64(*(*I)(unsafe.Pointer(&value))))
	}}
}

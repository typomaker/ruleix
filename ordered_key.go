package ruleix

import (
	"reflect"
	"unsafe"
)

// orderedKeyEncoder is selected while Lossy representations are compiled.
// Its published key function contains only a fixed typed load and the
// monotonic scalar transformation; search never performs reflection or a
// dynamic type switch.
type orderedKeyEncoder[V any] struct {
	key func(V) uint64
}

func compileOrderedKeyEncoder[V any]() (orderedKeyEncoder[V], bool) {
	typeOf := reflect.TypeOf((*V)(nil)).Elem()
	// Keep named scalar values on the existing comparator-backed path until a
	// production-shaped gate accepts changing their physical representation.
	if typeOf.PkgPath() != "" {
		return orderedKeyEncoder[V]{}, false
	}
	switch typeOf.Kind() {
	case reflect.Int:
		return signedOrderedKeyEncoder[V, int](64), true
	case reflect.Int8:
		return signedOrderedKeyEncoder[V, int8](8), true
	case reflect.Int16:
		return signedOrderedKeyEncoder[V, int16](16), true
	case reflect.Int32:
		return signedOrderedKeyEncoder[V, int32](32), true
	case reflect.Int64:
		return signedOrderedKeyEncoder[V, int64](64), true
	case reflect.Uint:
		return unsignedOrderedKeyEncoder[V, uint](64), true
	case reflect.Uint8:
		return unsignedOrderedKeyEncoder[V, uint8](8), true
	case reflect.Uint16:
		return unsignedOrderedKeyEncoder[V, uint16](16), true
	case reflect.Uint32:
		return unsignedOrderedKeyEncoder[V, uint32](32), true
	case reflect.Uint64:
		return unsignedOrderedKeyEncoder[V, uint64](64), true
	case reflect.Uintptr:
		return unsignedOrderedKeyEncoder[V, uintptr](64), true
	case reflect.Float32:
		return orderedKeyEncoder[V]{key: func(value V) uint64 {
			return unsignedOrderedKey(uint64(orderedFloat32Bits(*(*float32)(unsafe.Pointer(&value)))), 32)
		}}, true
	case reflect.Float64:
		return orderedKeyEncoder[V]{key: func(value V) uint64 {
			return orderedFloat64Bits(*(*float64)(unsafe.Pointer(&value)))
		}}, true
	default:
		return orderedKeyEncoder[V]{}, false
	}
}

type signedOrderedScalar interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64
}

func signedOrderedKeyEncoder[V any, S signedOrderedScalar](width uint) orderedKeyEncoder[V] {
	return orderedKeyEncoder[V]{key: func(value V) uint64 {
		return signedOrderedKey(int64(*(*S)(unsafe.Pointer(&value))), width)
	}}
}

type unsignedOrderedScalar interface {
	~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr
}

func unsignedOrderedKeyEncoder[V any, S unsignedOrderedScalar](width uint) orderedKeyEncoder[V] {
	return orderedKeyEncoder[V]{key: func(value V) uint64 {
		return unsignedOrderedKey(uint64(*(*S)(unsafe.Pointer(&value))), width)
	}}
}

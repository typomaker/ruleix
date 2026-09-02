package ruleix

import (
	"reflect"

	"github.com/RoaringBitmap/roaring/v2"
)

const lossyMaxBucketBits = 16

func bitmapBytes(bits *roaring.Bitmap) uint64 {
	if bits == nil {
		return 0
	}
	return bits.GetSerializedSizeInBytes()
}

func hashScalar(value any) (uint64, bool) {
	switch value := value.(type) {
	case bool:
		encoded := byte(0)
		if value {
			encoded = 1
		}
		return fnvHashByte(fnvHashByte(fnvOffset64, canonicalBool), encoded), true
	case string:
		return stableStringEqualityHash(value), true
	case int:
		return avalancheTaggedEqualityHash(canonicalInt, uint64(int64(value))), true
	case int8:
		return avalancheTaggedEqualityHash(canonicalInt8, uint64(value)), true
	case int16:
		return avalancheTaggedEqualityHash(canonicalInt16, uint64(value)), true
	case int32:
		return avalancheTaggedEqualityHash(canonicalInt32, uint64(value)), true
	case int64:
		return avalancheTaggedEqualityHash(canonicalInt64, uint64(value)), true
	case uint:
		return avalancheTaggedEqualityHash(canonicalUint, uint64(value)), true
	case uint8:
		return avalancheTaggedEqualityHash(canonicalUint8, uint64(value)), true
	case uint16:
		return avalancheTaggedEqualityHash(canonicalUint16, uint64(value)), true
	case uint32:
		return avalancheTaggedEqualityHash(canonicalUint32, uint64(value)), true
	case uint64:
		return avalancheTaggedEqualityHash(canonicalUint64, value), true
	case uintptr:
		return avalancheTaggedEqualityHash(canonicalUintptr, uint64(value)), true
	case float32:
		return avalancheTaggedEqualityHash(canonicalFloat32, uint64(canonicalFloat32Bits(value))), true
	case float64:
		return avalancheTaggedEqualityHash(canonicalFloat64, canonicalFloat64Bits(value)), true
	case [16]byte:
		hash := fnvHashByte(fnvOffset64, 0xf0)
		for _, item := range value {
			hash = fnvHashByte(hash, item)
		}
		return avalancheEqualityHash(hash), true
	case [2]string:
		hash := fnvHashByte(fnvOffset64, 0xf1)
		for _, item := range value {
			hash = fnvHashUint64(hash, uint64(len(item)))
			for i := range len(item) {
				hash = fnvHashByte(hash, item[i])
			}
		}
		return avalancheEqualityHash(hash), true
	default:
		return 0, false
	}
}

func comparableValueBytes(value any) uint64 {
	if encoded, ok := canonicalScalar(nil, value); ok {
		return uint64(len(encoded))
	}
	var size func(reflect.Value) uint64
	size = func(current reflect.Value) uint64 {
		if !current.IsValid() {
			return 0
		}
		switch current.Kind() {
		case reflect.String:
			return uint64(len(current.String())) + 9
		case reflect.Bool:
			return 2
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
			reflect.Uintptr, reflect.Float32, reflect.Float64,
			reflect.Chan, reflect.Pointer, reflect.UnsafePointer:
			return 9
		case reflect.Complex64, reflect.Complex128:
			return 17
		case reflect.Array:
			total := uint64(1)
			for i := range current.Len() {
				total += size(current.Index(i))
			}
			return total
		case reflect.Struct:
			total := uint64(1)
			for i := range current.NumField() {
				total += size(current.Field(i))
			}
			return total
		case reflect.Interface:
			if current.IsNil() {
				return 1
			}
			return 9 + uint64(len(current.Elem().Type().String())) + size(current.Elem())
		default:
			return 0
		}
	}
	return size(reflect.ValueOf(value))
}

const (
	fnvOffset64 = 14695981039346656037
	fnvPrime64  = 1099511628211
)

func fnvHashByte(hash uint64, value byte) uint64 {
	return (hash ^ uint64(value)) * fnvPrime64
}

func fnvHashUint64(hash, value uint64) uint64 {
	for shift := 56; shift >= 0; shift -= 8 {
		hash = fnvHashByte(hash, byte(value>>shift))
	}
	return hash
}

func fnvHashTaggedUint64(tag byte, value uint64) uint64 {
	return fnvHashUint64(fnvHashByte(fnvOffset64, tag), value)
}

func avalancheTaggedEqualityHash(tag byte, value uint64) uint64 {
	return avalancheEqualityHash(fnvHashTaggedUint64(tag, value))
}

func avalancheEqualityHash(hash uint64) uint64 {
	hash ^= hash >> 30
	hash *= 0xbf58476d1ce4e5b9
	hash ^= hash >> 27
	hash *= 0x94d049bb133111eb
	return hash ^ hash>>31
}

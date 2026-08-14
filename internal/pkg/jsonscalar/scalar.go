// Package jsonscalar identifies values that encoding/json can safely encode as a
// single JSON scalar (null, boolean, string, or finite number).
//
// It exists so audit / approval projections can copy "only scalars" from caller
// input without letting composite values (maps, slices, arrays, structs, pointers)
// smuggle nested secrets past a field allowlist, and without letting non-finite
// floats (NaN/±Inf) or invalid json.Number literals make the whole projection
// fail to marshal.
package jsonscalar

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
)

// IsScalar reports whether v can be JSON-encoded as exactly one JSON scalar
// without error.
//
// Accepted: nil; bool; string; every signed/unsigned integer kind; finite
// float32/float64; named types whose underlying kind is one of those; and a
// json.Number whose literal marshals (a valid JSON number). A named scalar that
// implements json.Marshaler is accepted only when its actual encoding is also a
// scalar. Rejected: NaN/±Inf floats (named or not), invalid json.Number literals,
// scalar marshalers that fail or emit an object/array, and every composite or
// non-scalar kind — map, slice, array, struct, pointer, channel, func, complex.
func IsScalar(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Bool,
		reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		// Continue below: named scalar types may implement json.Marshaler and emit
		// a composite value or an error despite their scalar underlying kind.
	case reflect.Float32, reflect.Float64:
		f := rv.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return false
		}
	default:
		// map/slice/array/struct/pointer/channel/func/complex and every other kind.
		return false
	}

	encoded, err := json.Marshal(v)
	if err != nil {
		return false
	}
	encoded = bytes.TrimSpace(encoded)
	if len(encoded) == 0 {
		return false
	}
	return encoded[0] != '{' && encoded[0] != '['
}

package jsonscalar

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

// named scalar aliases must be accepted exactly like their underlying kinds.
type testString string
type testBool bool
type testInt int
type testUint uint
type testFloat float64

type compositeMarshalingString string

func (compositeMarshalingString) MarshalJSON() ([]byte, error) {
	return []byte(`{"password":"nested"}`), nil
}

type errorMarshalingInt int

func (errorMarshalingInt) MarshalJSON() ([]byte, error) {
	return nil, errors.New("marshal failed")
}

type scalarMarshalingString string

func (scalarMarshalingString) MarshalJSON() ([]byte, error) {
	return []byte(`"safe"`), nil
}

func TestIsScalarAcceptsEveryJSONScalarKind(t *testing.T) {
	scalars := []any{
		nil,
		true,
		false,
		"plain",
		testString("named-string"),
		testBool(true),
		int(0), int8(-1), int16(2), int32(3), int64(4),
		testInt(5),
		uint(0), uint8(1), uint16(2), uint32(3), uint64(4), uintptr(5),
		testUint(6),
		float32(1.5),
		float64(2.5),
		testFloat(3.5),
		json.Number("7"),
		json.Number("1.5"),
		json.Number("-3"),
		json.Number("1e10"),
		json.Number("9007199254740993"),
	}
	for _, value := range scalars {
		t.Run(valueName(value), func(t *testing.T) {
			assert.True(t, IsScalar(value), "%T(%#v) must be a JSON scalar", value, value)
			_, err := json.Marshal(value)
			assert.NoError(t, err, "%T(%#v) must stay marshal-safe", value, value)
		})
	}
}

func TestIsScalarRejectsNonScalarKinds(t *testing.T) {
	pointer := 5
	typedNilPointer := (*int)(nil)
	channel := make(chan int)
	function := func() {}
	notScalars := []any{
		map[string]any{"a": 1},
		map[string]string{"a": "b"},
		[]string{"a"},
		[]any{"a"},
		[2]int{1, 2},
		struct{ A string }{A: "x"},
		&pointer,
		typedNilPointer,
		channel,
		function,
		complex64(1),
		complex128(1),
	}
	for _, value := range notScalars {
		t.Run(valueName(value), func(t *testing.T) {
			assert.False(t, IsScalar(value), "%T(%#v) must not be a JSON scalar", value, value)
		})
	}
}

func TestIsScalarHonorsCustomJSONEncoding(t *testing.T) {
	assert.True(t, IsScalar(scalarMarshalingString("ignored")), "a scalar kind that marshals to a scalar remains safe")
	assert.False(t, IsScalar(compositeMarshalingString("ignored")), "a scalar kind must not smuggle a composite through MarshalJSON")
	assert.False(t, IsScalar(errorMarshalingInt(1)), "a scalar kind whose MarshalJSON fails is not marshal-safe")
}

func TestIsScalarRejectsNonFiniteAndInvalidNumbers(t *testing.T) {
	type testFloatNaN float64
	notScalars := []any{
		math.NaN(),
		math.Inf(1),
		math.Inf(-1),
		testFloatNaN(math.NaN()),
		json.Number("abc"),
		json.Number("NaN"),
		json.Number("Inf"),
		json.Number("1e"),
		json.Number("+1"),
	}
	for _, value := range notScalars {
		t.Run(valueName(value), func(t *testing.T) {
			assert.False(t, IsScalar(value), "%T(%#v) must not be a JSON scalar", value, value)
			_, err := json.Marshal(value)
			assert.Error(t, err, "%T(%#v) is not marshal-safe", value, value)
		})
	}
}

// valueName 为每个测试值派生稳定的子测试名（含具体类型），避免同一类型不同值互相覆盖。
func valueName(v any) string {
	if v == nil {
		return "nil"
	}
	return reflect.TypeOf(v).String()
}

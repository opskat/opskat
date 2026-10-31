package extreg

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEndpointFieldsFromSchema(t *testing.T) {
	schema := map[string]any{"properties": map[string]any{
		"z":    map[string]any{"format": "endpoint"},
		"a":    map[string]any{"format": "endpoint"},
		"pass": map[string]any{"format": "password"},
		"name": map[string]any{"type": "string"},
	}}
	assert.Equal(t, []string{"a", "z"}, EndpointFieldsFromSchema(schema))
	assert.Empty(t, EndpointFieldsFromSchema(nil))
}

package opskat

import (
	"fmt"
	"reflect"
	"strings"
)

// JSON Schema generation from the Go types the handlers already use.
//
// The host renders tool parameters into the model-facing help document and parses
// `--flag=value` against them, so the schema has to describe exactly the struct the
// handler unmarshals into. Writing it a second time by hand is what let the two
// drift; reflecting it means a renamed field renames the flag.

type schemaMode int

const (
	// schemaModeParams describes tool arguments: what the exec flag DSL can express.
	schemaModeParams schemaMode = iota
	// schemaModeConfig describes an asset type's configuration form, which also
	// carries presentation tags and a stable field order.
	schemaModeConfig
)

// reflectSchema turns a struct type into a JSON Schema object.
// It panics on anything it cannot express — see Tool.
func reflectSchema(t reflect.Type, what string, mode schemaMode) map[string]any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		panic(fmt.Sprintf("opskat: %s must be a struct, got %s", what, t.Kind()))
	}

	props := map[string]any{}
	var order, required []string
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, optional := jsonFieldName(f)
		if name == "" {
			continue
		}
		fieldType := f.Type
		if fieldType.Kind() == reflect.Pointer {
			// A pointer field is how Go says "may be absent".
			fieldType = fieldType.Elem()
			optional = true
		}
		prop := propertySchema(fieldType, fmt.Sprintf("%s: field %s", what, f.Name))
		applyTags(prop, f, mode)
		props[name] = prop
		order = append(order, name)
		if !optional {
			required = append(required, name)
		}
	}

	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	if mode == schemaModeConfig && len(order) > 0 {
		// The form renders fields in declaration order rather than map order.
		schema["propertyOrder"] = order
	}
	return schema
}

// jsonFieldName resolves the field's wire name and whether it is optional.
// `json:"-"` returns an empty name, meaning "not part of the contract".
func jsonFieldName(f reflect.StructField) (name string, optional bool) {
	tag := f.Tag.Get("json")
	if tag == "-" {
		return "", false
	}
	parts := strings.Split(tag, ",")
	name = parts[0]
	if name == "" {
		name = f.Name
	}
	for _, opt := range parts[1:] {
		if opt == "omitempty" {
			optional = true
		}
	}
	return name, optional
}

func propertySchema(t reflect.Type, what string) map[string]any {
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice:
		if t.Elem().Kind() != reflect.String {
			panic(fmt.Sprintf("opskat: %s is []%s; only []string is supported", what, t.Elem().Kind()))
		}
		return map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	default:
		panic(fmt.Sprintf("opskat: %s has unsupported type %s — nested values go through the --json escape hatch, not a flag", what, t))
	}
}

// applyTags copies the declaration's presentation tags onto the property.
func applyTags(prop map[string]any, f reflect.StructField, mode schemaMode) {
	if v := f.Tag.Get("desc"); v != "" {
		prop["description"] = v
	}
	if mode != schemaModeConfig {
		return
	}
	if v := f.Tag.Get("title"); v != "" {
		prop["title"] = v
	}
	if v := f.Tag.Get("placeholder"); v != "" {
		prop["placeholder"] = v
	}
	if v := f.Tag.Get("format"); v != "" {
		prop["format"] = v
	}
	if v := f.Tag.Get("enum"); v != "" {
		prop["enum"] = strings.Split(v, ",")
	}
}

package extension

import "sort"

// PasswordFieldsFromSchema extracts property names that have "format": "password"
// from a JSON Schema configSchema.
func PasswordFieldsFromSchema(schema map[string]any) []string {
	if len(schema) == 0 {
		return nil
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		return nil
	}
	var fields []string
	for name, v := range props {
		prop, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if fmt, ok := prop["format"].(string); ok && fmt == "password" {
			fields = append(fields, name)
		}
	}
	sort.Strings(fields)
	return fields
}

// ConfigSchemaProperties returns the declared property names of a configSchema,
// sorted. Empty for a schema without a properties object.
func ConfigSchemaProperties(schema map[string]any) []string {
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ConfigSchemaRequired returns the property names listed in a configSchema's
// "required" array, sorted. Entries that are not declared properties are dropped:
// a required name nothing declares can never be supplied.
func ConfigSchemaRequired(schema map[string]any) []string {
	raw, ok := schema["required"].([]any)
	if !ok {
		return nil
	}
	props, _ := schema["properties"].(map[string]any)
	names := make([]string, 0, len(raw))
	for _, item := range raw {
		name, ok := item.(string)
		if !ok {
			continue
		}
		if _, declared := props[name]; !declared {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

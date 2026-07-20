package server_status_svc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

var jsonNumberPattern = regexp.MustCompile(`[-+]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][-+]?\d+)?`)

type indexedJSONObject struct {
	index  int
	object map[string]any
}

func decodeJSONPayload(payload []byte) (any, error) {
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 {
		return nil, fmt.Errorf("empty JSON payload")
	}

	start := -1
	for i, char := range payload {
		if char == '{' || char == '[' {
			start = i
			break
		}
	}
	if start < 0 {
		return nil, fmt.Errorf("JSON payload has no object or array")
	}
	endChar := byte('}')
	if payload[start] == '[' {
		endChar = ']'
	}
	end := bytes.LastIndexByte(payload, endChar)
	if end < start {
		return nil, fmt.Errorf("JSON payload is incomplete")
	}

	decoder := json.NewDecoder(bytes.NewReader(payload[start : end+1]))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func collectIndexedJSONObjects(root any, indexKeys []string, keyedPrefixes ...string) []indexedJSONObject {
	normalizedIndexKeys := normalizeJSONKeys(indexKeys)
	var objects []indexedJSONObject
	var walk func(any)
	walk = func(value any) {
		switch typed := value.(type) {
		case []any:
			for _, item := range typed {
				walk(item)
			}
		case map[string]any:
			if index, ok := directJSONIndex(typed, normalizedIndexKeys); ok {
				objects = append(objects, indexedJSONObject{index: index, object: typed})
				return
			}

			keys := sortedJSONMapKeys(typed)
			for _, key := range keys {
				child := typed[key]
				if index, ok := indexFromKey(key, keyedPrefixes); ok {
					if object, objectOK := child.(map[string]any); objectOK {
						objects = append(objects, indexedJSONObject{index: index, object: object})
						continue
					}
				}
				walk(child)
			}
		}
	}
	walk(root)
	return objects
}

func directJSONIndex(object map[string]any, normalizedKeys []string) (int, bool) {
	for _, wanted := range normalizedKeys {
		for key, value := range object {
			if normalizeJSONKey(key) != wanted {
				continue
			}
			number, ok := jsonFloat(value)
			if !ok || number < 0 || math.Trunc(number) != number || number > math.MaxInt {
				return 0, false
			}
			return int(number), true
		}
	}
	return 0, false
}

func indexFromKey(key string, prefixes []string) (int, bool) {
	normalized := normalizeJSONKey(key)
	for _, prefix := range prefixes {
		prefix = normalizeJSONKey(prefix)
		if !strings.HasPrefix(normalized, prefix) {
			continue
		}
		suffix := strings.TrimPrefix(normalized, prefix)
		if suffix == "" {
			continue
		}
		index, err := strconv.Atoi(suffix)
		if err == nil && index >= 0 {
			return index, true
		}
	}
	return 0, false
}

func lookupJSONValue(root any, keys ...string) (any, string, bool) {
	for _, wanted := range normalizeJSONKeys(keys) {
		if value, key, ok := findJSONValue(root, wanted); ok {
			return value, key, true
		}
	}
	return nil, "", false
}

func lookupDirectJSONValue(object map[string]any, keys ...string) (any, string, bool) {
	for _, wanted := range normalizeJSONKeys(keys) {
		for _, key := range sortedJSONMapKeys(object) {
			if normalizeJSONKey(key) == wanted {
				return object[key], key, true
			}
		}
	}
	return nil, "", false
}

func findJSONValue(root any, wanted string) (any, string, bool) {
	switch typed := root.(type) {
	case map[string]any:
		keys := sortedJSONMapKeys(typed)
		for _, key := range keys {
			if normalizeJSONKey(key) == wanted {
				return typed[key], key, true
			}
		}
		for _, key := range keys {
			if value, matchedKey, ok := findJSONValue(typed[key], wanted); ok {
				return value, matchedKey, true
			}
		}
	case []any:
		for _, item := range typed {
			if value, matchedKey, ok := findJSONValue(item, wanted); ok {
				return value, matchedKey, true
			}
		}
	}
	return nil, "", false
}

func jsonStringByKeys(root any, keys ...string) string {
	value, _, ok := lookupJSONValue(root, keys...)
	if !ok {
		return ""
	}
	return jsonString(value)
}

func jsonString(value any) string {
	switch typed := value.(type) {
	case string:
		return optionalString(typed)
	case json.Number:
		return optionalString(typed.String())
	case float64:
		return optionalString(strconv.FormatFloat(typed, 'f', -1, 64))
	default:
		return ""
	}
}

func jsonFloatByKeys(root any, keys ...string) *float64 {
	value, _, ok := lookupJSONValue(root, keys...)
	if !ok {
		return nil
	}
	number, ok := jsonFloat(value)
	if !ok {
		return nil
	}
	return &number
}

func jsonFloat(value any) (float64, bool) {
	var number float64
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		number = parsed
	case float64:
		number = typed
	case float32:
		number = float64(typed)
	case int:
		number = float64(typed)
	case int64:
		number = float64(typed)
	case string:
		if optionalString(typed) == "" {
			return 0, false
		}
		match := jsonNumberPattern.FindString(strings.ReplaceAll(typed, ",", ""))
		if match == "" {
			return 0, false
		}
		parsed, err := strconv.ParseFloat(match, 64)
		if err != nil {
			return 0, false
		}
		number = parsed
	default:
		return 0, false
	}
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, false
	}
	return number, true
}

func jsonBytesByKeys(root any, defaultUnit string, keys ...string) *int64 {
	value, matchedKey, ok := lookupJSONValue(root, keys...)
	if !ok {
		return nil
	}
	number, ok := jsonFloat(value)
	if !ok || number < 0 {
		return nil
	}
	unit := jsonMemoryUnit(value, matchedKey, defaultUnit)
	multiplier := float64(1)
	switch unit {
	case "kb":
		multiplier = 1000
	case "kib":
		multiplier = 1024
	case "mb":
		multiplier = 1000 * 1000
	case "mib":
		multiplier = 1024 * 1024
	case "gb":
		multiplier = 1000 * 1000 * 1000
	case "gib":
		multiplier = 1024 * 1024 * 1024
	}
	bytesValue := number * multiplier
	if bytesValue > math.MaxInt64 {
		return nil
	}
	result := int64(math.Round(bytesValue))
	return &result
}

func jsonMemoryUnit(value any, key, defaultUnit string) string {
	combined := strings.ToLower(key)
	if text, ok := value.(string); ok {
		combined += " " + strings.ToLower(text)
	}
	for _, unit := range []string{"gib", "gb", "mib", "mb", "kib", "kb"} {
		if strings.Contains(combined, unit) {
			return unit
		}
	}
	return strings.ToLower(defaultUnit)
}

func jsonCollectionCountByKeys(root any, keys ...string) *int {
	value, _, ok := lookupJSONValue(root, keys...)
	if !ok {
		return nil
	}
	count, ok := jsonCollectionCount(value)
	if !ok {
		return nil
	}
	return &count
}

func jsonCollectionCount(value any) (int, bool) {
	switch typed := value.(type) {
	case []any:
		return len(typed), true
	case map[string]any:
		if _, _, ok := lookupDirectJSONValue(typed, "pid", "process_id"); ok {
			return 1, true
		}
		return len(typed), true
	case string:
		normalized := strings.ToLower(strings.TrimSpace(typed))
		if normalized == "" || strings.Contains(normalized, "no pid") || strings.Contains(normalized, "no process") {
			return 0, true
		}
		parts := strings.FieldsFunc(normalized, func(r rune) bool {
			return r == ',' || unicode.IsSpace(r)
		})
		count := 0
		for _, part := range parts {
			if _, err := strconv.Atoi(part); err == nil {
				count++
			}
		}
		return count, count > 0
	default:
		if _, ok := jsonFloat(value); ok {
			return 1, true
		}
		return 0, false
	}
}

func normalizeJSONKeys(keys []string) []string {
	normalized := make([]string, 0, len(keys))
	for _, key := range keys {
		normalized = append(normalized, normalizeJSONKey(key))
	}
	return normalized
}

func normalizeJSONKey(key string) string {
	var builder strings.Builder
	for _, char := range strings.ToLower(key) {
		if char == '%' {
			builder.WriteString("percent")
		} else if unicode.IsLetter(char) || unicode.IsDigit(char) {
			builder.WriteRune(char)
		}
	}
	return builder.String()
}

func sortedJSONMapKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func mergeGPUFields(target *GPU, source GPU) {
	if target.DeviceID == "" {
		target.DeviceID = source.DeviceID
	}
	if target.PCIBusID == "" {
		target.PCIBusID = source.PCIBusID
	}
	if target.Vendor == "" {
		target.Vendor = source.Vendor
	}
	if target.Name == "" {
		target.Name = source.Name
	}
	if target.Driver == "" {
		target.Driver = source.Driver
	}
	if target.DriverVersion == "" {
		target.DriverVersion = source.DriverVersion
	}
	if target.Runtime == "" {
		target.Runtime = source.Runtime
	}
	if target.RuntimeVersion == "" {
		target.RuntimeVersion = source.RuntimeVersion
	}
	if target.UtilizationPercent == nil {
		target.UtilizationPercent = source.UtilizationPercent
	}
	if target.MemoryUsedBytes == nil {
		target.MemoryUsedBytes = source.MemoryUsedBytes
	}
	if target.MemoryTotalBytes == nil {
		target.MemoryTotalBytes = source.MemoryTotalBytes
	}
	if target.TemperatureC == nil {
		target.TemperatureC = source.TemperatureC
	}
	if target.PowerDrawWatts == nil {
		target.PowerDrawWatts = source.PowerDrawWatts
	}
	if target.PowerLimitWatts == nil {
		target.PowerLimitWatts = source.PowerLimitWatts
	}
	if target.FanPercent == nil {
		target.FanPercent = source.FanPercent
	}
	if target.ComputeProcessCount == nil {
		target.ComputeProcessCount = source.ComputeProcessCount
	}
}

package server_status_svc

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	amdSMIStaticBegin  = "__OPSKAT_AMD_SMI_STATIC_BEGIN__"
	amdSMIStaticEnd    = "__OPSKAT_AMD_SMI_STATIC_END__"
	amdSMIMetricBegin  = "__OPSKAT_AMD_SMI_METRIC_BEGIN__"
	amdSMIMetricEnd    = "__OPSKAT_AMD_SMI_METRIC_END__"
	amdSMIProcessBegin = "__OPSKAT_AMD_SMI_PROCESS_BEGIN__"
	amdSMIProcessEnd   = "__OPSKAT_AMD_SMI_PROCESS_END__"
	amdSMIVersionBegin = "__OPSKAT_AMD_SMI_VERSION_BEGIN__"
	amdSMIVersionEnd   = "__OPSKAT_AMD_SMI_VERSION_END__"

	rocmSMICoreBegin    = "__OPSKAT_ROCM_SMI_CORE_BEGIN__"
	rocmSMICoreEnd      = "__OPSKAT_ROCM_SMI_CORE_END__"
	rocmSMIProcessBegin = "__OPSKAT_ROCM_SMI_PROCESS_BEGIN__"
	rocmSMIProcessEnd   = "__OPSKAT_ROCM_SMI_PROCESS_END__"
)

// amd-smi is AMD's current management CLI. Each JSON subcommand is isolated so
// an optional process/version failure does not invalidate usable device metrics.
const amdSMICommand = `sh <<'OPSKAT_AMD_SMI'
if ! command -v amd-smi >/dev/null 2>&1; then
  exit 0
fi

printf '__OPSKAT_AMD_SMI_STATIC_BEGIN__\n'
LC_ALL=C amd-smi static --json 2>/dev/null || true
printf '__OPSKAT_AMD_SMI_STATIC_END__\n'

printf '__OPSKAT_AMD_SMI_METRIC_BEGIN__\n'
LC_ALL=C amd-smi metric --json 2>/dev/null || true
printf '__OPSKAT_AMD_SMI_METRIC_END__\n'

printf '__OPSKAT_AMD_SMI_PROCESS_BEGIN__\n'
LC_ALL=C amd-smi process --json 2>/dev/null || true
printf '__OPSKAT_AMD_SMI_PROCESS_END__\n'

printf '__OPSKAT_AMD_SMI_VERSION_BEGIN__\n'
LC_ALL=C amd-smi version --json 2>/dev/null || true
printf '__OPSKAT_AMD_SMI_VERSION_END__\n'
OPSKAT_AMD_SMI`

// rocm-smi remains the compatibility fallback for ROCm installations that do
// not ship a usable amd-smi. Core metrics and the permission-sensitive process
// query are separate JSON sections.
const rocmSMICommand = `sh <<'OPSKAT_ROCM_SMI'
if ! command -v rocm-smi >/dev/null 2>&1; then
  exit 0
fi

printf '__OPSKAT_ROCM_SMI_CORE_BEGIN__\n'
LC_ALL=C rocm-smi --showuniqueid --showproductname --showuse --showmeminfo vram --showtemp --showpower --showmaxpower --showfan --showdriverversion --json 2>/dev/null || true
printf '__OPSKAT_ROCM_SMI_CORE_END__\n'

printf '__OPSKAT_ROCM_SMI_PROCESS_BEGIN__\n'
LC_ALL=C rocm-smi --showpids --json 2>/dev/null || true
printf '__OPSKAT_ROCM_SMI_PROCESS_END__\n'
OPSKAT_ROCM_SMI`

type amdCollector struct{}

func init() {
	registerGPUCollector(amdCollector{})
}

func (amdCollector) Name() string  { return "amd-smi/rocm-smi" }
func (amdCollector) Priority() int { return 200 }
func (amdCollector) Collect(ctx context.Context, run statusCommandRunner) (*gpuCollectorResult, error) {
	var amdResult *gpuCollectorResult
	var amdErr error

	raw, err := run(ctx, amdSMICommand, "collect AMD GPU status with amd-smi failed")
	if err != nil {
		amdErr = err
	} else if strings.TrimSpace(raw) != "" {
		amdResult, amdErr = parseAMDSMIOutput(raw)
		if amdErr == nil && len(amdResult.GPUs) > 0 && hasGPUTelemetry(amdResult.GPUs) {
			return amdResult, nil
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	raw, err = run(ctx, rocmSMICommand, "collect AMD GPU status with rocm-smi failed")
	if err == nil && strings.TrimSpace(raw) != "" {
		rocmResult, parseErr := parseROCMSMIOutput(raw)
		if parseErr == nil && len(rocmResult.GPUs) > 0 {
			return rocmResult, nil
		}
		err = parseErr
	}
	if amdResult != nil && len(amdResult.GPUs) > 0 {
		return amdResult, nil
	}
	if amdErr != nil || err != nil {
		return nil, errors.Join(amdErr, err)
	}
	return &gpuCollectorResult{}, nil
}

func parseAMDSMIOutput(raw string) (*gpuCollectorResult, error) {
	staticJSON, err := extractMarkedOutput(raw, amdSMIStaticBegin, amdSMIStaticEnd)
	if err != nil {
		return nil, err
	}
	metricJSON, err := extractMarkedOutput(raw, amdSMIMetricBegin, amdSMIMetricEnd)
	if err != nil {
		return nil, err
	}
	processJSON, err := extractMarkedOutput(raw, amdSMIProcessBegin, amdSMIProcessEnd)
	if err != nil {
		return nil, err
	}
	versionJSON, err := extractMarkedOutput(raw, amdSMIVersionBegin, amdSMIVersionEnd)
	if err != nil {
		return nil, err
	}
	return parseAMDSMIJSON(staticJSON, metricJSON, processJSON, versionJSON)
}

func parseAMDSMIJSON(staticJSON, metricJSON, processJSON, versionJSON []byte) (*gpuCollectorResult, error) {
	devices := make(map[int]*GPU)
	var parseErrors []error

	if len(staticJSON) > 0 {
		root, err := decodeJSONPayload(staticJSON)
		if err != nil {
			parseErrors = append(parseErrors, fmt.Errorf("parse amd-smi static JSON: %w", err))
		} else {
			for _, indexed := range collectIndexedJSONObjects(root, []string{"gpu", "gpu_id"}, "gpu", "card") {
				mergeAMDDevice(devices, parseAMDStaticDevice(indexed.index, indexed.object))
			}
		}
	}

	if len(metricJSON) > 0 {
		root, err := decodeJSONPayload(metricJSON)
		if err != nil {
			parseErrors = append(parseErrors, fmt.Errorf("parse amd-smi metric JSON: %w", err))
		} else {
			for _, indexed := range collectIndexedJSONObjects(root, []string{"gpu", "gpu_id"}, "gpu", "card") {
				mergeAMDDevice(devices, parseAMDMetricDevice(indexed.index, indexed.object))
			}
		}
	}

	if len(devices) == 0 {
		if len(parseErrors) > 0 {
			return nil, errors.Join(parseErrors...)
		}
		return &gpuCollectorResult{}, nil
	}

	if len(processJSON) > 0 {
		if root, err := decodeJSONPayload(processJSON); err == nil {
			for _, indexed := range collectIndexedJSONObjects(root, []string{"gpu", "gpu_id"}, "gpu", "card") {
				if count := jsonCollectionCountByKeys(indexed.object, "process_list", "process_info", "processes", "pids"); count != nil {
					if gpu := devices[indexed.index]; gpu != nil {
						gpu.ComputeProcessCount = count
					}
				}
			}
		}
	}

	rocmVersion := ""
	if len(versionJSON) > 0 {
		if root, err := decodeJSONPayload(versionJSON); err == nil {
			rocmVersion = jsonStringByKeys(root, "rocm_version", "ROCM version")
		}
	}

	result := &gpuCollectorResult{GPUs: sortedAMDDevices(devices)}
	for i := range result.GPUs {
		if rocmVersion != "" {
			result.GPUs[i].Runtime = "ROCm"
			result.GPUs[i].RuntimeVersion = rocmVersion
		}
	}
	return result, nil
}

func parseAMDStaticDevice(index int, object map[string]any) GPU {
	gpu := GPU{
		Index:         index,
		DeviceID:      jsonStringByKeys(object, "uuid", "unique_id", "serial_number"),
		PCIBusID:      jsonStringByKeys(object, "bdf", "pci_bdf", "pci_bus_id", "pci_bdf_address"),
		Vendor:        "AMD",
		Name:          jsonStringByKeys(object, "market_name", "product_name", "card_series", "device_name"),
		DriverVersion: jsonStringByKeys(object, "driver_version"),
	}
	if driverValue, _, ok := lookupJSONValue(object, "driver"); ok {
		if driverObject, objectOK := driverValue.(map[string]any); objectOK && gpu.DriverVersion == "" {
			gpu.DriverVersion = jsonStringByKeys(driverObject, "version")
		}
	}
	if vramValue, _, ok := lookupJSONValue(object, "vram"); ok {
		gpu.MemoryTotalBytes = jsonBytesByKeys(vramValue, "b", "total_vram", "vram_total", "size")
	}
	if gpu.MemoryTotalBytes == nil {
		gpu.MemoryTotalBytes = jsonBytesByKeys(object, "b", "total_vram", "vram_total", "vram_size")
	}
	return gpu
}

func parseAMDMetricDevice(index int, object map[string]any) GPU {
	return GPU{
		Index:              index,
		Vendor:             "AMD",
		UtilizationPercent: jsonFloatByKeys(object, "gfx_activity", "gpu_utilization", "gpu_use", "gpu_activity"),
		MemoryUsedBytes:    jsonBytesByKeys(object, "b", "used_vram", "vram_used", "vram_total_used_memory_b"),
		MemoryTotalBytes:   jsonBytesByKeys(object, "b", "total_vram", "vram_total", "vram_total_memory_b"),
		TemperatureC:       jsonFloatByKeys(object, "hotspot_temperature", "temperature_hotspot", "hotspot", "edge_temperature", "gpu_temperature"),
		PowerDrawWatts:     jsonFloatByKeys(object, "socket_power", "current_socket_power", "average_graphics_package_power_w", "power_draw"),
		PowerLimitWatts:    jsonFloatByKeys(object, "power_limit", "max_socket_power", "max_graphics_package_power_w"),
		FanPercent:         jsonFloatByKeys(object, "fan_percent", "fan_speed"),
	}
}

func parseROCMSMIOutput(raw string) (*gpuCollectorResult, error) {
	coreJSON, err := extractMarkedOutput(raw, rocmSMICoreBegin, rocmSMICoreEnd)
	if err != nil {
		return nil, err
	}
	processJSON, err := extractMarkedOutput(raw, rocmSMIProcessBegin, rocmSMIProcessEnd)
	if err != nil {
		return nil, err
	}
	return parseROCMSMIJSON(coreJSON, processJSON)
}

func parseROCMSMIJSON(coreJSON, processJSON []byte) (*gpuCollectorResult, error) {
	root, err := decodeJSONPayload(coreJSON)
	if err != nil {
		return nil, fmt.Errorf("parse rocm-smi JSON: %w", err)
	}
	devices := make(map[int]*GPU)
	driverVersion := jsonStringByKeys(root, "driver_version")
	for _, indexed := range collectIndexedJSONObjects(root, nil, "card", "gpu") {
		gpu := GPU{
			Index:               indexed.index,
			DeviceID:            jsonStringByKeys(indexed.object, "unique_id", "uuid", "serial_number"),
			PCIBusID:            jsonStringByKeys(indexed.object, "pci_bus", "pci_bus_id", "bdf"),
			Vendor:              "AMD",
			Name:                jsonStringByKeys(indexed.object, "card_series", "product_name", "device_name", "card_model"),
			DriverVersion:       firstNonEmpty(jsonStringByKeys(indexed.object, "driver_version"), driverVersion),
			UtilizationPercent:  jsonFloatByKeys(indexed.object, "gpu_use", "gpu_use_percent", "gpu_utilization"),
			MemoryUsedBytes:     jsonBytesByKeys(indexed.object, "b", "vram_total_used_memory_b", "vram_used", "used_vram"),
			MemoryTotalBytes:    jsonBytesByKeys(indexed.object, "b", "vram_total_memory_b", "vram_total", "total_vram"),
			TemperatureC:        jsonFloatByKeys(indexed.object, "temperature_sensor_junction_c", "temperature_sensor_edge_c", "temperature_hotspot_c", "temperature"),
			PowerDrawWatts:      jsonFloatByKeys(indexed.object, "average_graphics_package_power_w", "current_socket_power_w", "power_draw_w"),
			PowerLimitWatts:     jsonFloatByKeys(indexed.object, "max_graphics_package_power_w", "power_cap_w", "power_limit_w"),
			FanPercent:          jsonFloatByKeys(indexed.object, "fan_speed_percent", "fan_percent", "fan_speed"),
			ComputeProcessCount: nil,
		}
		mergeAMDDevice(devices, gpu)
	}
	if len(devices) == 0 {
		return &gpuCollectorResult{}, nil
	}

	if len(processJSON) > 0 {
		if processRoot, processErr := decodeJSONPayload(processJSON); processErr == nil {
			for _, indexed := range collectIndexedJSONObjects(processRoot, nil, "card", "gpu") {
				if count := jsonCollectionCountByKeys(indexed.object, "process_list", "process_info", "processes", "kfd_pids", "pids"); count != nil {
					if gpu := devices[indexed.index]; gpu != nil {
						gpu.ComputeProcessCount = count
					}
				}
			}
		}
	}
	return &gpuCollectorResult{GPUs: sortedAMDDevices(devices)}, nil
}

func mergeAMDDevice(devices map[int]*GPU, source GPU) {
	target := devices[source.Index]
	if target == nil {
		deviceCopy := source
		devices[source.Index] = &deviceCopy
		return
	}
	mergeGPUFields(target, source)
}

func sortedAMDDevices(devices map[int]*GPU) []GPU {
	indexes := make([]int, 0, len(devices))
	for index := range devices {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	result := make([]GPU, 0, len(indexes))
	for _, index := range indexes {
		result = append(result, *devices[index])
	}
	return result
}

func hasGPUTelemetry(gpus []GPU) bool {
	for _, gpu := range gpus {
		if gpu.UtilizationPercent != nil || gpu.MemoryUsedBytes != nil || gpu.TemperatureC != nil || gpu.PowerDrawWatts != nil {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

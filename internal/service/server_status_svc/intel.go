package server_status_svc

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

const (
	xpuSMIDiscoveryBegin = "__OPSKAT_XPU_SMI_DISCOVERY_BEGIN__"
	xpuSMIDiscoveryEnd   = "__OPSKAT_XPU_SMI_DISCOVERY_END__"
	xpuSMIStatsBegin     = "__OPSKAT_XPU_SMI_STATS_BEGIN__"
	xpuSMIStatsEnd       = "__OPSKAT_XPU_SMI_STATS_END__"
)

// xpu-smi discovery/stats JSON is the supported Intel path. intel_gpu_top is
// intentionally excluded because its normal mode is a long-running stream.
const xpuSMICommand = `sh <<'OPSKAT_XPU_SMI'
if ! command -v xpu-smi >/dev/null 2>&1; then
  exit 0
fi

printf '__OPSKAT_XPU_SMI_DISCOVERY_BEGIN__\n'
LC_ALL=C xpu-smi discovery -j 2>/dev/null || true
printf '__OPSKAT_XPU_SMI_DISCOVERY_END__\n'

printf '__OPSKAT_XPU_SMI_STATS_BEGIN__\n'
LC_ALL=C xpu-smi stats -j 2>/dev/null || true
printf '__OPSKAT_XPU_SMI_STATS_END__\n'
OPSKAT_XPU_SMI`

type intelCollector struct{}

func init() {
	registerGPUCollector(intelCollector{})
}

func (intelCollector) Name() string  { return "xpu-smi" }
func (intelCollector) Priority() int { return 300 }
func (intelCollector) Collect(ctx context.Context, run statusCommandRunner) (*gpuCollectorResult, error) {
	raw, err := run(ctx, xpuSMICommand, "collect Intel GPU status with xpu-smi failed")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(raw) == "" {
		return &gpuCollectorResult{}, nil
	}
	return parseXPUSMIOutput(raw)
}

func parseXPUSMIOutput(raw string) (*gpuCollectorResult, error) {
	discoveryJSON, err := extractMarkedOutput(raw, xpuSMIDiscoveryBegin, xpuSMIDiscoveryEnd)
	if err != nil {
		return nil, err
	}
	statsJSON, err := extractMarkedOutput(raw, xpuSMIStatsBegin, xpuSMIStatsEnd)
	if err != nil {
		return nil, err
	}
	return parseXPUSMIJSON(discoveryJSON, statsJSON)
}

func parseXPUSMIJSON(discoveryJSON, statsJSON []byte) (*gpuCollectorResult, error) {
	devices := make(map[int]*GPU)
	var discoveryErr error
	if len(discoveryJSON) > 0 {
		root, err := decodeJSONPayload(discoveryJSON)
		if err != nil {
			discoveryErr = fmt.Errorf("parse xpu-smi discovery JSON: %w", err)
		} else {
			for _, indexed := range collectIndexedJSONObjects(root, []string{"device_id"}, "device", "gpu") {
				deviceType := strings.ToLower(jsonStringByKeys(indexed.object, "device_type"))
				if deviceType != "" && !strings.Contains(deviceType, "gpu") {
					continue
				}
				mergeIntelDevice(devices, GPU{
					Index:         indexed.index,
					DeviceID:      jsonStringByKeys(indexed.object, "uuid", "serial_number"),
					PCIBusID:      jsonStringByKeys(indexed.object, "pci_bdf_address", "pci_bdf", "bdf", "pci_bus_id"),
					Vendor:        "Intel",
					Name:          jsonStringByKeys(indexed.object, "device_name", "product_name", "name"),
					DriverVersion: jsonStringByKeys(indexed.object, "driver_version"),
					MemoryTotalBytes: jsonBytesByKeys(indexed.object, "b",
						"memory_physical_size_byte", "memory_size_bytes", "memory_size"),
				})
			}
		}
	}

	var statsErr error
	if len(statsJSON) > 0 {
		root, err := decodeJSONPayload(statsJSON)
		if err != nil {
			statsErr = fmt.Errorf("parse xpu-smi stats JSON: %w", err)
		} else {
			for _, indexed := range collectIndexedJSONObjects(root, []string{"device_id"}, "device", "gpu") {
				gpu := GPU{Index: indexed.index, Vendor: "Intel"}
				applyXPUStats(&gpu, indexed.object)
				mergeIntelDevice(devices, gpu)
			}
		}
	}

	if len(devices) == 0 {
		if discoveryErr != nil || statsErr != nil {
			return nil, fmt.Errorf("xpu-smi JSON unusable: %w", errors.Join(discoveryErr, statsErr))
		}
		return &gpuCollectorResult{}, nil
	}

	indexes := make([]int, 0, len(devices))
	for index := range devices {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	result := &gpuCollectorResult{GPUs: make([]GPU, 0, len(indexes))}
	for _, index := range indexes {
		result.GPUs = append(result.GPUs, *devices[index])
	}
	return result, nil
}

func applyXPUStats(gpu *GPU, object map[string]any) {
	if metricsValue, _, ok := lookupJSONValue(object, "device_level", "metrics"); ok {
		if metrics, arrayOK := metricsValue.([]any); arrayOK {
			for _, item := range metrics {
				record, recordOK := item.(map[string]any)
				if !recordOK {
					continue
				}
				metricType := normalizeJSONKey(jsonStringByKeys(record, "metrics_type", "metric_type", "name"))
				value, ok := xpuMetricValue(record)
				if !ok {
					continue
				}
				switch metricType {
				case "xpumstatsgpuutilization", "gpuutilization":
					gpu.UtilizationPercent = floatPointer(value)
				case "xpumstatsmemoryused", "memoryused", "gpumemoryused":
					gpu.MemoryUsedBytes = int64Pointer(value)
				case "xpumstatsmemorytotal", "memorytotal", "gpumemorytotal":
					gpu.MemoryTotalBytes = int64Pointer(value)
				case "xpumstatsgpucoretemperature", "gpucoretemperature", "gputemperature":
					gpu.TemperatureC = floatPointer(value)
				case "xpumstatspower", "power":
					gpu.PowerDrawWatts = floatPointer(value)
				}
			}
		}
	}

	if gpu.UtilizationPercent == nil {
		gpu.UtilizationPercent = jsonFloatByKeys(object, "gpu_utilization", "gpu_utilization_percent")
	}
	if gpu.MemoryUsedBytes == nil {
		gpu.MemoryUsedBytes = jsonBytesByKeys(object, "b", "gpu_memory_used", "memory_used")
	}
	if gpu.MemoryTotalBytes == nil {
		gpu.MemoryTotalBytes = jsonBytesByKeys(object, "b", "gpu_memory_total", "memory_total")
	}
	if gpu.TemperatureC == nil {
		gpu.TemperatureC = jsonFloatByKeys(object, "gpu_core_temperature", "gpu_temperature")
	}
	if gpu.PowerDrawWatts == nil {
		gpu.PowerDrawWatts = jsonFloatByKeys(object, "power", "power_draw")
	}
}

func xpuMetricValue(record map[string]any) (float64, bool) {
	valueJSON, _, ok := lookupDirectJSONValue(record, "value", "avg")
	if !ok {
		return 0, false
	}
	value, ok := jsonFloat(valueJSON)
	if !ok || value < 0 {
		return 0, false
	}
	if scaleJSON, _, scaleOK := lookupDirectJSONValue(record, "scale"); scaleOK {
		if scale, numberOK := jsonFloat(scaleJSON); numberOK && scale > 1 {
			value /= scale
		}
	}
	return value, true
}

func mergeIntelDevice(devices map[int]*GPU, source GPU) {
	target := devices[source.Index]
	if target == nil {
		copy := source
		devices[source.Index] = &copy
		return
	}
	mergeGPUFields(target, source)
}

func floatPointer(value float64) *float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	return &value
}

func int64Pointer(value float64) *int64 {
	if value < 0 || value > math.MaxInt64 || math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	result := int64(math.Round(value))
	return &result
}

package server_status_svc

import (
	"context"
	"encoding/csv"
	"fmt"
	"math"
	"strconv"
	"strings"
)

const (
	nvidiaGPUBegin           = "__OPSKAT_NVIDIA_GPU_BEGIN__"
	nvidiaGPUEnd             = "__OPSKAT_NVIDIA_GPU_END__"
	nvidiaCUDAKey            = "__OPSKAT_NVIDIA_CUDA_VERSION__="
	nvidiaProcessBegin       = "__OPSKAT_NVIDIA_PROCESS_BEGIN__"
	nvidiaProcessEnd         = "__OPSKAT_NVIDIA_PROCESS_END__"
	nvidiaProcessUnavailable = "__OPSKAT_NVIDIA_PROCESS_UNAVAILABLE__"
)

const nvidiaSMICommand = `sh <<'OPSKAT_NVIDIA'
if ! command -v nvidia-smi >/dev/null 2>&1; then
  exit 0
fi

GPU_OUTPUT=$(LC_ALL=C nvidia-smi --query-gpu=index,uuid,name,utilization.gpu,memory.used,memory.total,temperature.gpu,power.draw,power.limit,fan.speed,driver_version,pci.bus_id --format=csv,noheader,nounits 2>/dev/null) || exit 1
printf '__OPSKAT_NVIDIA_GPU_BEGIN__\n'
if [ -n "$GPU_OUTPUT" ]; then
  printf '%s\n' "$GPU_OUTPUT"
fi
printf '__OPSKAT_NVIDIA_GPU_END__\n'

CUDA_VERSION=$(LC_ALL=C nvidia-smi 2>/dev/null | sed -n 's/.*CUDA Version: \([^ ]*\).*/\1/p' | head -n 1)
printf '__OPSKAT_NVIDIA_CUDA_VERSION__=%s\n' "$CUDA_VERSION"

PROCESS_OUTPUT=$(LC_ALL=C nvidia-smi --query-compute-apps=gpu_uuid,pid --format=csv,noheader,nounits 2>/dev/null)
PROCESS_STATUS=$?
printf '__OPSKAT_NVIDIA_PROCESS_BEGIN__\n'
if [ "$PROCESS_STATUS" -eq 0 ]; then
  if [ -n "$PROCESS_OUTPUT" ]; then
    printf '%s\n' "$PROCESS_OUTPUT"
  fi
else
  printf '__OPSKAT_NVIDIA_PROCESS_UNAVAILABLE__\n'
fi
printf '__OPSKAT_NVIDIA_PROCESS_END__\n'
OPSKAT_NVIDIA`

type nvidiaResult struct {
	GPUs          []GPU
	DriverVersion string
	CUDAVersion   string
}

type nvidiaGPURecord struct {
	gpu    GPU
	uuid   string
	driver string
}

type nvidiaCollector struct{}

func init() {
	registerGPUCollector(nvidiaCollector{})
}

func (nvidiaCollector) Name() string  { return "nvidia-smi" }
func (nvidiaCollector) Priority() int { return 100 }
func (nvidiaCollector) Collect(ctx context.Context, run statusCommandRunner) (*gpuCollectorResult, error) {
	result, err := collectNVIDIA(ctx, run)
	if err != nil {
		return nil, err
	}
	return &gpuCollectorResult{
		GPUs:             result.GPUs,
		GPUDriverVersion: result.DriverVersion,
		CUDAVersion:      result.CUDAVersion,
	}, nil
}

func collectNVIDIA(ctx context.Context, run statusCommandRunner) (*nvidiaResult, error) {
	raw, err := run(ctx, nvidiaSMICommand, "collect NVIDIA GPU status failed")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(raw) == "" {
		return &nvidiaResult{}, nil
	}
	return parseNVIDIAOutput(raw)
}

func parseNVIDIAOutput(raw string) (*nvidiaResult, error) {
	var gpuLines []string
	var processLines []string
	var cudaVersion string
	var inGPUSection bool
	var inProcessSection bool
	var sawGPUBegin bool
	var sawGPUEnd bool
	var sawProcessBegin bool
	var sawProcessEnd bool
	var processUnavailable bool

	for _, rawLine := range strings.Split(raw, "\n") {
		line := strings.TrimSpace(rawLine)
		switch line {
		case "":
			continue
		case nvidiaGPUBegin:
			if sawGPUBegin || inProcessSection {
				return nil, fmt.Errorf("parse NVIDIA GPU status: invalid GPU section")
			}
			sawGPUBegin = true
			inGPUSection = true
		case nvidiaGPUEnd:
			if !inGPUSection {
				return nil, fmt.Errorf("parse NVIDIA GPU status: unexpected GPU section end")
			}
			inGPUSection = false
			sawGPUEnd = true
		case nvidiaProcessBegin:
			if inGPUSection || sawProcessBegin {
				return nil, fmt.Errorf("parse NVIDIA GPU status: invalid process section")
			}
			sawProcessBegin = true
			inProcessSection = true
		case nvidiaProcessEnd:
			if !inProcessSection {
				return nil, fmt.Errorf("parse NVIDIA GPU status: unexpected process section end")
			}
			inProcessSection = false
			sawProcessEnd = true
		case nvidiaProcessUnavailable:
			if inProcessSection {
				processUnavailable = true
			}
		default:
			switch {
			case inGPUSection:
				gpuLines = append(gpuLines, line)
			case inProcessSection:
				processLines = append(processLines, line)
			case strings.HasPrefix(line, nvidiaCUDAKey):
				cudaVersion = optionalString(strings.TrimPrefix(line, nvidiaCUDAKey))
			}
		}
	}

	if inGPUSection || !sawGPUBegin || !sawGPUEnd {
		return nil, fmt.Errorf("parse NVIDIA GPU status: incomplete GPU section")
	}
	if inProcessSection || sawProcessBegin != sawProcessEnd {
		processUnavailable = true
	}

	records := make([]nvidiaGPURecord, 0, len(gpuLines))
	for lineNumber, line := range gpuLines {
		record, err := parseNVIDIAGPURecord(line)
		if err != nil {
			return nil, fmt.Errorf("parse NVIDIA GPU status line %d: %w", lineNumber+1, err)
		}
		records = append(records, record)
	}

	processCounts := map[string]int(nil)
	if sawProcessBegin && sawProcessEnd && !processUnavailable {
		counts, err := parseNVIDIAProcessCounts(processLines)
		if err == nil {
			processCounts = counts
		}
	}

	result := &nvidiaResult{CUDAVersion: cudaVersion, GPUs: make([]GPU, 0, len(records))}
	for _, record := range records {
		if result.DriverVersion == "" && record.driver != "" {
			result.DriverVersion = record.driver
		}
		if processCounts != nil {
			count := processCounts[record.uuid]
			record.gpu.ComputeProcessCount = &count
		}
		if cudaVersion != "" {
			record.gpu.Runtime = "CUDA"
			record.gpu.RuntimeVersion = cudaVersion
		}
		result.GPUs = append(result.GPUs, record.gpu)
	}
	return result, nil
}

func parseNVIDIAGPURecord(line string) (nvidiaGPURecord, error) {
	fields, err := parseCSVLine(line)
	if err != nil {
		return nvidiaGPURecord{}, err
	}
	if len(fields) != 11 && len(fields) != 12 {
		return nvidiaGPURecord{}, fmt.Errorf("got %d fields, want 11 or 12", len(fields))
	}

	index, err := strconv.Atoi(fields[0])
	if err != nil {
		return nvidiaGPURecord{}, fmt.Errorf("parse index %q: %w", fields[0], err)
	}
	utilization, err := parseOptionalFloat(fields[3])
	if err != nil {
		return nvidiaGPURecord{}, fmt.Errorf("parse utilization %q: %w", fields[3], err)
	}
	memoryUsed, err := parseMiBBytes(fields[4])
	if err != nil {
		return nvidiaGPURecord{}, fmt.Errorf("parse used memory %q: %w", fields[4], err)
	}
	memoryTotal, err := parseMiBBytes(fields[5])
	if err != nil {
		return nvidiaGPURecord{}, fmt.Errorf("parse total memory %q: %w", fields[5], err)
	}
	temperature, err := parseOptionalFloat(fields[6])
	if err != nil {
		return nvidiaGPURecord{}, fmt.Errorf("parse temperature %q: %w", fields[6], err)
	}
	powerDraw, err := parseOptionalFloat(fields[7])
	if err != nil {
		return nvidiaGPURecord{}, fmt.Errorf("parse power draw %q: %w", fields[7], err)
	}
	powerLimit, err := parseOptionalFloat(fields[8])
	if err != nil {
		return nvidiaGPURecord{}, fmt.Errorf("parse power limit %q: %w", fields[8], err)
	}
	fanPercent, err := parseOptionalFloat(fields[9])
	if err != nil {
		return nvidiaGPURecord{}, fmt.Errorf("parse fan utilization %q: %w", fields[9], err)
	}

	record := nvidiaGPURecord{
		gpu: GPU{
			Index:              index,
			DeviceID:           optionalString(fields[1]),
			Vendor:             "NVIDIA",
			Name:               fields[2],
			DriverVersion:      optionalString(fields[10]),
			UtilizationPercent: utilization,
			MemoryUsedBytes:    memoryUsed,
			MemoryTotalBytes:   memoryTotal,
			TemperatureC:       temperature,
			PowerDrawWatts:     powerDraw,
			PowerLimitWatts:    powerLimit,
			FanPercent:         fanPercent,
		},
		uuid:   fields[1],
		driver: optionalString(fields[10]),
	}
	if len(fields) == 12 {
		record.gpu.PCIBusID = optionalString(fields[11])
	}
	return record, nil
}

func parseNVIDIAProcessCounts(lines []string) (map[string]int, error) {
	counts := make(map[string]int)
	for lineNumber, line := range lines {
		fields, err := parseCSVLine(line)
		if err != nil {
			return nil, fmt.Errorf("parse process line %d: %w", lineNumber+1, err)
		}
		if len(fields) != 2 || optionalString(fields[0]) == "" || optionalString(fields[1]) == "" {
			return nil, fmt.Errorf("parse process line %d: invalid record", lineNumber+1)
		}
		counts[fields[0]]++
	}
	return counts, nil
}

func parseCSVLine(line string) ([]string, error) {
	reader := csv.NewReader(strings.NewReader(line))
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	fields, err := reader.Read()
	if err != nil {
		return nil, err
	}
	for i := range fields {
		fields[i] = strings.TrimSpace(fields[i])
	}
	return fields, nil
}

func parseOptionalFloat(value string) (*float64, error) {
	if optionalString(value) == "" {
		return nil, nil
	}
	number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return nil, err
	}
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return nil, fmt.Errorf("non-finite number")
	}
	return &number, nil
}

func parseMiBBytes(value string) (*int64, error) {
	mebibytes, err := parseOptionalFloat(value)
	if err != nil || mebibytes == nil {
		return nil, err
	}
	bytes := *mebibytes * 1024 * 1024
	if bytes < 0 || bytes > math.MaxInt64 {
		return nil, fmt.Errorf("memory value out of range")
	}
	valueBytes := int64(math.Round(bytes))
	return &valueBytes, nil
}

func optionalString(value string) string {
	normalized := strings.TrimSpace(value)
	unavailable := strings.ToLower(strings.Trim(normalized, "[]"))
	switch unavailable {
	case "", "-", "n/a", "na", "not supported", "not available", "unsupported":
		return ""
	default:
		return normalized
	}
}

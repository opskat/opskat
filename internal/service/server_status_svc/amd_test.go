package server_status_svc

import (
	"context"
	"errors"
	"testing"
)

func TestParseAMDSMIJSON(t *testing.T) {
	result, err := parseAMDSMIJSON(
		readGPUFixture(t, "amd-smi-static.json"),
		readGPUFixture(t, "amd-smi-metric.json"),
		readGPUFixture(t, "amd-smi-process.json"),
		readGPUFixture(t, "amd-smi-version.json"),
	)
	if err != nil {
		t.Fatalf("parseAMDSMIJSON returned error: %v", err)
	}
	if len(result.GPUs) != 1 {
		t.Fatalf("len(GPUs) = %d, want 1", len(result.GPUs))
	}

	gpu := result.GPUs[0]
	if gpu.Index != 0 || gpu.Vendor != "AMD" || gpu.Name != "AMD Instinct MI250X" {
		t.Fatalf("unexpected AMD identity: %+v", gpu)
	}
	if gpu.DeviceID != "a1b2c3d4-0000-0000-0000-000000000000" || gpu.PCIBusID != "0000:41:00.0" {
		t.Fatalf("unexpected AMD stable identity: %+v", gpu)
	}
	if gpu.DriverVersion != "6.8.5" || gpu.Runtime != "ROCm" || gpu.RuntimeVersion != "6.4.1" {
		t.Fatalf("unexpected AMD metadata: %+v", gpu)
	}
	assertFloatMetric(t, "UtilizationPercent", gpu.UtilizationPercent, 73)
	assertInt64Metric(t, "MemoryUsedBytes", gpu.MemoryUsedBytes, 17179869184)
	assertInt64Metric(t, "MemoryTotalBytes", gpu.MemoryTotalBytes, 68702699520)
	assertFloatMetric(t, "TemperatureC", gpu.TemperatureC, 78)
	assertFloatMetric(t, "PowerDrawWatts", gpu.PowerDrawWatts, 420.5)
	assertFloatMetric(t, "PowerLimitWatts", gpu.PowerLimitWatts, 560)
	assertFloatMetric(t, "FanPercent", gpu.FanPercent, 45)
	if gpu.ComputeProcessCount == nil || *gpu.ComputeProcessCount != 2 {
		t.Fatalf("ComputeProcessCount = %v, want 2", gpu.ComputeProcessCount)
	}
}

func TestParseAMDSMIJSONAcceptsGPUKeyedSchemaAndUnitStrings(t *testing.T) {
	staticJSON := []byte(`{
  "gpu0": {
    "unique id": "amd-keyed-0",
    "market name": "AMD Instinct MI210",
    "BDF": "0000:31:00.0",
    "driver version": "6.7.0",
    "vram": {"size": "64 GiB"}
  }
}`)
	metricJSON := []byte(`{
  "gpu0": {
    "GPU utilization": "25 %",
    "used VRAM": "16 GiB",
    "total VRAM": "64 GiB",
    "hotspot": "70 C",
    "socket power": "300 W"
  }
}`)

	result, err := parseAMDSMIJSON(staticJSON, metricJSON, nil, nil)
	if err != nil {
		t.Fatalf("parseAMDSMIJSON returned error: %v", err)
	}
	if len(result.GPUs) != 1 {
		t.Fatalf("len(GPUs) = %d, want 1", len(result.GPUs))
	}
	gpu := result.GPUs[0]
	if gpu.DeviceID != "amd-keyed-0" || gpu.PCIBusID != "0000:31:00.0" {
		t.Fatalf("unexpected keyed-schema identity: %+v", gpu)
	}
	assertFloatMetric(t, "UtilizationPercent", gpu.UtilizationPercent, 25)
	assertInt64Metric(t, "MemoryUsedBytes", gpu.MemoryUsedBytes, 16*1024*1024*1024)
	assertInt64Metric(t, "MemoryTotalBytes", gpu.MemoryTotalBytes, 64*1024*1024*1024)
}

func TestAMDCollectorFallsBackToROCMSMIWhenAMDSMIIsUnusable(t *testing.T) {
	rocmOutput := rocmSMIFixtureOutput(t)
	var commands []string

	result, err := (amdCollector{}).Collect(context.Background(), func(_ context.Context, command, _ string) (string, error) {
		commands = append(commands, command)
		switch command {
		case amdSMICommand:
			return markedGPUSection(amdSMIStaticBegin, amdSMIStaticEnd, []byte("{malformed")), nil
		case rocmSMICommand:
			return rocmOutput, nil
		default:
			return "", errors.New("unexpected command")
		}
	})
	if err != nil {
		t.Fatalf("AMD collector returned error: %v", err)
	}
	if len(commands) != 2 || commands[0] != amdSMICommand || commands[1] != rocmSMICommand {
		t.Fatalf("commands = %#v, want amd-smi then rocm-smi", commands)
	}
	if len(result.GPUs) != 1 || result.GPUs[0].Name != "AMD Radeon PRO W7900" {
		t.Fatalf("unexpected ROCm fallback result: %+v", result.GPUs)
	}
	gpu := result.GPUs[0]
	if gpu.DeviceID != "0x1122334455667788" || gpu.PCIBusID != "0000:0d:00.0" || gpu.DriverVersion != "6.8.5" {
		t.Fatalf("unexpected ROCm fallback identity/metadata: %+v", gpu)
	}
	assertFloatMetric(t, "UtilizationPercent", gpu.UtilizationPercent, 61)
	assertInt64Metric(t, "MemoryUsedBytes", gpu.MemoryUsedBytes, 8589934592)
	assertInt64Metric(t, "MemoryTotalBytes", gpu.MemoryTotalBytes, 51527024640)
	assertFloatMetric(t, "TemperatureC", gpu.TemperatureC, 71)
	assertFloatMetric(t, "PowerDrawWatts", gpu.PowerDrawWatts, 248)
	assertFloatMetric(t, "PowerLimitWatts", gpu.PowerLimitWatts, 295)
	assertFloatMetric(t, "FanPercent", gpu.FanPercent, 38)
	if gpu.ComputeProcessCount == nil || *gpu.ComputeProcessCount != 3 {
		t.Fatalf("ComputeProcessCount = %v, want 3", gpu.ComputeProcessCount)
	}
}

func TestAMDCollectorDoesNotRunFallbackOrDuplicateWhenAMDSMIWorks(t *testing.T) {
	amdOutput := amdSMIFixtureOutput(t)
	rocmCalls := 0

	result, err := (amdCollector{}).Collect(context.Background(), func(_ context.Context, command, _ string) (string, error) {
		switch command {
		case amdSMICommand:
			return amdOutput, nil
		case rocmSMICommand:
			rocmCalls++
			return "", nil
		default:
			return "", errors.New("unexpected command")
		}
	})
	if err != nil {
		t.Fatalf("AMD collector returned error: %v", err)
	}
	if rocmCalls != 0 {
		t.Fatalf("rocm-smi calls = %d, want 0", rocmCalls)
	}
	if len(result.GPUs) != 1 {
		t.Fatalf("len(GPUs) = %d, want 1", len(result.GPUs))
	}
}

func assertFloatMetric(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}

func assertInt64Metric(t *testing.T, name string, got *int64, want int64) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}

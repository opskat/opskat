package server_status_svc

import "testing"

func TestParseXPUSMIJSON(t *testing.T) {
	result, err := parseXPUSMIJSON(
		readGPUFixture(t, "xpu-smi-discovery.json"),
		readGPUFixture(t, "xpu-smi-stats.json"),
	)
	if err != nil {
		t.Fatalf("parseXPUSMIJSON returned error: %v", err)
	}
	if len(result.GPUs) != 1 {
		t.Fatalf("len(GPUs) = %d, want 1", len(result.GPUs))
	}

	gpu := result.GPUs[0]
	if gpu.Index != 0 || gpu.Vendor != "Intel" || gpu.Name != "Intel(R) Data Center GPU Max 1550" {
		t.Fatalf("unexpected Intel identity: %+v", gpu)
	}
	if gpu.DeviceID != "00000000-0000-0000-0000-00000000abcd" || gpu.PCIBusID != "0000:bd:00.0" {
		t.Fatalf("unexpected Intel stable identity: %+v", gpu)
	}
	if gpu.DriverVersion != "1.3.30872" {
		t.Fatalf("DriverVersion = %q, want 1.3.30872", gpu.DriverVersion)
	}
	assertFloatMetric(t, "UtilizationPercent", gpu.UtilizationPercent, 52)
	assertInt64Metric(t, "MemoryUsedBytes", gpu.MemoryUsedBytes, 12884901888)
	assertInt64Metric(t, "MemoryTotalBytes", gpu.MemoryTotalBytes, 51539607552)
	assertFloatMetric(t, "TemperatureC", gpu.TemperatureC, 64)
	assertFloatMetric(t, "PowerDrawWatts", gpu.PowerDrawWatts, 231.5)
	if gpu.PowerLimitWatts != nil || gpu.FanPercent != nil {
		t.Fatalf("unsupported Intel power limit/fan metrics must remain unavailable: %+v", gpu)
	}
	if gpu.ComputeProcessCount != nil {
		t.Fatalf("ComputeProcessCount = %v, want unavailable", gpu.ComputeProcessCount)
	}
}

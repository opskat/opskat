package server_status_svc

import (
	"context"
	"errors"
	"testing"
	"time"
)

type testGPUCollector struct {
	name     string
	priority int
	collect  func(context.Context, statusCommandRunner) (*gpuCollectorResult, error)
}

func (c testGPUCollector) Name() string  { return c.name }
func (c testGPUCollector) Priority() int { return c.priority }
func (c testGPUCollector) Collect(ctx context.Context, run statusCommandRunner) (*gpuCollectorResult, error) {
	return c.collect(ctx, run)
}

func TestCollectGPUStatusCombinesMixedVendorsInRegistryOrder(t *testing.T) {
	collectors := []gpuCollector{
		stubGPUCollector("intel", 300, GPU{Index: 0, Vendor: "Intel", DeviceID: "intel-0"}),
		stubGPUCollector("nvidia", 100, GPU{Index: 0, Vendor: "NVIDIA", DeviceID: "nvidia-0"}),
		stubGPUCollector("amd", 200, GPU{Index: 0, Vendor: "AMD", DeviceID: "amd-0"}),
	}

	result := collectGPUStatusWithCollectors(context.Background(), nil, collectors)
	if len(result.GPUs) != 3 {
		t.Fatalf("len(GPUs) = %d, want 3", len(result.GPUs))
	}
	got := []string{result.GPUs[0].Vendor, result.GPUs[1].Vendor, result.GPUs[2].Vendor}
	want := []string{"NVIDIA", "AMD", "Intel"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("vendor order = %#v, want %#v", got, want)
		}
	}
}

func TestCollectGPUStatusPreservesSuccessfulCollectorsWhenOthersFailOrExpire(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	blockedStarted := make(chan struct{})
	fastFinished := make(chan struct{})
	collectors := []gpuCollector{
		testGPUCollector{
			name:     "nvidia",
			priority: 100,
			collect: func(context.Context, statusCommandRunner) (*gpuCollectorResult, error) {
				<-blockedStarted
				close(fastFinished)
				return &gpuCollectorResult{GPUs: []GPU{{Index: 0, Vendor: "NVIDIA", DeviceID: "nvidia-0"}}}, nil
			},
		},
		testGPUCollector{
			name:     "amd",
			priority: 200,
			collect: func(context.Context, statusCommandRunner) (*gpuCollectorResult, error) {
				return nil, errors.New("permission denied")
			},
		},
		testGPUCollector{
			name:     "intel",
			priority: 300,
			collect: func(ctx context.Context, _ statusCommandRunner) (*gpuCollectorResult, error) {
				close(blockedStarted)
				<-ctx.Done()
				return nil, ctx.Err()
			},
		},
	}

	done := make(chan gpuCollectorResult, 1)
	go func() {
		done <- collectGPUStatusWithCollectors(ctx, nil, collectors)
	}()

	waitForSignal(t, blockedStarted, "collectors did not start concurrently")
	waitForSignal(t, fastFinished, "successful collector did not finish")
	cancel()

	select {
	case result := <-done:
		if len(result.GPUs) != 1 || result.GPUs[0].Vendor != "NVIDIA" {
			t.Fatalf("successful GPUs were not preserved: %+v", result.GPUs)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GPU collection did not return after context cancellation")
	}
}

func TestCollectGPUStatusDeduplicatesStableDeviceIdentity(t *testing.T) {
	collectors := []gpuCollector{
		stubGPUCollector("amd-primary", 100, GPU{Index: 0, Vendor: "AMD", DeviceID: "same-device", Name: "primary"}),
		stubGPUCollector("amd-fallback", 200, GPU{Index: 0, Vendor: "AMD", DeviceID: "same-device", Name: "fallback"}),
	}

	result := collectGPUStatusWithCollectors(context.Background(), nil, collectors)
	if len(result.GPUs) != 1 {
		t.Fatalf("len(GPUs) = %d, want 1", len(result.GPUs))
	}
	if result.GPUs[0].Name != "primary" {
		t.Fatalf("dedup kept %q, want higher-priority collector result", result.GPUs[0].Name)
	}
}

func TestCollectGPUStatusMergesDRMInventoryIntoVendorTelemetryByPCIAddress(t *testing.T) {
	utilization := 42.0
	collectors := []gpuCollector{
		stubGPUCollector("nvidia", 100, GPU{
			Index: 0, Vendor: "NVIDIA", PCIBusID: "00000000:01:00.0", UtilizationPercent: &utilization,
		}),
		stubGPUCollector("linux-drm", 1000, GPU{
			Index: 0, Vendor: "NVIDIA", PCIBusID: "0000:01:00.0", Name: "NVIDIA RTX 4090", Driver: "nvidia",
		}),
	}

	result := collectGPUStatusWithCollectors(context.Background(), nil, collectors)
	if len(result.GPUs) != 1 {
		t.Fatalf("len(GPUs) = %d, want 1", len(result.GPUs))
	}
	gpu := result.GPUs[0]
	if gpu.Name != "NVIDIA RTX 4090" || gpu.Driver != "nvidia" || gpu.UtilizationPercent == nil || *gpu.UtilizationPercent != 42 {
		t.Fatalf("vendor telemetry and DRM inventory were not merged: %+v", gpu)
	}
}

func TestParseLinuxDRMInventoryReturnsBasicGPUWithoutTelemetry(t *testing.T) {
	result, err := parseLinuxDRMInventory(`__OPSKAT_DRM_GPU_BEGIN__
0	0x8086	0x4680	0000:06:10.0	i915	Intel Corporation AlderLake-S GT1 [8086:4680] (rev 0c)
__OPSKAT_DRM_GPU_END__
`)
	if err != nil {
		t.Fatalf("parseLinuxDRMInventory returned error: %v", err)
	}
	if len(result.GPUs) != 1 {
		t.Fatalf("len(GPUs) = %d, want 1", len(result.GPUs))
	}
	gpu := result.GPUs[0]
	if gpu.Index != 0 || gpu.Vendor != "Intel" || gpu.Name != "Intel Corporation AlderLake-S GT1 [8086:4680] (rev 0c)" {
		t.Fatalf("unexpected GPU identity: %+v", gpu)
	}
	if gpu.DeviceID != "8086:4680" || gpu.PCIBusID != "0000:06:10.0" || gpu.Driver != "i915" {
		t.Fatalf("unexpected GPU metadata: %+v", gpu)
	}
	if gpu.UtilizationPercent != nil || gpu.MemoryUsedBytes != nil || gpu.TemperatureC != nil {
		t.Fatalf("inventory GPU must not fabricate telemetry: %+v", gpu)
	}
}

func stubGPUCollector(name string, priority int, gpu GPU) gpuCollector {
	return testGPUCollector{
		name:     name,
		priority: priority,
		collect: func(context.Context, statusCommandRunner) (*gpuCollectorResult, error) {
			return &gpuCollectorResult{GPUs: []GPU{gpu}}, nil
		},
	}
}

func waitForSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatal(message)
	}
}

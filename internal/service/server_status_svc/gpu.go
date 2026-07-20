package server_status_svc

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

const gpuOptionalBudget = 2 * time.Second

// gpuCollector is the extension seam for optional accelerator telemetry.
// Implementations register themselves from their vendor-specific source file.
type gpuCollector interface {
	Name() string
	Priority() int
	Collect(context.Context, statusCommandRunner) (*gpuCollectorResult, error)
}

type gpuCollectorResult struct {
	GPUs             []GPU
	GPUDriverVersion string
	CUDAVersion      string
}

var gpuCollectorRegistry struct {
	sync.RWMutex
	collectors []gpuCollector
}

func registerGPUCollector(collector gpuCollector) {
	gpuCollectorRegistry.Lock()
	defer gpuCollectorRegistry.Unlock()
	for _, registered := range gpuCollectorRegistry.collectors {
		if registered.Name() == collector.Name() {
			panic(fmt.Sprintf("GPU collector %q already registered", collector.Name()))
		}
	}
	gpuCollectorRegistry.collectors = append(gpuCollectorRegistry.collectors, collector)
}

func registeredGPUCollectors() []gpuCollector {
	gpuCollectorRegistry.RLock()
	defer gpuCollectorRegistry.RUnlock()
	collectors := append([]gpuCollector(nil), gpuCollectorRegistry.collectors...)
	sortGPUCollectors(collectors)
	return collectors
}

func collectGPUStatus(ctx context.Context, run statusCommandRunner) gpuCollectorResult {
	return collectGPUStatusWithCollectors(ctx, run, registeredGPUCollectors())
}

func collectGPUStatusWithCollectors(
	ctx context.Context,
	run statusCommandRunner,
	collectors []gpuCollector,
) gpuCollectorResult {
	collectors = append([]gpuCollector(nil), collectors...)
	sortGPUCollectors(collectors)

	type collectorRun struct {
		index  int
		name   string
		result *gpuCollectorResult
		err    error
	}

	runs := make(chan collectorRun, len(collectors))
	var wg sync.WaitGroup
	for index, collector := range collectors {
		wg.Add(1)
		go func(index int, collector gpuCollector) {
			defer wg.Done()
			result, err := collector.Collect(ctx, run)
			runs <- collectorRun{index: index, name: collector.Name(), result: result, err: err}
		}(index, collector)
	}
	go func() {
		wg.Wait()
		close(runs)
	}()

	ordered := make([]*gpuCollectorResult, len(collectors))
	for runResult := range runs {
		if runResult.err != nil {
			logger.Ctx(ctx).Debug("GPU collector unavailable",
				zap.String("collector", runResult.name),
				zap.Error(runResult.err),
			)
			continue
		}
		ordered[runResult.index] = runResult.result
	}

	combined := gpuCollectorResult{}
	seen := make(map[string]int)
	for _, result := range ordered {
		if result == nil {
			continue
		}
		if combined.GPUDriverVersion == "" && result.GPUDriverVersion != "" {
			combined.GPUDriverVersion = result.GPUDriverVersion
		}
		if combined.CUDAVersion == "" && result.CUDAVersion != "" {
			combined.CUDAVersion = result.CUDAVersion
		}
		for _, gpu := range result.GPUs {
			key := gpuIdentityKey(gpu)
			if existingIndex, exists := seen[key]; exists {
				mergeGPUFields(&combined.GPUs[existingIndex], gpu)
				continue
			}
			seen[key] = len(combined.GPUs)
			combined.GPUs = append(combined.GPUs, gpu)
		}
	}
	return combined
}

func sortGPUCollectors(collectors []gpuCollector) {
	sort.SliceStable(collectors, func(i, j int) bool {
		if collectors[i].Priority() == collectors[j].Priority() {
			return collectors[i].Name() < collectors[j].Name()
		}
		return collectors[i].Priority() < collectors[j].Priority()
	})
}

func gpuIdentityKey(gpu GPU) string {
	vendor := strings.ToLower(strings.TrimSpace(gpu.Vendor))
	if pci := normalizePCIBusID(gpu.PCIBusID); pci != "" {
		return vendor + "|pci|" + pci
	}
	if id := strings.ToLower(strings.TrimSpace(gpu.DeviceID)); id != "" {
		return vendor + "|id|" + id
	}
	return fmt.Sprintf("%s|index|%d|name|%s", vendor, gpu.Index, strings.ToLower(strings.TrimSpace(gpu.Name)))
}

func normalizePCIBusID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	parts := strings.Split(value, ":")
	if len(parts) == 3 && len(parts[0]) > 4 {
		parts[0] = parts[0][len(parts[0])-4:]
		return strings.Join(parts, ":")
	}
	return value
}

func extractMarkedOutput(raw, begin, end string) ([]byte, error) {
	beginIndex := strings.Index(raw, begin)
	if beginIndex < 0 {
		return nil, nil
	}
	payloadStart := beginIndex + len(begin)
	endIndex := strings.Index(raw[payloadStart:], end)
	if endIndex < 0 {
		return nil, fmt.Errorf("output section %q is incomplete", begin)
	}
	payload := strings.TrimSpace(raw[payloadStart : payloadStart+endIndex])
	return []byte(payload), nil
}

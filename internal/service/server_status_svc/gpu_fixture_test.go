package server_status_svc

import (
	"os"
	"path/filepath"
	"testing"
)

func readGPUFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func markedGPUSection(begin, end string, payload []byte) string {
	return begin + "\n" + string(payload) + "\n" + end + "\n"
}

func amdSMIFixtureOutput(t *testing.T) string {
	t.Helper()
	return markedGPUSection(amdSMIStaticBegin, amdSMIStaticEnd, readGPUFixture(t, "amd-smi-static.json")) +
		markedGPUSection(amdSMIMetricBegin, amdSMIMetricEnd, readGPUFixture(t, "amd-smi-metric.json")) +
		markedGPUSection(amdSMIProcessBegin, amdSMIProcessEnd, readGPUFixture(t, "amd-smi-process.json")) +
		markedGPUSection(amdSMIVersionBegin, amdSMIVersionEnd, readGPUFixture(t, "amd-smi-version.json"))
}

func rocmSMIFixtureOutput(t *testing.T) string {
	t.Helper()
	return markedGPUSection(rocmSMICoreBegin, rocmSMICoreEnd, readGPUFixture(t, "rocm-smi.json")) +
		markedGPUSection(rocmSMIProcessBegin, rocmSMIProcessEnd, readGPUFixture(t, "rocm-smi-process.json"))
}

func xpuSMIFixtureOutput(t *testing.T) string {
	t.Helper()
	return markedGPUSection(xpuSMIDiscoveryBegin, xpuSMIDiscoveryEnd, readGPUFixture(t, "xpu-smi-discovery.json")) +
		markedGPUSection(xpuSMIStatsBegin, xpuSMIStatsEnd, readGPUFixture(t, "xpu-smi-stats.json"))
}

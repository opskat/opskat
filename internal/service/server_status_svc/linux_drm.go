package server_status_svc

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

const (
	linuxDRMInventoryBegin = "__OPSKAT_DRM_GPU_BEGIN__"
	linuxDRMInventoryEnd   = "__OPSKAT_DRM_GPU_END__"
)

// linuxDRMInventoryCommand discovers Linux DRM cards without requiring a vendor CLI.
// It returns identity only; performance metrics remain unset unless a vendor collector supplies them.
const linuxDRMInventoryCommand = `sh <<'OPSKAT_DRM_GPU'
printf '__OPSKAT_DRM_GPU_BEGIN__\n'
index=0
for card in /sys/class/drm/card[0-9]*; do
  [ -d "$card/device" ] || continue
  vendor=$(cat "$card/device/vendor" 2>/dev/null) || continue
  device=$(cat "$card/device/device" 2>/dev/null) || continue
  device_path=$(readlink -f "$card/device" 2>/dev/null) || continue
  bdf=${device_path##*/}
  driver_path=$(readlink -f "$card/device/driver" 2>/dev/null || true)
  driver=${driver_path##*/}
  name=""
  if command -v lspci >/dev/null 2>&1; then
    name=$(LC_ALL=C lspci -D -s "$bdf" 2>/dev/null | sed 's/^[^ ]* //; s/^[^:]*: //' | tr '\t' ' ')
  fi
  printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$index" "$vendor" "$device" "$bdf" "$driver" "$name"
  index=$((index+1))
done
printf '__OPSKAT_DRM_GPU_END__\n'
OPSKAT_DRM_GPU`

type linuxDRMCollector struct{}

func init() {
	registerGPUCollector(linuxDRMCollector{})
}

func (linuxDRMCollector) Name() string  { return "linux-drm" }
func (linuxDRMCollector) Priority() int { return 1000 }
func (linuxDRMCollector) Collect(ctx context.Context, run statusCommandRunner) (*gpuCollectorResult, error) {
	raw, err := run(ctx, linuxDRMInventoryCommand, "discover Linux DRM GPUs failed")
	if err != nil {
		return nil, err
	}
	return parseLinuxDRMInventory(raw)
}

func parseLinuxDRMInventory(raw string) (*gpuCollectorResult, error) {
	payload, err := extractMarkedOutput(raw, linuxDRMInventoryBegin, linuxDRMInventoryEnd)
	if err != nil {
		return nil, err
	}
	result := &gpuCollectorResult{}
	for _, line := range strings.Split(string(payload), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 6)
		if len(fields) != 6 {
			return nil, fmt.Errorf("parse Linux DRM inventory record: got %d fields", len(fields))
		}
		index, parseErr := strconv.Atoi(strings.TrimSpace(fields[0]))
		if parseErr != nil {
			return nil, fmt.Errorf("parse Linux DRM card index %q: %w", fields[0], parseErr)
		}
		vendorID := normalizePCIID(fields[1])
		deviceID := normalizePCIID(fields[2])
		vendor := pciVendorName(vendorID)
		name := strings.TrimSpace(fields[5])
		if name == "" {
			name = vendor + " GPU"
			if deviceID != "" {
				name += " " + deviceID
			}
		}
		stableID := deviceID
		if vendorID != "" && deviceID != "" {
			stableID = vendorID + ":" + deviceID
		}
		result.GPUs = append(result.GPUs, GPU{
			Index:    index,
			DeviceID: stableID,
			PCIBusID: normalizePCIBusID(fields[3]),
			Vendor:   vendor,
			Name:     name,
			Driver:   strings.TrimSpace(fields[4]),
		})
	}
	return result, nil
}

func normalizePCIID(value string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(value), "0x"))
}

func pciVendorName(vendorID string) string {
	switch vendorID {
	case "10de":
		return "NVIDIA"
	case "1002":
		return "AMD"
	case "8086":
		return "Intel"
	default:
		if vendorID == "" {
			return "GPU"
		}
		return "PCI " + vendorID
	}
}

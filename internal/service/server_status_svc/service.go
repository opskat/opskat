package server_status_svc

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// Snapshot 表示一次服务器状态快照。
type Snapshot struct {
	Hostname         string  `json:"hostname,omitempty"`
	OS               string  `json:"os,omitempty"`
	Uptime           string  `json:"uptime,omitempty"`
	CPUPercent       float64 `json:"cpuPercent,omitempty"`
	Load1            float64 `json:"load1,omitempty"`
	Load5            float64 `json:"load5,omitempty"`
	Load15           float64 `json:"load15,omitempty"`
	MemoryUsedBytes  int64   `json:"memoryUsedBytes,omitempty"`
	MemoryTotalBytes int64   `json:"memoryTotalBytes,omitempty"`
	DiskMount        string  `json:"diskMount,omitempty"`
	DiskUsedBytes    int64   `json:"diskUsedBytes,omitempty"`
	DiskTotalBytes   int64   `json:"diskTotalBytes,omitempty"`
	CollectedAt      int64   `json:"collectedAt,omitempty"`
}

const snapshotCommand = `sh <<'OPSKAT_STATUS'
OS=$(uname -s 2>/dev/null || echo unknown)
HOST=$(hostname 2>/dev/null || echo unknown)
UPTIME_TEXT=$(uptime 2>/dev/null | tr '\n' ' ' | sed 's/[[:space:]]\+/ /g; s/^ //; s/ $//')

LOAD1=""
LOAD5=""
LOAD15=""
if [ -r /proc/loadavg ]; then
  set -- $(cat /proc/loadavg)
  LOAD1=$1
  LOAD5=$2
  LOAD15=$3
elif [ "$OS" = "Darwin" ]; then
  set -- $(sysctl -n vm.loadavg 2>/dev/null | tr -d '{}')
  LOAD1=$1
  LOAD5=$2
  LOAD15=$3
fi

CPU_PERCENT=""
if [ -r /proc/stat ]; then
  read -r _ u1 n1 s1 i1 w1 irq1 sirq1 st1 _ < /proc/stat
  t1=$((u1+n1+s1+i1+w1+irq1+sirq1+st1))
  idle1=$((i1+w1))
  sleep 0.2
  read -r _ u2 n2 s2 i2 w2 irq2 sirq2 st2 _ < /proc/stat
  t2=$((u2+n2+s2+i2+w2+irq2+sirq2+st2))
  idle2=$((i2+w2))
  dt=$((t2-t1))
  didle=$((idle2-idle1))
  if [ "$dt" -gt 0 ]; then
    CPU_PERCENT=$(awk -v dt="$dt" -v didle="$didle" 'BEGIN { printf "%.1f", (dt-didle)*100/dt }')
  fi
elif [ "$OS" = "Darwin" ]; then
  CPU_PERCENT=$(top -l 2 -n 0 2>/dev/null | awk -F'[:,%]' '/CPU usage/ { idle=$(NF-1); used=100-idle } END { if (used != "") printf "%.1f", used }')
fi

MEM_TOTAL_BYTES=""
MEM_USED_BYTES=""
if [ -r /proc/meminfo ]; then
  mem_total_kb=$(awk '/MemTotal:/ {print $2}' /proc/meminfo)
  mem_available_kb=$(awk '/MemAvailable:/ {print $2}' /proc/meminfo)
  if [ -n "$mem_total_kb" ] && [ -n "$mem_available_kb" ]; then
    MEM_TOTAL_BYTES=$((mem_total_kb*1024))
    MEM_USED_BYTES=$(((mem_total_kb-mem_available_kb)*1024))
  fi
elif [ "$OS" = "Darwin" ]; then
  page_size=$(pagesize 2>/dev/null || echo 4096)
  mem_total_bytes=$(sysctl -n hw.memsize 2>/dev/null)
  pages_free=$(vm_stat 2>/dev/null | awk '/Pages free/ {gsub("\\.","",$3); print $3}')
  pages_inactive=$(vm_stat 2>/dev/null | awk '/Pages inactive/ {gsub("\\.","",$3); print $3}')
  pages_speculative=$(vm_stat 2>/dev/null | awk '/Pages speculative/ {gsub("\\.","",$3); print $3}')
  if [ -n "$mem_total_bytes" ] && [ -n "$pages_free" ] && [ -n "$pages_inactive" ]; then
    free_pages=$((pages_free + pages_inactive + ${pages_speculative:-0}))
    MEM_TOTAL_BYTES=$mem_total_bytes
    MEM_USED_BYTES=$((mem_total_bytes - free_pages*page_size))
  fi
fi

DISK_MOUNT="/"
DISK_TOTAL_BYTES=""
DISK_USED_BYTES=""
df_line=$(df -Pk / 2>/dev/null | awk 'NR==2 {print $2" "$3" "$6}')
if [ -n "$df_line" ]; then
  set -- $df_line
  DISK_TOTAL_BYTES=$(awk -v kb="$1" 'BEGIN { printf "%.0f", kb*1024 }')
  DISK_USED_BYTES=$(awk -v kb="$2" 'BEGIN { printf "%.0f", kb*1024 }')
  DISK_MOUNT=$3
fi

printf 'OS=%s\n' "$OS"
printf 'HOST=%s\n' "$HOST"
printf 'UPTIME=%s\n' "$UPTIME_TEXT"
printf 'LOAD1=%s\n' "$LOAD1"
printf 'LOAD5=%s\n' "$LOAD5"
printf 'LOAD15=%s\n' "$LOAD15"
printf 'CPU_PERCENT=%s\n' "$CPU_PERCENT"
printf 'MEM_TOTAL_BYTES=%s\n' "$MEM_TOTAL_BYTES"
printf 'MEM_USED_BYTES=%s\n' "$MEM_USED_BYTES"
printf 'DISK_MOUNT=%s\n' "$DISK_MOUNT"
printf 'DISK_TOTAL_BYTES=%s\n' "$DISK_TOTAL_BYTES"
printf 'DISK_USED_BYTES=%s\n' "$DISK_USED_BYTES"
OPSKAT_STATUS`

// Collect 从当前 SSH 连接采集服务器状态。
func Collect(ctx context.Context, client *ssh.Client) (*Snapshot, error) {
	if client == nil {
		return nil, fmt.Errorf("ssh client is nil")
	}

	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("create ssh session: %w", err)
	}
	defer func() {
		_ = session.Close()
	}()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	runCh := make(chan error, 1)
	go func() {
		runCh <- session.Run(snapshotCommand)
	}()

	select {
	case err := <-runCh:
		if err != nil {
			if stderr.Len() > 0 {
				return nil, fmt.Errorf("collect server status failed: %s", strings.TrimSpace(stderr.String()))
			}
			return nil, fmt.Errorf("collect server status failed: %w", err)
		}
	case <-ctx.Done():
		_ = session.Close()
		return nil, ctx.Err()
	}

	snapshot, err := parseSnapshot(stdout.String())
	if err != nil {
		return nil, err
	}
	snapshot.CollectedAt = time.Now().UnixMilli()
	return snapshot, nil
}

func parseSnapshot(raw string) (*Snapshot, error) {
	values := make(map[string]string)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[key] = strings.TrimSpace(value)
	}

	snapshot := &Snapshot{
		Hostname:  values["HOST"],
		OS:        values["OS"],
		Uptime:    values["UPTIME"],
		DiskMount: values["DISK_MOUNT"],
	}
	snapshot.CPUPercent = parseFloat(values["CPU_PERCENT"])
	snapshot.Load1 = parseFloat(values["LOAD1"])
	snapshot.Load5 = parseFloat(values["LOAD5"])
	snapshot.Load15 = parseFloat(values["LOAD15"])
	snapshot.MemoryTotalBytes = parseInt64(values["MEM_TOTAL_BYTES"])
	snapshot.MemoryUsedBytes = parseInt64(values["MEM_USED_BYTES"])
	snapshot.DiskTotalBytes = parseInt64(values["DISK_TOTAL_BYTES"])
	snapshot.DiskUsedBytes = parseInt64(values["DISK_USED_BYTES"])

	if snapshot.Hostname == "" &&
		snapshot.OS == "" &&
		snapshot.Uptime == "" &&
		snapshot.MemoryTotalBytes == 0 &&
		snapshot.DiskTotalBytes == 0 {
		return nil, fmt.Errorf("parse server status: empty snapshot")
	}

	return snapshot, nil
}

func parseFloat(value string) float64 {
	if value == "" {
		return 0
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0
	}
	return number
}

func parseInt64(value string) int64 {
	if value == "" {
		return 0
	}
	number, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0
	}
	return number
}

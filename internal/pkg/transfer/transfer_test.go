package transfer

import (
	"strings"
	"testing"
	"time"
)

func TestGenerateIDUniqueAndPrefixed(t *testing.T) {
	a := GenerateID("sftp")
	b := GenerateID("sftp")
	if a == b {
		t.Fatalf("expected unique ids, got %q == %q", a, b)
	}
	if !strings.HasPrefix(a, "sftp-") {
		t.Fatalf("expected prefix sftp-, got %q", a)
	}
	if z := GenerateID("zmodem"); !strings.HasPrefix(z, "zmodem-") {
		t.Fatalf("expected prefix zmodem-, got %q", z)
	}
}

func TestReporterThrottleAndSpeed(t *testing.T) {
	cur := time.Unix(0, 0)
	clock := func() time.Time { return cur }
	var got []Progress
	r := newReporter(func(p Progress) { got = append(got, p) }, clock)

	// t=0: 首条 progress 立即放行（此时 elapsed=0，速率仍为 0）。
	r.Report(Progress{Status: "progress", BytesDone: 100})
	// t=50ms: 距上次不足 100ms，被节流丢弃。
	cur = cur.Add(50 * time.Millisecond)
	r.Report(Progress{Status: "progress", BytesDone: 200})
	// t=150ms: 放行，平均速率 = 300 bytes / 0.15s = 2000 bytes/s。
	cur = cur.Add(100 * time.Millisecond)
	r.Report(Progress{Status: "progress", BytesDone: 300})

	if len(got) != 2 {
		t.Fatalf("want 2 progress emits (throttled), got %d", len(got))
	}
	if got[0].Speed != 0 {
		t.Fatalf("want first speed 0, got %d", got[0].Speed)
	}
	if got[1].Speed != 2000 {
		t.Fatalf("want speed 2000, got %d", got[1].Speed)
	}
}

func TestReporterEmitsTerminalImmediately(t *testing.T) {
	cur := time.Unix(0, 0)
	clock := func() time.Time { return cur }
	var statuses []string
	r := newReporter(func(p Progress) { statuses = append(statuses, p.Status) }, clock)

	r.Report(Progress{Status: "progress", BytesDone: 1}) // 立即
	r.Report(Progress{Status: "progress", BytesDone: 2}) // 同一时刻，被节流
	r.Report(Progress{Status: "done"})                   // 终态，不受节流，立即

	if len(statuses) != 2 || statuses[0] != "progress" || statuses[1] != "done" {
		t.Fatalf("want [progress done], got %v", statuses)
	}
}

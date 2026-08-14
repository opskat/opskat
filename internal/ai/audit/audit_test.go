package audit

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"

	"github.com/opskat/opskat/internal/ai/aictx"
)

func TestTruncateString(t *testing.T) {
	convey.Convey("字符串截断", t, func() {
		convey.Convey("短字符串不截断", func() {
			assert.Equal(t, "hello", truncateString("hello", 10))
		})

		convey.Convey("超长字符串截断到指定长度并追加标记", func() {
			result := truncateString("abcdefghij", 5)
			assert.Equal(t, "abcde\n...[truncated]", result)
		})

		convey.Convey("空字符串返回空", func() {
			assert.Equal(t, "", truncateString("", 10))
		})
	})
}

// TestWriteToolCall_PreservesAllColumnsVerbatimIncludingLiteralRedacted locks the
// raw-by-default Audit contract (spec §"Audit raw-by-default"): the default writer
// must save command/request/result/error exactly as it receives them — no canonical
// value replacement, no JSON re-encoding. A literal "<redacted>" is just an ordinary
// value now: it must survive byte-for-byte, and so must every raw secret next to it.
// The payloads are deliberately shaped so that the old canonical redactor would have
// altered each column (flag/token replacement, map-key re-sort, Authorization-token
// rewrite), so this test fails as long as any redaction path remains.
func TestWriteToolCall_PreservesAllColumnsVerbatimIncludingLiteralRedacted(t *testing.T) {
	repo := setupAuditRepo(t)
	command := `client --password <redacted> --token=raw-token`
	argsJSON := `{"asset":"db","command":"connect","config":{"password":"<redacted>","host":"db.internal"}}`
	result := `{"password":"<redacted>","value":"raw-result","id":7}`
	errMsg := "Authorization: Bearer <redacted>err-token; detail=raw-error"

	NewDefaultAuditWriter().WriteToolCall(context.Background(), ToolCallInfo{
		ToolName: "exec",
		ArgsJSON: argsJSON,
		Command:  command,
		Result:   result,
		Error:    errors.New(errMsg),
	})

	if len(repo.logs) != 1 {
		t.Fatalf("expected 1 audit log, got %d", len(repo.logs))
	}
	entry := repo.logs[0]
	for column, value := range map[string]string{
		"command": entry.Command,
		"request": entry.Request,
		"result":  entry.Result,
		"error":   entry.Error,
	} {
		if !strings.Contains(value, "<redacted>") {
			t.Fatalf("audit %s must preserve the literal <redacted> marker: %q", column, value)
		}
	}
	if entry.Command != command {
		t.Fatalf("Command = %q, want verbatim %q", entry.Command, command)
	}
	if entry.Request != argsJSON {
		t.Fatalf("Request = %q, want verbatim %q", entry.Request, argsJSON)
	}
	if entry.Result != result {
		t.Fatalf("Result = %q, want verbatim %q", entry.Result, result)
	}
	if entry.Error != errMsg {
		t.Fatalf("Error = %q, want verbatim %q", entry.Error, errMsg)
	}
}

// TestWriteToolCall_PreservesRequestJSONFormatting verifies the request column keeps
// the writer's original JSON formatting instead of decoding and re-encoding it. The
// old canonical redactor round-tripped the payload through encoding/json, which
// compacts whitespace and re-sorts map keys; the raw-by-default writer must not.
func TestWriteToolCall_PreservesRequestJSONFormatting(t *testing.T) {
	repo := setupAuditRepo(t)
	argsJSON := "{\n  \"asset\": \"db\",\n  \"command\": \"connect --password db-pass\"\n}"
	NewDefaultAuditWriter().WriteToolCall(context.Background(), ToolCallInfo{
		ToolName: "exec",
		ArgsJSON: argsJSON,
	})

	if len(repo.logs) != 1 {
		t.Fatalf("expected 1 audit log, got %d", len(repo.logs))
	}
	if got := repo.logs[0].Request; got != argsJSON {
		t.Fatalf("Request = %q, want verbatim formatting %q", got, argsJSON)
	}
}

// TestWriteToolCall_RequestStillTruncatedAt4096 pins the existing 4096-byte truncation
// of the request column: removing redaction must not remove the truncation.
func TestWriteToolCall_RequestStillTruncatedAt4096(t *testing.T) {
	repo := setupAuditRepo(t)
	argsJSON := `{"asset":"db","command":"echo ` + strings.Repeat("x", 4200) + `"}`
	NewDefaultAuditWriter().WriteToolCall(context.Background(), ToolCallInfo{
		ToolName: "exec",
		ArgsJSON: argsJSON,
	})

	if len(repo.logs) != 1 {
		t.Fatalf("expected 1 audit log, got %d", len(repo.logs))
	}
	got := repo.logs[0].Request
	if len(got) != 4096+len("\n...[truncated]") {
		t.Fatalf("Request length = %d, want %d", len(got), 4096+len("\n...[truncated]"))
	}
	if !strings.HasSuffix(got, "\n...[truncated]") {
		t.Fatalf("Request must end with truncation marker, got: %q", got[len(got)-20:])
	}
}

// TestWriteToolCall_ResultStillTruncatedAt32768 pins the existing 32768-byte truncation
// of the result column.
func TestWriteToolCall_ResultStillTruncatedAt32768(t *testing.T) {
	repo := setupAuditRepo(t)
	result := `{"id":7,"data":"` + strings.Repeat("y", 33000) + `"}`
	NewDefaultAuditWriter().WriteToolCall(context.Background(), ToolCallInfo{
		ToolName: "get_asset",
		ArgsJSON: `{"id":7}`,
		Result:   result,
	})

	if len(repo.logs) != 1 {
		t.Fatalf("expected 1 audit log, got %d", len(repo.logs))
	}
	got := repo.logs[0].Result
	if len(got) != 32768+len("\n...[truncated]") {
		t.Fatalf("Result length = %d, want %d", len(got), 32768+len("\n...[truncated]"))
	}
	if !strings.HasSuffix(got, "\n...[truncated]") {
		t.Fatalf("Result must end with truncation marker, got: %q", got[len(got)-20:])
	}
}

// TestWriteToolCall_DenyWithoutErrorUsesRawDecisionMessage pins the deny path where no
// handler error exists: the error column falls back to the decision message verbatim.
func TestWriteToolCall_DenyWithoutErrorUsesRawDecisionMessage(t *testing.T) {
	repo := setupAuditRepo(t)
	message := "rejected: password=dec-msg-secret <redacted>"
	NewDefaultAuditWriter().WriteToolCall(context.Background(), ToolCallInfo{
		ToolName: "exec",
		ArgsJSON: `{"asset":"db","command":"connect"}`,
		Result:   "USER DENIED",
		Decision: &aictx.CheckResult{
			Decision:       aictx.Deny,
			DecisionSource: aictx.SourcePolicyDeny,
			Message:        message,
		},
	})

	if len(repo.logs) != 1 {
		t.Fatalf("expected 1 audit log, got %d", len(repo.logs))
	}
	entry := repo.logs[0]
	if entry.Success != 0 {
		t.Fatalf("deny must set success=0, got %d", entry.Success)
	}
	if entry.Error != message {
		t.Fatalf("Error = %q, want raw decision message %q", entry.Error, message)
	}
}

// TestWriteToolCall_DenyWithoutMessageFallsBackToRawResult pins the final deny fallback:
// with no handler error and no decision message, the error column is the raw result text.
func TestWriteToolCall_DenyWithoutMessageFallsBackToRawResult(t *testing.T) {
	repo := setupAuditRepo(t)
	result := `{"ok":false,"reason":"password=res-secret <redacted>"}`
	NewDefaultAuditWriter().WriteToolCall(context.Background(), ToolCallInfo{
		ToolName: "exec",
		ArgsJSON: `{"asset":"db","command":"connect"}`,
		Result:   result,
		Decision: &aictx.CheckResult{
			Decision:       aictx.Deny,
			DecisionSource: aictx.SourceUserDeny,
		},
	})

	if len(repo.logs) != 1 {
		t.Fatalf("expected 1 audit log, got %d", len(repo.logs))
	}
	entry := repo.logs[0]
	if entry.Error != result {
		t.Fatalf("Error = %q, want raw result fallback %q", entry.Error, result)
	}
}

// TestWriteGrantSubmitAudit_PreservesRawPatterns pins grant_submit rows to store the
// joined patterns verbatim — literal <redacted> and any secret text included.
func TestWriteGrantSubmitAudit_PreservesRawPatterns(t *testing.T) {
	repo := setupAuditRepo(t)
	patterns := []string{"kubectl --token=<redacted> logs *", "ssh --password real-pattern-secret"}
	WriteGrantSubmitAudit(context.Background(), 3, "web-1", patterns)

	if len(repo.logs) != 1 {
		t.Fatalf("expected 1 audit log, got %d", len(repo.logs))
	}
	entry := repo.logs[0]
	if entry.ToolName != "grant_submit" {
		t.Fatalf("ToolName = %q, want grant_submit", entry.ToolName)
	}
	want := strings.Join(patterns, ", ")
	if entry.Command != want {
		t.Fatalf("Command = %q, want raw %q", entry.Command, want)
	}
}

// TestWriteGrantDiscardedAudit_PreservesRawCommand pins grant_discarded rows to store
// the command verbatim.
func TestWriteGrantDiscardedAudit_PreservesRawCommand(t *testing.T) {
	repo := setupAuditRepo(t)
	command := "object delete mybucket/logs/ --token=<redacted> --password=raw-grant-secret"
	WriteGrantDiscardedAudit(context.Background(), 3, "web-1", command)

	if len(repo.logs) != 1 {
		t.Fatalf("expected 1 audit log, got %d", len(repo.logs))
	}
	if got := repo.logs[0].Command; got != command {
		t.Fatalf("Command = %q, want raw %q", got, command)
	}
}

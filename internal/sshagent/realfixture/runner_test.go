package realfixture

import (
	"context"
	"os"
	"testing"

	"golang.org/x/crypto/ssh"
)

// runAndReport executes the fixture scenarios, sanitizes the report JSON and
// the run log against every secret the fixture could have leaked (the private
// key material, the public key blob and the MFA answers), writes the
// machine-readable report and log to the OPSKAT_FIXTURE_OUT / OPSKAT_FIXTURE_LOG
// paths when set, and reports whether every scenario passed with no secret
// leakage. It is the shared driver for the unix and windows entry points.
func runAndReport(t *testing.T, ctx context.Context, cfg Config, private, answers, pubBlobs []string) bool {
	t.Helper()
	report, logText := Run(ctx, cfg)
	reportJSON := ReportJSON(report)

	leaks := scanArtifacts(reportJSON, logText, private, answers, pubBlobs)
	report.Sanitized = len(leaks) == 0
	for _, l := range leaks {
		t.Errorf("secret leak in %s: %s", l.Where, l.Kind)
	}
	report.AllPass = report.AllPass && report.Sanitized

	t.Logf("\n%s\n", ReportJSON(report))
	t.Logf("fixture log:\n%s", logText)

	if out := os.Getenv("OPSKAT_FIXTURE_OUT"); out != "" {
		if err := WriteReport(out, report); err != nil {
			t.Errorf("write report to %s: %v", out, err)
		}
	}
	if logPath := os.Getenv("OPSKAT_FIXTURE_LOG"); logPath != "" {
		if err := os.WriteFile(logPath, []byte(logText), 0o600); err != nil { //nolint:gosec // path from runner script env
			t.Errorf("write log to %s: %v", logPath, err)
		}
	}
	return report.AllPass
}

// parseAuthorizedKeyFile reads an ssh-keygen .pub file and returns its public
// key, so the fixture selects the exact key the runner script pre-loaded into
// the system agent.
func parseAuthorizedKeyFile(path string) (ssh.PublicKey, error) {
	b, err := os.ReadFile(path) //nolint:gosec // path from runner script env
	if err != nil {
		return nil, err
	}
	key, _, _, _, err := ssh.ParseAuthorizedKey(b)
	if err != nil {
		return nil, err
	}
	return key, nil
}

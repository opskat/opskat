// Package realfixture is the real cross-platform SSH agent fixture harness.
//
// It drives internal/sshagent through REAL OS transports — a system ssh-agent
// over a unix socket on macOS/Linux (with a self-served Go agent as a
// documented fallback), and a Go-served OpenSSH-compatible named pipe on
// Windows — covering the five spec scenarios: native success, identity
// missing, provider rejects signing, cancel while waiting on the agent, and
// agent + MFA. The harness is CI-only by default: nothing runs unless
// OPSKAT_REAL_AGENT_FIXTURE=1 is set, and the Windows path is additionally
// build-tagged and run under the race detector in CI.
package realfixture

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/opskat/opskat/internal/sshagent"
)

// Result is the machine-readable outcome of one fixture scenario. It carries
// only codes and safe detail strings; it never carries endpoint values,
// public key blobs, signatures or challenge answers.
type Result struct {
	Name      string `json:"name"`
	Expected  string `json:"expected"`
	Got       string `json:"got"`
	Pass      bool   `json:"pass"`
	Used      bool   `json:"used,omitempty"`
	SignCount int    `json:"sign_count,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// Report is the machine-readable result of a whole fixture run. Its JSON form
// is the artifact that CI and the runner script consume.
type Report struct {
	Platform    string   `json:"platform"`
	SocketKind  string   `json:"socket_kind"`
	AgentSource string   `json:"agent_source"`
	Scenarios   []Result `json:"scenarios"`
	AllPass     bool     `json:"all_pass"`
	Sanitized   bool     `json:"sanitized"`
}

// Config carries the transport- and platform-specific pieces of a run. The
// factories come from the unix or windows fixture file; the scenarios
// themselves are platform-agnostic.
type Config struct {
	Platform    string
	SocketKind  string
	AgentSource string
	// SelectedPub is the public key loaded into the "real" agent (system
	// ssh-agent or the self-served keyring). The fixture never records its
	// blob, only its canonical fingerprint.
	SelectedPub ssh.PublicKey
	// Real returns the agent source used by the real-agent scenarios (native
	// success, identity missing, agent + MFA).
	Real func() (sshagent.Source, error)
	// Rejecting serves a provider that lists pub but refuses to sign.
	Rejecting func(pub ssh.PublicKey) (sshagent.Source, func(), error)
	// Delayed serves a provider that never answers a request. The ready
	// channel closes once a request is in flight (i.e. the client is blocked
	// waiting), which is when the scenario cancels.
	Delayed func() (sshagent.Source, <-chan struct{}, func(), error)
	// MFAAnswer is the challenge answer the scenario's interactive caller
	// returns; it doubles as a secret the sanitizer must never see in
	// artifacts.
	MFAAnswer string
}

// Run executes the five scenarios against the transport described by cfg and
// returns the machine-readable report plus the free-form run log. The report's
// JSON form is secret-free; the log is scanned by the caller before being
// written as an artifact.
func Run(ctx context.Context, cfg Config) (Report, string) {
	var log strings.Builder
	report := Report{
		Platform:    cfg.Platform,
		SocketKind:  cfg.SocketKind,
		AgentSource: cfg.AgentSource,
	}
	if cfg.MFAAnswer == "" {
		cfg.MFAAnswer = "123456"
	}

	scenarios := []struct {
		name string
		fn   func(context.Context, Config, *strings.Builder) Result
	}{
		{"native_success", scenarioNativeSuccess},
		{"identity_missing", scenarioIdentityMissing},
		{"provider_rejects_signing", scenarioReject},
		{"cancel_while_waiting", scenarioCancel},
		{"agent_mfa", scenarioMFA},
	}
	allPass := true
	for _, sc := range scenarios {
		res := sc.fn(ctx, cfg, &log)
		fmt.Fprintf(&log, "scenario %-24s expected=%-28s got=%-28s pass=%v\n", res.Name, res.Expected, res.Got, res.Pass)
		report.Scenarios = append(report.Scenarios, res)
		if !res.Pass {
			allPass = false
		}
	}
	report.AllPass = allPass
	return report, log.String()
}

// WriteReport writes the machine-readable report to path. It never contains
// private keys, signatures or challenge answers.
func WriteReport(path string, r Report) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600) //nolint:gosec // path comes from the runner script env
}

// ReportJSON returns the pretty JSON form of the report, used by the sanitizer
// and by the runner script.
func ReportJSON(r Report) string {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	return string(b)
}

// errCodeOf maps an error to the machine-readable "got" value: the typed
// sshagent code when present, "ok" for a nil error, otherwise a generic marker
// (the raw error text stays in the run log, never in the report).
func errCodeOf(err error) string {
	if err == nil {
		return "ok"
	}
	if code, ok := sshagent.CodeOf(err); ok {
		return code
	}
	return "unexpected_error"
}

// openAndSelect opens the agent source and performs precise signer selection.
// On any failure the transport is closed (the package already closes it on
// selection failure; Close is idempotent).
func openAndSelect(ctx context.Context, src sshagent.Source, fingerprint string) (*sshagent.Agent, *sshagent.AgentAuth, error) {
	ag, err := sshagent.Open(ctx, src)
	if err != nil {
		return nil, nil, err
	}
	aa, err := ag.AuthMethod(ctx, fingerprint)
	if err != nil {
		_ = ag.Close()
		return nil, nil, err
	}
	return ag, aa, nil
}

// acceptOnly returns a PublicKeyCallback that accepts exactly the selected
// fingerprint and rejects any other key, so the scenario observes what the
// precise signer selection actually offers.
func acceptOnly(fp string) func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
	return func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if sshagent.FingerprintSHA256(key) == fp {
			return nil, nil
		}
		return nil, fmt.Errorf("unexpected key offered")
	}
}

// scenarioNativeSuccess opens the real agent, selects the loaded key, and
// completes a full handshake against a controllable SSH server. It passes only
// if the server saw exactly the selected key and the provider actually signed.
func scenarioNativeSuccess(ctx context.Context, cfg Config, log *strings.Builder) Result {
	res := Result{Name: "native_success", Expected: "ok"}
	src, err := cfg.Real()
	if err != nil {
		res.Got = errCodeOf(err)
		return res
	}
	fp := sshagent.FingerprintSHA256(cfg.SelectedPub)
	ag, aa, err := openAndSelect(ctx, src, fp)
	if err != nil {
		res.Got = errCodeOf(err)
		return res
	}
	defer func() { _ = ag.Close() }()

	srv, err := newSSHServer(&ssh.ServerConfig{PublicKeyCallback: acceptOnly(fp)})
	if err != nil {
		res.Got = "setup_error"
		return res
	}
	defer func() { _ = srv.Close() }()

	client, err := ag.Dial(ctx, srv.addr, fixtureClientConfig("alice", srv.hostKey), aa)
	if err != nil {
		res.Got = errCodeOf(err)
		return res
	}
	defer func() { _ = client.Close() }()

	offered := srv.offeredFingerprints()
	fmt.Fprintf(log, "  native_success: offered=%v signer_used=%v sign_count=%d\n", offered, aa.Used(), aa.SignCount())
	res.Got = "ok"
	res.Used = aa.Used()
	res.SignCount = aa.SignCount()
	res.Detail = fmt.Sprintf("offered=%v", offered)
	res.Pass = aa.Used() && aa.SignCount() > 0 && len(offered) == 1 && offered[0] == fp
	return res
}

// scenarioIdentityMissing selects a fingerprint that the real agent does not
// hold; the handshake must fail before any dial with ssh_agent_identity_missing
// and never fall back to another key.
func scenarioIdentityMissing(ctx context.Context, cfg Config, log *strings.Builder) Result {
	res := Result{Name: "identity_missing", Expected: sshagent.CodeIdentityMissing}
	src, err := cfg.Real()
	if err != nil {
		res.Got = errCodeOf(err)
		return res
	}
	// A freshly generated key that is never loaded into the real agent.
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		res.Got = "setup_error"
		return res
	}
	pub, err := ssh.NewPublicKey(priv.Public())
	if err != nil {
		res.Got = "setup_error"
		return res
	}
	ag, err := sshagent.Open(ctx, src)
	if err != nil {
		res.Got = errCodeOf(err)
		return res
	}
	defer func() { _ = ag.Close() }()
	_, err = ag.AuthMethod(ctx, sshagent.FingerprintSHA256(pub))
	res.Got = errCodeOf(err)
	res.Pass = res.Got == res.Expected
	return res
}

// scenarioReject drives a real transport against a provider that lists a
// valid identity but refuses every sign request; the handshake must surface
// ssh_agent_sign_failed and never count a signature as used.
func scenarioReject(ctx context.Context, cfg Config, log *strings.Builder) Result {
	res := Result{Name: "provider_rejects_signing", Expected: sshagent.CodeSignFailed}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		res.Got = "setup_error"
		return res
	}
	pub, err := ssh.NewPublicKey(priv.Public())
	if err != nil {
		res.Got = "setup_error"
		return res
	}
	src, cleanup, err := cfg.Rejecting(pub)
	if err != nil {
		res.Got = "setup_error"
		return res
	}
	if cleanup != nil {
		defer cleanup()
	}
	fp := sshagent.FingerprintSHA256(pub)
	ag, aa, err := openAndSelect(ctx, src, fp)
	if err != nil {
		res.Got = errCodeOf(err)
		return res
	}
	defer func() { _ = ag.Close() }()

	srv, err := newSSHServer(&ssh.ServerConfig{PublicKeyCallback: acceptOnly(fp)})
	if err != nil {
		res.Got = "setup_error"
		return res
	}
	defer func() { _ = srv.Close() }()

	_, err = ag.Dial(ctx, srv.addr, fixtureClientConfig("alice", srv.hostKey), aa)
	res.Got = errCodeOf(err)
	res.Used = aa.Used()
	res.Pass = res.Got == res.Expected && !aa.Used()
	return res
}

// scenarioCancel opens a provider that never answers, blocks in a listing, and
// cancels the context; the wait must stop with the sshagent cancel code and
// the transport must be released.
func scenarioCancel(ctx context.Context, cfg Config, log *strings.Builder) Result {
	res := Result{Name: "cancel_while_waiting", Expected: sshagent.CodeCancelled}
	src, ready, cleanup, err := cfg.Delayed()
	if err != nil {
		res.Got = "setup_error"
		return res
	}
	if cleanup != nil {
		defer cleanup()
	}

	cctx, cancel := context.WithCancel(ctx)
	defer cancel()

	ag, err := sshagent.Open(cctx, src)
	if err != nil {
		res.Got = errCodeOf(err)
		return res
	}
	defer func() { _ = ag.Close() }()

	done := make(chan error, 1)
	go func() {
		// The fingerprint never matches because the provider never answers;
		// the point is that the listing wait is canceled, not selection.
		_, err := ag.AuthMethod(cctx, "SHA256:0000000000000000000000000000000000000000000000000000000000000000")
		done <- err
	}()

	// Cancel only once the request is actually in flight, so the scenario
	// proves a canceled wait, not a canceled dial.
	select {
	case <-ready:
	case <-cctx.Done():
	case <-time.After(10 * time.Second):
		res.Got = "setup_timeout"
		return res
	}
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		res.Got = errCodeOf(err)
	case <-time.After(10 * time.Second):
		res.Got = "timeout"
	}
	res.Pass = res.Got == res.Expected
	return res
}

// answerCaller is the interactive caller for the MFA scenario. It records the
// structured challenge (without retaining answers) and returns the configured
// answers.
type answerCaller struct {
	mu      sync.Mutex
	challs  []sshagent.MFAChallenge
	answers []string
}

func (c *answerCaller) SubmitChallenge(_ context.Context, ch sshagent.MFAChallenge) ([]string, error) {
	c.mu.Lock()
	c.challs = append(c.challs, ch)
	ans := append([]string(nil), c.answers...)
	c.mu.Unlock()
	return ans, nil
}

func (c *answerCaller) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.challs)
}

func (c *answerCaller) first() (sshagent.MFAChallenge, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.challs) == 0 {
		return sshagent.MFAChallenge{}, false
	}
	return c.challs[0], true
}

// scenarioMFA completes the real-agent handshake against a server that accepts
// the selected public key as partial success and then requires exactly one
// keyboard-interactive round; the interactive caller's answer completes the
// connection.
func scenarioMFA(ctx context.Context, cfg Config, log *strings.Builder) Result {
	res := Result{Name: "agent_mfa", Expected: "ok"}
	src, err := cfg.Real()
	if err != nil {
		res.Got = errCodeOf(err)
		return res
	}
	fp := sshagent.FingerprintSHA256(cfg.SelectedPub)
	ag, aa, err := openAndSelect(ctx, src, fp)
	if err != nil {
		res.Got = errCodeOf(err)
		return res
	}
	defer func() { _ = ag.Close() }()

	answer := cfg.MFAAnswer
	srv, err := newSSHServer(&ssh.ServerConfig{
		PublicKeyCallback: func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if sshagent.FingerprintSHA256(key) != fp {
				return nil, fmt.Errorf("unexpected key offered")
			}
			return nil, &ssh.PartialSuccessError{Next: ssh.ServerAuthCallbacks{
				KeyboardInteractiveCallback: func(c ssh.ConnMetadata, client ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
					answers, err := client("Verification", "Enter code", []string{"Code: ", "Second: "}, []bool{false, false})
					if err != nil {
						return nil, err
					}
					if len(answers) != 2 || answers[0] != answer || answers[1] != "ok" {
						return nil, fmt.Errorf("bad answers")
					}
					return nil, nil
				},
			}}
		},
		KeyboardInteractiveCallback: func(c ssh.ConnMetadata, client ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
			answers, err := client("Verification", "Enter code", []string{"Code: ", "Second: "}, []bool{false, false})
			if err != nil {
				return nil, err
			}
			if len(answers) != 2 || answers[0] != answer || answers[1] != "ok" {
				return nil, fmt.Errorf("bad answers")
			}
			return nil, nil
		},
	})
	if err != nil {
		res.Got = "setup_error"
		return res
	}
	defer func() { _ = srv.Close() }()

	caller := &answerCaller{answers: []string{answer, "ok"}}
	client, err := ag.Dial(ctx, srv.addr, fixtureClientConfig("alice", srv.hostKey), aa, sshagent.DialOptions{MFA: caller})
	if err != nil {
		res.Got = errCodeOf(err)
		return res
	}
	defer func() { _ = client.Close() }()

	ch, got := caller.first()
	res.Got = "ok"
	res.Used = aa.Used()
	res.SignCount = aa.SignCount()
	if got {
		res.Detail = fmt.Sprintf("prompts=%d name=%q", len(ch.Prompts), ch.Name)
	}
	fmt.Fprintf(log, "  agent_mfa: challenges=%d prompts=%v signer_used=%v\n", caller.count(), ch.Prompts, aa.Used())
	res.Pass = aa.Used() && caller.count() == 1 && got && len(ch.Prompts) == 2 && ch.Name == "Verification"
	return res
}

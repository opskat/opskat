package command

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/approval"
)

// storeDesktop replaces both socket round trips, so the tests observe what opsctl
// decided to ask the desktop app for — not whether one happens to be running.
type storeDesktop struct {
	entries   []approval.ExtStoreEntry
	searchErr error
	langs     []string
	installs  []string
	answer    func(name string) (approval.ApprovalResponse, error)
}

func stubStoreDesktop(t *testing.T, d *storeDesktop) *storeDesktop {
	t.Helper()
	origSearch, origInstall := storeSearchFn, storeInstallFn
	storeSearchFn = func(lang string) ([]approval.ExtStoreEntry, error) {
		d.langs = append(d.langs, lang)
		return d.entries, d.searchErr
	}
	storeInstallFn = func(name string) (approval.ApprovalResponse, error) {
		d.installs = append(d.installs, name)
		if d.answer == nil {
			return approval.ApprovalResponse{Approved: true, Extension: name, Version: "9.9.9"}, nil
		}
		return d.answer(name)
	}
	t.Cleanup(func() { storeSearchFn, storeInstallFn = origSearch, origInstall })
	return d
}

// runCaptured runs f and returns its exit code, stdout and stderr.
func runCaptured(t *testing.T, f func() int) (int, string, string) {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	orig := os.Stdout
	os.Stdout = w
	var code int
	stderr := captureStderr(t, func() { code = f() })
	os.Stdout = orig
	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return code, string(out), stderr
}

func sampleStore() []approval.ExtStoreEntry {
	return []approval.ExtStoreEntry{
		{Name: "es", DisplayName: "Elasticsearch", Description: "Browse indices", Latest: "0.2.0", Status: approval.ExtStoreStatusInstall},
		{Name: "kafka", DisplayName: "Kafka Tools", Latest: "0.4.1", Installed: "0.1.0", Status: approval.ExtStoreStatusUpdate},
		{Name: "notebook", DisplayName: "Notebook", Latest: "1.0.0", Installed: "1.2.0", Status: approval.ExtStoreStatusInstalled},
		{Name: "pgdoctor", DisplayName: "PG Doctor", Status: approval.ExtStoreStatusUnavailable,
			Reason: "needs hostABI 9.1; update OpsKat"},
		{Name: "redis-x", DisplayName: "Redis X", Latest: "2.0.0", Installed: "1.0.0", Status: approval.ExtStoreStatusUpdate},
	}
}

func zhCtx() context.Context { return aictx.WithPolicyLang(context.Background(), "zh-cn") }

// --- search --------------------------------------------------------------------

func TestExtSearchListsStatusWithoutInstalling(t *testing.T) {
	d := stubStoreDesktop(t, &storeDesktop{entries: sampleStore()})

	code, out, _ := runCaptured(t, func() int { return cmdExt(context.Background(), []string{"search"}) })

	require.Equal(t, 0, code)
	assert.Empty(t, d.installs, "search never installs or asks for a confirm")
	assert.Equal(t, []string{"en"}, d.langs)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, lines, 6)
	assert.Regexp(t, `^NAME\s+DISPLAY NAME\s+LATEST\s+INSTALLED\s+STATUS$`, lines[0])
	assert.Regexp(t, `^es\s+Elasticsearch\s+0\.2\.0\s+-\s+not installed$`, lines[1])
	assert.Regexp(t, `^kafka\s+Kafka Tools\s+0\.4\.1\s+0\.1\.0\s+update available$`, lines[2])
	assert.Regexp(t, `^notebook\s+Notebook\s+1\.0\.0\s+1\.2\.0\s+installed$`, lines[3])
	assert.Regexp(t, `^pgdoctor\s+PG Doctor\s+-\s+-\s+incompatible: needs hostABI 9\.1; update OpsKat$`, lines[4])
}

func TestExtSearchFollowsTheLocale(t *testing.T) {
	d := stubStoreDesktop(t, &storeDesktop{entries: sampleStore()})

	code, out, _ := runCaptured(t, func() int { return cmdExt(zhCtx(), []string{"search"}) })

	require.Equal(t, 0, code)
	assert.Equal(t, []string{"zh-CN"}, d.langs, "display names come back in the CLI's language")
	for _, want := range []string{"名称", "未安装", "可更新", "已安装", "不兼容"} {
		assert.Contains(t, out, want)
	}
}

func TestExtSearchFiltersByKeyword(t *testing.T) {
	stubStoreDesktop(t, &storeDesktop{entries: sampleStore()})

	t.Run("name, display name or description, case-insensitively", func(t *testing.T) {
		for kw, want := range map[string]string{"KAF": "kafka", "doctor": "pgdoctor", "indices": "es"} {
			code, out, _ := runCaptured(t, func() int { return cmdExt(context.Background(), []string{"search", kw}) })
			require.Equal(t, 0, code)
			lines := strings.Split(strings.TrimSpace(out), "\n")
			require.Len(t, lines, 2, kw)
			assert.True(t, strings.HasPrefix(lines[1], want+" "), "%s → %s", kw, lines[1])
		}
	})

	t.Run("no match says so", func(t *testing.T) {
		code, out, _ := runCaptured(t, func() int { return cmdExt(context.Background(), []string{"search", "zzz"}) })
		require.Equal(t, 0, code)
		assert.Contains(t, out, "No extensions")
		assert.NotContains(t, out, "NAME")
	})
}

func TestExtSearchFailsWhenTheDesktopCannotAnswer(t *testing.T) {
	stubStoreDesktop(t, &storeDesktop{searchErr: errors.New("cannot connect to desktop app (is it running?)")})

	code, out, stderr := runCaptured(t, func() int { return cmdExt(context.Background(), []string{"search"}) })

	assert.Equal(t, 1, code)
	assert.Empty(t, out)
	assert.Contains(t, stderr, "is it running?")
}

// --- install -------------------------------------------------------------------

func TestExtInstallInstallsTheOfferThroughTheDesktop(t *testing.T) {
	d := stubStoreDesktop(t, &storeDesktop{entries: sampleStore(), answer: func(string) (approval.ApprovalResponse, error) {
		return approval.ApprovalResponse{Approved: true, Extension: "es", Version: "0.2.0"}, nil
	}})

	code, out, _ := runCaptured(t, func() int { return cmdExt(context.Background(), []string{"install", "es"}) })

	require.Equal(t, 0, code)
	assert.Equal(t, []string{"es"}, d.installs)
	assert.Contains(t, out, "es 0.2.0")
}

func TestExtInstallSameOrHigherIsNotReinstalled(t *testing.T) {
	d := stubStoreDesktop(t, &storeDesktop{entries: sampleStore()})

	code, out, _ := runCaptured(t, func() int { return cmdExt(context.Background(), []string{"install", "notebook"}) })

	require.Equal(t, 0, code)
	assert.Empty(t, d.installs, "nothing is sent to the desktop, so no confirm is shown")
	assert.Contains(t, out, "1.2.0")
	assert.Contains(t, out, "already installed")
}

func TestExtInstallFailures(t *testing.T) {
	cases := []struct {
		name     string
		target   string
		answer   approval.ApprovalResponse
		err      error
		contains string
		sent     bool
	}{
		{name: "declined in the app", target: "es", sent: true, contains: "declined",
			answer: approval.ApprovalResponse{ErrorKind: approval.ExtStoreInstallCanceled, Reason: "the install was declined in the app"}},
		{name: "verification failure", target: "es", sent: true, contains: "expected aaa, got bbb",
			answer: approval.ApprovalResponse{ErrorKind: "digest", Reason: "digest mismatch: expected aaa, got bbb"}},
		{name: "desktop gone mid-request", target: "es", sent: true, contains: "is it running?",
			err: errors.New("cannot connect to desktop app (is it running?)")},
		{name: "incompatible", target: "pgdoctor", contains: "needs hostABI 9.1"},
		{name: "not in the index", target: "nope", contains: `"nope"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := stubStoreDesktop(t, &storeDesktop{entries: sampleStore(), answer: func(string) (approval.ApprovalResponse, error) {
				return c.answer, c.err
			}})

			code, out, stderr := runCaptured(t, func() int { return cmdExt(context.Background(), []string{"install", c.target}) })

			assert.Equal(t, 1, code)
			assert.Empty(t, out)
			assert.Contains(t, stderr, c.contains)
			assert.Equal(t, c.sent, len(d.installs) == 1)
		})
	}
}

func TestExtInstallRaceWithAnotherInstallIsNotAFailure(t *testing.T) {
	stubStoreDesktop(t, &storeDesktop{entries: sampleStore(), answer: func(string) (approval.ApprovalResponse, error) {
		return approval.ApprovalResponse{ErrorKind: approval.ExtStoreInstallUpToDate,
			Reason: `extension "es" 0.2.0 is installed and the store offers nothing newer`}, nil
	}})

	code, out, _ := runCaptured(t, func() int { return cmdExt(context.Background(), []string{"install", "es"}) })

	assert.Equal(t, 0, code)
	assert.Contains(t, out, "nothing newer")
}

func TestExtInstallNeedsExactlyOneName(t *testing.T) {
	d := stubStoreDesktop(t, &storeDesktop{entries: sampleStore()})

	assert.Equal(t, 1, cmdExt(context.Background(), []string{"install"}))
	assert.Equal(t, 1, cmdExt(context.Background(), []string{"install", "es", "kafka"}))
	assert.Empty(t, d.langs)
	assert.Empty(t, d.installs)
}

// --- update --------------------------------------------------------------------

func TestExtUpdateOne(t *testing.T) {
	t.Run("an available update goes through the desktop", func(t *testing.T) {
		d := stubStoreDesktop(t, &storeDesktop{entries: sampleStore()})
		code, out, _ := runCaptured(t, func() int { return cmdExt(context.Background(), []string{"update", "kafka"}) })
		require.Equal(t, 0, code)
		assert.Equal(t, []string{"kafka"}, d.installs)
		assert.Contains(t, out, "kafka")
	})

	t.Run("nothing newer says so and sends nothing", func(t *testing.T) {
		d := stubStoreDesktop(t, &storeDesktop{entries: sampleStore()})
		code, out, _ := runCaptured(t, func() int { return cmdExt(context.Background(), []string{"update", "notebook"}) })
		require.Equal(t, 0, code)
		assert.Empty(t, d.installs)
		assert.Contains(t, out, "no update available")
	})

	t.Run("not installed is an error pointing at install", func(t *testing.T) {
		d := stubStoreDesktop(t, &storeDesktop{entries: sampleStore()})
		code, _, stderr := runCaptured(t, func() int { return cmdExt(context.Background(), []string{"update", "es"}) })
		assert.Equal(t, 1, code)
		assert.Empty(t, d.installs)
		assert.Contains(t, stderr, "opsctl ext install es")
	})

	t.Run("declined is non-zero", func(t *testing.T) {
		stubStoreDesktop(t, &storeDesktop{entries: sampleStore(), answer: func(string) (approval.ApprovalResponse, error) {
			return approval.ApprovalResponse{ErrorKind: approval.ExtStoreInstallCanceled, Reason: "declined"}, nil
		}})
		code, _, stderr := runCaptured(t, func() int { return cmdExt(context.Background(), []string{"update", "kafka"}) })
		assert.Equal(t, 1, code)
		assert.Contains(t, stderr, "declined")
	})
}

func TestExtUpdateAll(t *testing.T) {
	t.Run("each updatable extension is confirmed separately", func(t *testing.T) {
		d := stubStoreDesktop(t, &storeDesktop{entries: sampleStore()})
		code, out, _ := runCaptured(t, func() int { return cmdExt(context.Background(), []string{"update", "--all"}) })
		require.Equal(t, 0, code)
		assert.Equal(t, []string{"kafka", "redis-x"}, d.installs, "one desktop request, so one confirm, per extension")
		assert.Contains(t, out, "kafka")
		assert.Contains(t, out, "redis-x")
	})

	t.Run("a declined one does not stop the rest, but the run fails", func(t *testing.T) {
		d := stubStoreDesktop(t, &storeDesktop{entries: sampleStore(), answer: func(name string) (approval.ApprovalResponse, error) {
			if name == "kafka" {
				return approval.ApprovalResponse{ErrorKind: approval.ExtStoreInstallCanceled, Reason: "declined"}, nil
			}
			return approval.ApprovalResponse{Approved: true, Extension: name, Version: "2.0.0"}, nil
		}})
		code, out, stderr := runCaptured(t, func() int { return cmdExt(context.Background(), []string{"update", "--all"}) })
		assert.Equal(t, 1, code)
		assert.Equal(t, []string{"kafka", "redis-x"}, d.installs)
		assert.Contains(t, stderr, "kafka")
		assert.Contains(t, out, "redis-x 2.0.0")
	})

	t.Run("nothing to update says so", func(t *testing.T) {
		d := stubStoreDesktop(t, &storeDesktop{entries: sampleStore()[:1]})
		code, out, _ := runCaptured(t, func() int { return cmdExt(context.Background(), []string{"update", "--all"}) })
		require.Equal(t, 0, code)
		assert.Empty(t, d.installs)
		assert.Contains(t, out, "No updates available")
	})
}

func TestExtUpdateNeedsANameOrAll(t *testing.T) {
	d := stubStoreDesktop(t, &storeDesktop{entries: sampleStore()})

	assert.Equal(t, 1, cmdExt(context.Background(), []string{"update"}))
	assert.Equal(t, 1, cmdExt(context.Background(), []string{"update", "--all", "kafka"}))
	assert.Empty(t, d.langs)
}

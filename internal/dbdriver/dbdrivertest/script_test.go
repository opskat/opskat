package dbdrivertest

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

func withRunSQLFile() *Fake {
	fake := NewFake()
	fake.Desc.Capabilities = append(fake.Desc.Capabilities, dbdriver.CapRunSQLFile)
	return fake
}

func openScriptFake(t *testing.T, fake *Fake) dbdriver.Session {
	t.Helper()
	session, err := fake.Open(context.Background(), dbdriver.OpenRequest{Config: &dbdriver.Config{Driver: "fakedb", Host: "h"}})
	require.NoError(t, err)
	return session
}

func TestFakeDriverRunningSQLFilesPassesConformance(t *testing.T) {
	fake := withRunSQLFile()
	Register(t, fake)
	RunConformance(t, fake, FakeSessions(fake))
}

// 没声明运行 SQL 文件的假驱动：一条语句也不执行，以 ErrUnsupported 拒绝。
func TestFakeSessionRefusesAnUndeclaredScript(t *testing.T) {
	fake := NewFake()
	session := openScriptFake(t, fake).(*FakeSession)
	script := "DELETE FROM t;"
	_, err := session.RunScript(context.Background(), dbdriver.ScriptRequest{Script: strings.NewReader(script), Size: int64(len(script))})
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	assert.Empty(t, session.Statements())
}

// lenientScriptSession 运行任何文件（什么也不执行），不管驱动声明了什么。
type lenientScriptSession struct{ dbdriver.Session }

func (lenientScriptSession) RunScript(context.Context, dbdriver.ScriptRequest) (*dbdriver.ScriptResult, error) {
	return &dbdriver.ScriptResult{}, nil
}

// refusingScriptSession 拒绝运行任何文件。
type refusingScriptSession struct{ dbdriver.Session }

func (refusingScriptSession) RunScript(context.Context, dbdriver.ScriptRequest) (*dbdriver.ScriptResult, error) {
	return nil, fmt.Errorf("%w: no scripts", dbdriver.ErrUnsupported)
}

// continuingScriptSession 忽略「不继续」：失败之后照样执行后面的语句。
type continuingScriptSession struct{ dbdriver.Session }

func (s continuingScriptSession) RunScript(ctx context.Context, req dbdriver.ScriptRequest) (*dbdriver.ScriptResult, error) {
	req.ContinueOnError = true
	return s.Session.RunScript(ctx, req)
}

// 一致性测试也得能失败：运行 SQL 文件与声明不符、或没在第一条失败的语句处停下，都要点出来。
func TestConformanceChecksRunScriptAgainstTheDeclaration(t *testing.T) {
	undeclared := NewFake()
	problems := sessionProblems(undeclared.Desc, lenientScriptSession{openScriptFake(t, undeclared)})
	assert.Contains(t, strings.Join(problems, "\n"), "RunScript")

	declared := withRunSQLFile()
	problems = sessionProblems(declared.Desc, refusingScriptSession{openScriptFake(t, declared)})
	assert.Contains(t, strings.Join(problems, "\n"), "RunScript")

	problems = sessionProblems(declared.Desc, continuingScriptSession{openScriptFake(t, declared)})
	assert.Contains(t, strings.Join(problems, "\n"), "RunScript")

	assert.Empty(t, sessionProblems(declared.Desc, openScriptFake(t, declared)))
}

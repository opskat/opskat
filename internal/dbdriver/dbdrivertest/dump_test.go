package dbdrivertest

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

func withDumpSQL() *Fake {
	fake := NewFake()
	fake.Desc.Capabilities = append(fake.Desc.Capabilities, dbdriver.CapViewDDL, dbdriver.CapExportSQL, dbdriver.CapDumpSQL)
	return fake
}

func TestFakeDriverDumpingSQLFilesPassesConformance(t *testing.T) {
	fake := withDumpSQL()
	Register(t, fake)
	RunConformance(t, fake, FakeSessions(fake))
	RunTableConformance(t, fake, FakeSessions(fake), "app", "users")
}

// 没声明转储的假驱动：什么也不读、不写，以 ErrUnsupported 拒绝。
func TestFakeSessionRefusesAnUndeclaredDump(t *testing.T) {
	fake := NewFake()
	fake.Desc.Capabilities = append(fake.Desc.Capabilities, dbdriver.CapViewDDL, dbdriver.CapExportSQL)
	session := openScriptFake(t, fake).(*FakeSession)
	var out bytes.Buffer

	_, err := session.Dump(context.Background(), dbdriver.DumpRequest{Database: "app", Tables: []string{"users"}, Writer: &out})
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	assert.Empty(t, out.String())
	assert.Empty(t, session.TableCalls())
}

// 声明了转储的假驱动经编排写出转储：定义来自会话的 TableDDL，行来自它的游标与 writer。
func TestFakeSessionDumpsThroughTheOrchestration(t *testing.T) {
	fake := withDumpSQL()
	session := openScriptFake(t, fake).(*FakeSession)
	var out bytes.Buffer

	result, err := session.Dump(context.Background(), dbdriver.DumpRequest{
		Database: "app", Tables: []string{"users"}, WithData: true, Writer: &out,
	})
	require.NoError(t, err)
	assert.Equal(t, dbdriver.DumpResult{Objects: 1, Rows: 3, Bytes: int64(out.Len())}, *result)
	assert.Contains(t, out.String(), "FAKE DUMP BEGIN;\n")
	assert.Contains(t, out.String(), "FAKE DROP table users;\nCREATE TABLE users (id int, name text);\n")
	assert.Contains(t, out.String(), "FAKE INSERT ROW 3 OF app.users\n")
	assert.Empty(t, session.Statements())
}

// lenientDumpSession 写出任何转储（什么也不写），不管驱动声明了什么。
type lenientDumpSession struct{ dbdriver.Session }

func (lenientDumpSession) Dump(context.Context, dbdriver.DumpRequest) (*dbdriver.DumpResult, error) {
	return &dbdriver.DumpResult{}, nil
}

// refusingDumpSession 拒绝任何转储。
type refusingDumpSession struct{ dbdriver.Session }

func (refusingDumpSession) Dump(context.Context, dbdriver.DumpRequest) (*dbdriver.DumpResult, error) {
	return nil, fmt.Errorf("%w: no dumps", dbdriver.ErrUnsupported)
}

// 一致性测试也得能失败：转储与声明不符、声明了却不与 CapViewDDL 一起、或写出的转储与表不符，都要点出来。
func TestConformanceChecksDumpAgainstTheDeclaration(t *testing.T) {
	undeclared := NewFake()
	problems := sessionProblems(undeclared.Desc, lenientDumpSession{openScriptFake(t, undeclared)})
	assert.Contains(t, strings.Join(problems, "\n"), "Dump")

	declared := withDumpSQL()
	problems = sessionProblems(declared.Desc, refusingDumpSession{openScriptFake(t, declared)})
	assert.Contains(t, strings.Join(problems, "\n"), "Dump")
	assert.Empty(t, sessionProblems(declared.Desc, openScriptFake(t, declared)))

	problems = tableProblems(declared.Desc, lenientDumpSession{openScriptFake(t, declared)}, "app", "users")
	assert.Contains(t, strings.Join(problems, "\n"), "Dump")
	assert.Empty(t, tableProblems(declared.Desc, openScriptFake(t, declared), "app", "users"))

	withoutDDL := withDumpSQL()
	withoutDDL.Desc.Capabilities = slices.DeleteFunc(withoutDDL.Desc.Capabilities, func(c dbdriver.Capability) bool { return c == dbdriver.CapViewDDL })
	assert.Empty(t, descriptorProblems(declared.Desc))
	assert.Contains(t, strings.Join(descriptorProblems(withoutDDL.Desc), "\n"), string(dbdriver.CapViewDDL))
}

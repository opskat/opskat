package command

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/mcp"
)

func TestCmdMCPExternalModeRequiresSecret(t *testing.T) {
	t.Setenv(mcpApprovalSecretEnv, "")
	origSessionDir := sessionDataDir
	sessionDataDir = t.TempDir
	t.Cleanup(func() { sessionDataDir = origSessionDir })

	stderr := captureStderr(t, func() {
		code := cmdMCP(context.Background(), []string{"--approval-mode=external"}, "test-session-1234")
		require.Equal(t, 1, code)
	})
	require.Contains(t, stderr, mcpApprovalSecretEnv)
}

func TestCmdMCPRejectsInvalidFlags(t *testing.T) {
	stderr := captureStderr(t, func() {
		code := cmdMCP(context.Background(), []string{"--approval-mode=bogus"}, "test-session-1234")
		require.Equal(t, 1, code)
	})
	require.Contains(t, stderr, "invalid --approval-mode")

	stderr2 := captureStderr(t, func() {
		code := cmdMCP(context.Background(), []string{"--toolset=bogus"}, "test-session-1234")
		require.Equal(t, 1, code)
	})
	require.Contains(t, stderr2, "invalid --toolset")
}

func TestCmdMCPExternalToolsetListAndAppOnlyResolve(t *testing.T) {
	t.Setenv(mcpApprovalSecretEnv, "cli-test-secret")
	origSessionDir := sessionDataDir
	sessionDataDir = t.TempDir
	t.Cleanup(func() { sessionDataDir = origSessionDir })

	clientR, serverW := io.Pipe()
	serverR, clientW := io.Pipe()
	origStdio := mcpStdio
	origStdout := mcpStdout
	mcpStdio = func() (io.Reader, io.Writer) { return serverR, io.Discard }
	mcpStdout = func() io.Writer { return serverW }
	t.Cleanup(func() {
		mcpStdio = origStdio
		mcpStdout = origStdout
	})

	done := make(chan int, 1)
	go func() {
		done <- cmdMCP(context.Background(), []string{"--approval-mode=external", "--toolset=core"}, "test-session-1234")
	}()

	reader := bufio.NewReader(clientR)
	send := func(id int, method string, params any) mcp.Response {
		t.Helper()
		rawID, err := json.Marshal(id)
		require.NoError(t, err)
		var rawParams json.RawMessage
		if params != nil {
			rawParams, err = json.Marshal(params)
			require.NoError(t, err)
		}
		b, err := json.Marshal(mcp.Request{JSONRPC: "2.0", ID: rawID, Method: method, Params: rawParams})
		require.NoError(t, err)
		_, err = clientW.Write(append(b, '\n'))
		require.NoError(t, err)
		line, err := reader.ReadBytes('\n')
		require.NoError(t, err)
		var resp mcp.Response
		require.NoError(t, json.Unmarshal(line, &resp))
		return resp
	}

	initResp := send(1, "initialize", map[string]any{"protocolVersion": mcp.ProtocolVersion})
	require.Nil(t, initResp.Error)

	listResp := send(2, "tools/list", nil)
	require.Nil(t, listResp.Error)
	raw, err := json.Marshal(listResp.Result)
	require.NoError(t, err)
	var listRes struct {
		Tools []mcp.ToolSpec `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(raw, &listRes))

	modelVisibleCount := 0
	names := make(map[string]mcp.ToolSpec)
	for _, spec := range listRes.Tools {
		names[spec.Name] = spec
		ui, _ := spec.Meta["ui"].(map[string]any)
		vis, _ := ui["visibility"].([]any)
		if len(vis) == 1 && vis[0] == "app" {
			continue
		}
		modelVisibleCount++
	}
	require.LessOrEqual(t, modelVisibleCount, 10, "core toolset must stay <= 10 model-visible tools for Hob direct exposure")
	require.Contains(t, names, "exec")
	require.Contains(t, names, "batch_exec")
	require.Contains(t, names, "cp")
	require.Contains(t, names, "request_permission")
	require.Contains(t, names, "list_audit")
	require.Contains(t, names, mcp.GetTaskToolName)
	require.Contains(t, names, mcp.ResolveToolName)
	require.Equal(t, map[string]any{"ui": map[string]any{"visibility": []any{"app"}}}, names[mcp.ResolveToolName].Meta)

	_ = clientW.Close()
	_ = clientR.Close()
	select {
	case code := <-done:
		require.Equal(t, 0, code)
	case <-time.After(3 * time.Second):
		t.Fatal("cmdMCP did not exit after stdin closed")
	}
}

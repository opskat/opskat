package ai

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// LocalCLIProvider 本地 CLI provider（claude/codex）
type LocalCLIProvider struct {
	name    string
	cliPath string // CLI 可执行文件路径
	cliType string // "claude" 或 "codex"
}

// NewLocalCLIProvider 创建本地 CLI provider
func NewLocalCLIProvider(name, cliPath, cliType string) *LocalCLIProvider {
	return &LocalCLIProvider{
		name:    name,
		cliPath: cliPath,
		cliType: cliType,
	}
}

func (p *LocalCLIProvider) Name() string { return p.name }

func (p *LocalCLIProvider) Chat(ctx context.Context, messages []Message, _ []Tool) (<-chan StreamEvent, error) {
	// 将消息转换为单个 prompt
	prompt := messagesToPrompt(messages)

	var cmd *exec.Cmd
	switch p.cliType {
	case "claude":
		cmd = exec.CommandContext(ctx, p.cliPath, "--print", "--no-input", prompt)
	case "codex":
		cmd = exec.CommandContext(ctx, p.cliPath, "--quiet", prompt)
	default:
		return nil, fmt.Errorf("不支持的 CLI 类型: %s", p.cliType)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("获取 stdout 失败: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 CLI 失败: %w", err)
	}

	ch := make(chan StreamEvent, 32)
	go func() {
		defer close(ch)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			ch <- StreamEvent{Type: "content", Content: line + "\n"}
		}
		cmd.Wait()
		ch <- StreamEvent{Type: "done"}
	}()

	return ch, nil
}

// messagesToPrompt 将消息列表转换为文本 prompt
func messagesToPrompt(messages []Message) string {
	var parts []string
	for _, msg := range messages {
		switch msg.Role {
		case RoleSystem:
			parts = append(parts, "[System]\n"+msg.Content)
		case RoleUser:
			parts = append(parts, msg.Content)
		case RoleAssistant:
			parts = append(parts, "[Assistant]\n"+msg.Content)
		case RoleTool:
			parts = append(parts, "[Tool Result]\n"+msg.Content)
		}
	}
	return strings.Join(parts, "\n\n")
}

// DetectLocalCLIs 检测本地安装的 AI CLI 工具
func DetectLocalCLIs() []CLIInfo {
	var results []CLIInfo

	clis := []struct {
		name    string
		cliType string
		cmds    []string // 候选可执行文件名
	}{
		{"Claude Code", "claude", []string{"claude"}},
		{"Codex", "codex", []string{"codex"}},
	}

	for _, cli := range clis {
		for _, cmd := range cli.cmds {
			path, err := exec.LookPath(cmd)
			if err == nil {
				version := getCLIVersion(path)
				results = append(results, CLIInfo{
					Name:    cli.name,
					Type:    cli.cliType,
					Path:    path,
					Version: version,
				})
				break
			}
		}
	}

	return results
}

// CLIInfo 本地 CLI 信息
type CLIInfo struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Path    string `json:"path"`
	Version string `json:"version"`
}

func getCLIVersion(path string) string {
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		return "unknown"
	}
	version := strings.TrimSpace(string(out))
	// 取第一行
	if idx := strings.IndexByte(version, '\n'); idx > 0 {
		version = version[:idx]
	}
	return version
}

// CLIInfoJSON 序列化 CLIInfo 列表
func CLIInfoJSON(infos []CLIInfo) string {
	data, _ := json.Marshal(infos)
	return string(data)
}

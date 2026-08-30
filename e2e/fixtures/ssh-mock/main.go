// Minimal SSH server mock for the e2e harness — just enough for the app's SSH
// "Test Connection" (a TCP dial + SSH handshake, no command) to pass against a
// hermetic in-harness server. Pure stdlib + golang.org/x/crypto/ssh (already a
// project dep); started as a Playwright `webServer` (see playwright.config.ts)
// and dialed by the real app at 127.0.0.1:<port>. The SSH analog of
// fixtures/redis-mock.mjs.
//
// Why no auth / no host key dance is needed:
//   - NoClientAuth: true — x/crypto/ssh probes the "none" method first, which the
//     server accepts, so whatever credential the form sends is irrelevant (the
//     app's empty-password Test Connection still completes).
//   - the app's TestConnection binding uses AutoTrustFirstRejectChangeVerifyFunc,
//     so the random host key generated here is auto-trusted on first connect (no
//     UI prompt) against the fresh e2e DB.
package main

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
)

var commandLogPath string

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: ssh-mock <port>")
		os.Exit(1)
	}
	port, err := strconv.Atoi(os.Args[1])
	if err != nil || port <= 0 {
		fmt.Fprintf(os.Stderr, "ssh-mock: invalid port %q\n", os.Args[1])
		os.Exit(1)
	}
	if len(os.Args) >= 3 {
		candidate := filepath.Clean(os.Args[2])
		if filepath.Base(candidate) != "ssh-mock.commands" {
			fmt.Fprintln(os.Stderr, "ssh-mock: command log must be named ssh-mock.commands")
			os.Exit(1)
		}
		commandLogPath = candidate
		_ = os.Remove(commandLogPath)
	}

	signer, err := newSigner()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ssh-mock: host key: %v\n", err)
		os.Exit(1)
	}
	config := &ssh.ServerConfig{NoClientAuth: true}
	config.AddHostKey(signer)

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ssh-mock: listen %s: %v\n", addr, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "ssh-mock listening on %s\n", addr)

	for {
		conn, err := listener.Accept()
		if err != nil {
			continue // transient accept error (e.g. Playwright's TCP readiness probe)
		}
		go handleConn(conn, config)
	}
}

// handleConn completes the SSH server handshake, then services session channels
// (accept + exec/shell → exit-status 0). TestConnection only needs the handshake,
// but handling sessions keeps the mock reusable for specs that run a command. Any
// error just drops the connection — a real client wouldn't hit it.
func handleConn(conn net.Conn, config *ssh.ServerConfig) {
	defer func() { _ = conn.Close() }()
	serverConn, chans, reqs, err := ssh.NewServerConn(conn, config)
	if err != nil {
		return // handshake failed, or the readiness probe closed early
	}
	defer func() { _ = serverConn.Close() }()
	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "unsupported channel type")
			continue
		}
		channel, requests, err := newChannel.Accept()
		if err != nil {
			continue
		}
		go handleSession(channel, requests)
	}
}

// handleSession answers one session request. "exec" echoes the command it was
// given back on stdout — that echo is what lets an AI-exec spec prove the string
// the *server* received is the same one the approval dialog showed and the audit
// row recorded, rather than only comparing the app's own two copies of it.
func handleSession(channel ssh.Channel, requests <-chan *ssh.Request) {
	defer func() { _ = channel.Close() }()
	for req := range requests {
		switch req.Type {
		case "pty-req", "window-change", "env":
			_ = req.Reply(true, nil)
		case "exec":
			_ = req.Reply(true, nil)
			// RFC 4254 §6.5: the "exec" payload is a single string field.
			var payload struct{ Command string }
			if err := ssh.Unmarshal(req.Payload, &payload); err == nil {
				_, _ = fmt.Fprintf(channel, "mock-exec-ran: %s\n", payload.Command)
			}
			_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{Status: 0}))
			return
		case "shell":
			_ = req.Reply(true, nil)
			serveShell(channel)
			return
		default:
			_ = req.Reply(false, nil)
		}
	}
}

// serveShell keeps the PTY open and marks every submitted line. The marker is
// the remote oracle used by the terminal e2e: input must cross Wails and SSH
// before it can be rendered back by xterm.
func serveShell(channel ssh.Channel) {
	_, _ = fmt.Fprint(channel, "mock-shell-ready\r\n$ ")
	reader := bufio.NewReader(channel)
	for {
		line, err := reader.ReadString('\r')
		if err != nil {
			break
		}
		command := strings.TrimSuffix(line, "\r")
		recordShellCommand(command)
		_, _ = fmt.Fprintf(channel, "mock-shell-ran: %s\r\n$ ", command)
	}
	_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{Status: 0}))
}

func recordShellCommand(command string) {
	if commandLogPath == "" {
		return
	}
	// #nosec G304 -- main validates the harness-owned path's fixed basename;
	// the parent is the isolated per-run data directory supplied by env.js.
	file, err := os.OpenFile(commandLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	_, _ = fmt.Fprintln(file, command)
}

func newSigner() (ssh.Signer, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	return ssh.NewSignerFromKey(key)
}

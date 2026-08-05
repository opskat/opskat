#!/usr/bin/env bash
#
# Real cross-platform SSH agent fixtures — macOS/Linux Unix socket.
#
# Starts a REAL ssh-agent (the system binary, checked with `command -v`), loads
# an ed25519 key with ssh-add, points SSH_AUTH_SOCK at it and drives
# internal/sshagent/realfixture through the real transport, covering:
#   native success, identity missing, provider rejects signing,
#   cancel while waiting on the agent, agent + MFA.
#
# If no `ssh-agent` binary exists, the Go harness falls back to serving its own
# keyring agent over a real unix socket (documented in README.md).
#
# Emits a machine-readable JSON report (out/result.json) plus logs
# (out/run.log, out/test.log), and asserts none of them contain the private
# key, a signature or the MFA challenge answer. The private key and the socket
# live in a work/ dir that is removed on exit.
#
# Windows: the named-pipe fixtures are CI-only and run under the race detector
# in .github/workflows/ssh-agent-fixtures.yml — never run them on a Unix host.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
OUT_DIR="${SCRIPT_DIR}/out"
WORK_DIR="${SCRIPT_DIR}/work"

agent_pid=""
pubkey=""
privkey=""
auth_sock=""

cleanup() {
  if [ -n "${agent_pid}" ]; then
    kill "${agent_pid}" 2>/dev/null || true
  fi
  rm -rf "${WORK_DIR}"
}
trap cleanup EXIT

mkdir -p "${OUT_DIR}" "${WORK_DIR}"

# Prefer the real system ssh-agent for a true native fixture.
if command -v ssh-agent >/dev/null 2>&1; then
  socket="${WORK_DIR}/agent.sock"
  keyfile="${WORK_DIR}/fixture_id_ed25519"
  agent_out="$(ssh-agent -a "${socket}" 2>&1 || true)"
  agent_pid="$(printf '%s\n' "${agent_out}" | sed -n 's/.*Agent pid \([0-9][0-9]*\).*/\1/p' | head -1)"
  if [ -z "${agent_pid}" ]; then
    echo "ERROR: ssh-agent did not start: ${agent_out}" >&2
    exit 1
  fi
  auth_sock="${socket}"
  export SSH_AUTH_SOCK="${socket}"
  ssh-keygen -q -t ed25519 -N "" -f "${keyfile}"
  ssh-add -q "${keyfile}"
  pubkey="${keyfile}.pub"
  privkey="${keyfile}"
  echo "==> system ssh-agent (pid ${agent_pid}) on ${socket}; ed25519 key loaded"
else
  echo "==> no system ssh-agent; Go harness serves its own keyring agent over a unix socket"
fi

set +e
(
  cd "${REPO_ROOT}"
  OPSKAT_REAL_AGENT_FIXTURE=1 \
  SSH_AUTH_SOCK="${auth_sock}" \
  OPSKAT_FIXTURE_PUBKEY="${pubkey}" \
  OPSKAT_FIXTURE_PRIVKEY="${privkey}" \
  OPSKAT_FIXTURE_OUT="${OUT_DIR}/result.json" \
  OPSKAT_FIXTURE_LOG="${OUT_DIR}/run.log" \
  go test ./internal/sshagent/realfixture/ -run 'TestRealUnixSocketFixtures' -count=1 -v \
    2>&1 | tee "${OUT_DIR}/test.log"
)
status=${PIPESTATUS[0]}
set -e

echo ""
echo "=== machine-readable result: ${OUT_DIR}/result.json ==="
if [ -f "${OUT_DIR}/result.json" ]; then
  cat "${OUT_DIR}/result.json"
else
  echo "(no result.json — the fixture did not reach the reporting step)"
fi

# Belt-and-braces: the Go sanitizer already asserts this, but a fixture must
# never ship an artifact that leaks the private key or the MFA answer.
if grep -Rq -- '-----BEGIN OPENSSH PRIVATE KEY-----' "${OUT_DIR}" \
   || grep -Rq -- 'fixture-answer-42' "${OUT_DIR}"; then
  echo "ERROR: artifact under ${OUT_DIR} leaks private key or MFA answer" >&2
  exit 1
fi

exit "${status}"

#!/usr/bin/env bash
#
# Windows named-pipe SSH agent fixtures — CI-ONLY.
#
# Drives internal/sshagent/realfixture's TestRealWindowsNamedPipeFixtures, which
# serves an OpenSSH-compatible named pipe (\\\\.\\pipe\\...) with its own
# keyring agent (no dependency on the Windows OpenSSH agent service) and covers
# the five spec scenarios over the Windows transport. The named-pipe cancel
# path runs under the race detector (-race) as required by the design spec's
# "跨平台真实 fixture" row.
#
# This script MUST NOT be run on macOS/Linux: the named-pipe fixture code is
# build-tagged `windows` and only the GitHub Actions windows job invokes it.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
OUT_DIR="${SCRIPT_DIR}/out"

mkdir -p "${OUT_DIR}"

set +e
(
  cd "${REPO_ROOT}"
  OPSKAT_REAL_AGENT_FIXTURE=1 \
  OPSKAT_FIXTURE_OUT="${OUT_DIR}/result-windows.json" \
  OPSKAT_FIXTURE_LOG="${OUT_DIR}/run-windows.log" \
  go test -race ./internal/sshagent/realfixture/ -run 'TestRealWindowsNamedPipeFixtures' -count=1 -v \
    2>&1 | tee "${OUT_DIR}/test-windows.log"
)
status=${PIPESTATUS[0]}
set -e

echo ""
echo "=== machine-readable result: ${OUT_DIR}/result-windows.json ==="
if [ -f "${OUT_DIR}/result-windows.json" ]; then
  cat "${OUT_DIR}/result-windows.json"
else
  echo "(no result-windows.json — the fixture did not reach the reporting step)"
fi

# Belt-and-braces secret scan, mirroring run.sh.
if grep -Rq -- '-----BEGIN OPENSSH PRIVATE KEY-----' "${OUT_DIR}" \
   || grep -Rq -- 'fixture-answer-42' "${OUT_DIR}"; then
  echo "ERROR: artifact under ${OUT_DIR} leaks private key or MFA answer" >&2
  exit 1
fi

exit "${status}"

// Runs the real opsctl CLI as a subprocess against the running e2e app. Shared by the
// specs that drive opsctl (opsctl-approval.spec.ts, generic-asset.spec.ts).
import { spawn } from "node:child_process";

export interface OpsctlResult {
  code: number | null;
  stdout: string;
  stderr: string;
}

export function runOpsctl(args: string[]): Promise<OpsctlResult> {
  const child = spawn("go", ["run", "./cmd/opsctl", ...args], {
    cwd: "..",
    env: process.env,
    stdio: ["ignore", "pipe", "pipe"],
  });
  let stdout = "";
  let stderr = "";
  child.stdout.setEncoding("utf8").on("data", (chunk) => (stdout += chunk));
  child.stderr.setEncoding("utf8").on("data", (chunk) => (stderr += chunk));
  return new Promise((resolve, reject) => {
    child.once("error", reject);
    // "close", not "exit": the stdio streams are fully drained only then, and callers
    // assert on stdout.
    child.once("close", (code) => resolve({ code, stdout, stderr }));
  });
}

// Runs the real opsctl CLI as a subprocess against the running e2e app, the same
// shape as opsctl-approval.spec.ts's local `runOpsctl` — pulled out here because
// generic-asset.spec.ts needs several invocations (HTTP GET/DELETE, command exec,
// secret get) rather than the one call that spec makes.
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
    child.once("exit", (code) => resolve({ code, stdout, stderr }));
  });
}

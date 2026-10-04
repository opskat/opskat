// A tiny in-process HTTP mock for the generic-asset HTTP exec spec
// (docs/specs/2026-09-28-generic-asset.md "HTTP 请求" / "取值"). It runs inside the
// Playwright test process itself rather than as a fourth harness `webServer` like
// redis-mock / ssh-mock / openai-mock: those live on fixed ports carved out of this
// checkout's port block (harness/env.js), and that block is already fully assigned
// (+0 app, +1 redis, +2 ssh, +3 openai, +4 sandbox app, +5 sandbox CDP). A random
// loopback port needs no block entry and sidesteps the exact "port already bound by
// something else" hijack risk documented in e2e-harness-guide.md §7 (34216 reused by
// a sibling checkout) entirely, since the OS picks a free port at listen() time. The
// real Wails app dials it exactly like any other loopback service — nothing about
// being in-process makes the HTTP round trip any less real.
import { createServer, type IncomingHttpHeaders, type Server } from "node:http";

export interface RecordedRequest {
  method: string;
  path: string;
  headers: IncomingHttpHeaders;
  body: string;
}

export interface HttpMock {
  port: number;
  baseUrl: string;
  /** Append-only log of every request received so far, in arrival order. */
  requests: RecordedRequest[];
  close(): Promise<void>;
}

/**
 * Starts the mock and resolves once it is listening. Every method gets a 200 JSON
 * response — the spec only cares that opsctl's declared 2xx/approval/audit behavior
 * is correct, not about protocol semantics of a real backend, so the mock does not
 * try to emulate a specific API's routes or status codes.
 */
export function startHttpMock(): Promise<HttpMock> {
  return new Promise((resolve, reject) => {
    const requests: RecordedRequest[] = [];
    const server: Server = createServer((req, res) => {
      const chunks: Buffer[] = [];
      req.on("data", (chunk: Buffer) => chunks.push(chunk));
      req.on("end", () => {
        const body = Buffer.concat(chunks).toString("utf8");
        const url = new URL(req.url ?? "/", "http://mock.invalid");
        requests.push({ method: req.method ?? "", path: url.pathname, headers: req.headers, body });
        res.writeHead(200, { "content-type": "application/json" });
        res.end(JSON.stringify({ ok: true, method: req.method, path: url.pathname }));
      });
    });
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const addr = server.address();
      const port = addr && typeof addr === "object" ? addr.port : 0;
      resolve({
        port,
        baseUrl: `http://127.0.0.1:${port}`,
        requests,
        close: () => new Promise<void>((res) => server.close(() => res())),
      });
    });
  });
}

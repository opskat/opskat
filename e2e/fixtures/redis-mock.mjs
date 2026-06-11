// Minimal RESP mock server for the e2e harness — just enough for go-redis v9 to
// complete a connection and for the app's "Test Connection" (a single PING) to
// pass. Pure Node, no dependencies; started as a Playwright `webServer` (see
// playwright.config.ts) and dialed by the real app at 127.0.0.1:<port>.
//
// go-redis v9 connect handshake (vendored redis.go `initConn`):
//   1. HELLO 3        — a *redis error* reply makes go-redis fall back to RESP2
//                       and continue (only a parse/network error is fatal), so we
//                       answer "-ERR ...". With no password/DB the auth+select
//                       pipeline is then empty.
//   2. CLIENT SETINFO — sent twice (lib name/version); errors are tolerated.
//   3. PING           — our explicit test command; must get "+PONG".
// So: HELLO -> error, PING -> +PONG, everything else -> +OK.
import net from "node:net";

const PONG = Buffer.from("+PONG\r\n");
const OK = Buffer.from("+OK\r\n");
const HELLO_ERR = Buffer.from("-ERR unknown command 'HELLO'\r\n");

const port = Number(process.argv[2]);
if (!Number.isInteger(port) || port <= 0) {
  console.error("usage: redis-mock.mjs <port>");
  process.exit(1);
}

// Parse one RESP array command from `buf` at `offset`. Returns { name, next }
// once a full command is buffered, null if it needs more bytes. Throws on
// malformed framing (caller drops the connection). go-redis lowercases command
// names on the wire, so we upper-case for comparison.
function parseCommand(buf, offset) {
  if (offset >= buf.length) return null;
  if (buf[offset] !== 0x2a /* '*' */) throw new Error("expected array header");
  const headerEnd = buf.indexOf("\r\n", offset);
  if (headerEnd === -1) return null;
  const argc = Number(buf.toString("latin1", offset + 1, headerEnd));
  if (!Number.isInteger(argc) || argc < 0) throw new Error("bad argc");
  let pos = headerEnd + 2;
  let name = "";
  for (let i = 0; i < argc; i++) {
    if (pos >= buf.length) return null;
    if (buf[pos] !== 0x24 /* '$' */) throw new Error("expected bulk string");
    const lenEnd = buf.indexOf("\r\n", pos);
    if (lenEnd === -1) return null;
    const len = Number(buf.toString("latin1", pos + 1, lenEnd));
    if (!Number.isInteger(len) || len < 0) throw new Error("bad bulk length");
    const dataStart = lenEnd + 2;
    const dataEnd = dataStart + len;
    if (dataEnd + 2 > buf.length) return null; // wait for the bulk body + CRLF
    if (i === 0) name = buf.toString("latin1", dataStart, dataEnd).toUpperCase();
    pos = dataEnd + 2;
  }
  return { name, next: pos };
}

const server = net.createServer((socket) => {
  let buf = Buffer.alloc(0);
  socket.on("data", (chunk) => {
    buf = buf.length ? Buffer.concat([buf, chunk]) : chunk;
    try {
      for (;;) {
        const cmd = parseCommand(buf, 0);
        if (!cmd) break;
        buf = buf.subarray(cmd.next);
        if (cmd.name === "HELLO") socket.write(HELLO_ERR);
        else if (cmd.name === "PING") socket.write(PONG);
        else socket.write(OK); // AUTH / SELECT / CLIENT SETINFO / QUIT / ...
      }
    } catch {
      socket.destroy(); // malformed framing — not a real client, drop it
    }
  });
  // Ignore resets — e.g. Playwright's TCP readiness probe connects then closes.
  socket.on("error", () => {});
});

server.on("error", (err) => {
  console.error(`redis-mock failed: ${err.message}`);
  process.exit(1);
});

server.listen(port, "127.0.0.1", () => {
  console.error(`redis-mock listening on 127.0.0.1:${port}`);
});

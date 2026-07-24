// Minimal OpenAI-compatible chat-completions mock for the e2e harness — enough
// for the app's real AI stack (internal/ai/runner → cago provider/openai →
// sashabaranov/go-openai) to run a scripted multi-turn tool-calling session with
// no network and no API key that costs money. Pure Node, no dependencies;
// started as a Playwright `webServer` (see playwright.config.ts) and dialed by
// the real app at http://127.0.0.1:<port>/v1 (the AI provider row's api_base).
//
// The app always streams (agent.Runner → Provider.ChatStream), so /v1/chat/completions
// answers SSE when the request carries `"stream": true`, and plain JSON otherwise.
//
// ---------------------------------------------------------------------------
// Programmable interface (this is the point of the mock — a spec decides what the
// "model" replies, the mock hard-codes nothing):
//
//   POST /__mock/script     body: {"steps":[ <step>, ... ]}
//        Installs a script and clears the recorded requests. Each incoming
//        /v1/chat/completions consumes the next step, in order.
//        step := {"text":"..."}                              → an assistant message
//              | {"tool":{"name":"exec","args":{...}}}       → one tool call
//              | {"tools":[{"name":..,"args":{..}}, ...]}    → parallel tool calls
//        When the script runs out, the mock replies with `exhaustedText` (default
//        "[mock] script exhausted") so a runaway agent loop terminates instead of
//        hanging the spec.
//
//   GET  /__mock/requests   → {"requests":[<the raw chat-completions bodies>]}
//        Lets a spec assert on what the model was *shown* — e.g. that the tool
//        result for a denied exec carries the USER DENIED text, or that a tool
//        was never called again after a grant.
//
// Everything is per-process state; the harness starts one mock per run.
import http from "node:http";

const port = Number(process.argv[2]);
if (!Number.isInteger(port) || port <= 0) {
  console.error("usage: openai-mock.mjs <port>");
  process.exit(1);
}

/** @type {Array<Record<string, unknown>>} */
let steps = [];
let stepIndex = 0;
let exhaustedText = "[mock] script exhausted";
/** @type {Array<Record<string, unknown>>} */
let requests = [];

function readBody(req) {
  return new Promise((resolve, reject) => {
    let data = "";
    req.on("data", (c) => (data += c));
    req.on("end", () => resolve(data));
    req.on("error", reject);
  });
}

function json(res, code, payload) {
  const body = JSON.stringify(payload);
  res.writeHead(code, { "content-type": "application/json", "content-length": Buffer.byteLength(body) });
  res.end(body);
}

// Turn a script step into the (content, toolCalls) pair the wire format needs.
function materialize(step) {
  if (!step) return { content: exhaustedText, toolCalls: [] };
  const calls = step.tools ?? (step.tool ? [step.tool] : []);
  if (calls.length > 0) {
    return {
      content: step.text ?? "",
      toolCalls: calls.map((c, i) => ({
        id: `call_${stepIndex}_${i}`,
        type: "function",
        function: { name: c.name, arguments: JSON.stringify(c.args ?? {}) },
      })),
    };
  }
  return { content: step.text ?? "", toolCalls: [] };
}

function chunk(model, delta, finishReason) {
  return {
    id: "chatcmpl-mock",
    object: "chat.completion.chunk",
    created: Math.floor(Date.now() / 1000),
    model,
    choices: [{ index: 0, delta, finish_reason: finishReason ?? null }],
  };
}

function streamReply(res, model, content, toolCalls) {
  res.writeHead(200, {
    "content-type": "text/event-stream",
    "cache-control": "no-cache",
    connection: "keep-alive",
  });
  const send = (obj) => res.write(`data: ${JSON.stringify(obj)}\n\n`);

  send(chunk(model, { role: "assistant", content: "" }));
  if (content) send(chunk(model, { content }));
  toolCalls.forEach((tc, i) => {
    // One delta per call carrying the whole argument string: go-openai accumulates
    // ArgsDelta, so a single complete chunk is a valid (if unfragmented) stream.
    send(chunk(model, { tool_calls: [{ index: i, id: tc.id, type: "function", function: tc.function }] }));
  });
  send(chunk(model, {}, toolCalls.length > 0 ? "tool_calls" : "stop"));
  // stream_options.include_usage → a final choices-less chunk carrying usage.
  send({
    id: "chatcmpl-mock",
    object: "chat.completion.chunk",
    created: Math.floor(Date.now() / 1000),
    model,
    choices: [],
    usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 },
  });
  res.write("data: [DONE]\n\n");
  res.end();
}

const server = http.createServer((req, res) => {
  const url = new URL(req.url ?? "/", `http://127.0.0.1:${port}`);

  if (req.method === "POST" && url.pathname === "/__mock/script") {
    readBody(req)
      .then((raw) => {
        const parsed = raw ? JSON.parse(raw) : {};
        steps = Array.isArray(parsed.steps) ? parsed.steps : [];
        exhaustedText = parsed.exhaustedText ?? "[mock] script exhausted";
        stepIndex = 0;
        requests = [];
        json(res, 200, { ok: true, steps: steps.length });
      })
      .catch((err) => json(res, 400, { error: String(err) }));
    return;
  }

  if (req.method === "GET" && url.pathname === "/__mock/requests") {
    json(res, 200, { requests, consumed: stepIndex });
    return;
  }

  if (req.method === "GET" && url.pathname === "/v1/models") {
    json(res, 200, { object: "list", data: [{ id: "mock-model", object: "model", owned_by: "e2e" }] });
    return;
  }

  if (req.method === "POST" && url.pathname === "/v1/chat/completions") {
    readBody(req)
      .then((raw) => {
        let body = {};
        try {
          body = JSON.parse(raw);
        } catch {
          body = { unparsed: raw };
        }
        requests.push(body);
        // Conversation-title generation also uses chat completions, but does not
        // send tool definitions. It must not consume an agent script step or the
        // next real tool call becomes timing-dependent on when title generation runs.
        const isAgentRequest = Array.isArray(body.tools) && body.tools.length > 0;
        const step = isAgentRequest ? steps[stepIndex] : { text: "e2e conversation" };
        const { content, toolCalls } = materialize(step);
        if (isAgentRequest) stepIndex++;
        const model = body.model || "mock-model";
        if (body.stream) {
          streamReply(res, model, content, toolCalls);
          return;
        }
        json(res, 200, {
          id: "chatcmpl-mock",
          object: "chat.completion",
          created: Math.floor(Date.now() / 1000),
          model,
          choices: [
            {
              index: 0,
              message: {
                role: "assistant",
                content,
                ...(toolCalls.length > 0 ? { tool_calls: toolCalls } : {}),
              },
              finish_reason: toolCalls.length > 0 ? "tool_calls" : "stop",
            },
          ],
          usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 },
        });
      })
      .catch((err) => json(res, 400, { error: String(err) }));
    return;
  }

  json(res, 404, { error: `openai-mock: unhandled ${req.method} ${url.pathname}` });
});

server.on("error", (err) => {
  console.error(`openai-mock failed: ${err.message}`);
  process.exit(1);
});

server.listen(port, "127.0.0.1", () => {
  console.error(`openai-mock listening on 127.0.0.1:${port}`);
});

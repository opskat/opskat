// frontend/src/extension/api.ts
import { CallExtensionTool, CancelExtensionTool } from "../../wailsjs/go/extension/Extension";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import type { ExtAPI, ExtCallOptions, ExtEvent } from "./types";

// crypto.randomUUID is available in every WebView the app supports; a collision
// would make two calls indistinguishable, so this is not somewhere to hand-roll a
// counter that resets when the page reloads.
function newInvocationId(): string {
  return crypto.randomUUID();
}

interface ToolEventPayload {
  extension: string;
  invocationId: string;
  eventType: string;
  data: unknown;
}

// subscribeToolEvents delivers the events of one call to onEvent. EventsOn's
// return value is this listener's own unsubscribe; EventsOff takes only the
// event name, so calling it would drop every other running call's listener
// along with this one.
function subscribeToolEvents(extName: string, invocationId: string, onEvent: (e: ExtEvent) => void): () => void {
  return EventsOn("ext:tool:event", (event: ToolEventPayload) => {
    if (event.extension === extName && event.invocationId === invocationId) {
      onEvent({ eventType: event.eventType, data: event.data });
    }
  });
}

export function createExtensionAPI(): ExtAPI {
  return {
    async callTool(
      extName: string,
      tool: string,
      args: unknown,
      assetId?: number,
      options?: ExtCallOptions
    ): Promise<unknown> {
      // A page's call runs directly on the backend (no approval or audit; see
      // CallExtensionTool). The invocation id is minted here rather than returned
      // by the backend because this call needs it before the call returns: it is
      // what tells this call's events apart from every other call of the same
      // extension, and what cancel names.
      const signal = options?.signal;
      signal?.throwIfAborted();
      const invocationId = newInvocationId();
      // Subscribed before the call starts, so its first events are not missed.
      const unsubscribe = options?.onEvent ? subscribeToolEvents(extName, invocationId, options.onEvent) : undefined;
      try {
        const call = CallExtensionTool(extName, tool, JSON.stringify(args ?? {}), invocationId, assetId ?? 0);
        if (!signal) return parseResult(await call);
        return await untilAborted(call, signal, invocationId);
      } finally {
        unsubscribe?.();
      }
    },
  };
}

// untilAborted settles with the call, or — once signal aborts — with the
// signal's reason as soon as the backend has taken the cancel, not when the
// canceled call finally unwinds (the guest gets a grace period to stop on its
// own). A failed cancel is what the caller sees instead, rather than an abort
// that silently did nothing.
async function untilAborted(call: Promise<string>, signal: AbortSignal, invocationId: string): Promise<unknown> {
  let onAbort: () => void = () => undefined;
  const aborted = new Promise<never>((_resolve, reject) => {
    onAbort = () => {
      CancelExtensionTool(invocationId).then(() => reject(signal.reason), reject);
    };
    signal.addEventListener("abort", onAbort, { once: true });
  });
  try {
    const result = await Promise.race([call, aborted]);
    // Aborted while the result was on its way: the caller has stopped
    // waiting, and the cancel it started still has to be accounted for.
    if (signal.aborted) return await aborted;
    return parseResult(result);
  } catch (err) {
    // The canceled backend call usually fails first, with its own "context
    // canceled"; the abort is the outcome the caller asked about.
    if (signal.aborted) return await aborted;
    throw err;
  } finally {
    signal.removeEventListener("abort", onAbort);
  }
}

function parseResult(result: string): unknown {
  if (!result) return null;
  try {
    return JSON.parse(result);
  } catch {
    return result;
  }
}

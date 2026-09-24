// frontend/src/extension/api.ts
import {
  CallExtensionAction,
  CallExtensionTool,
  CancelExtensionAction,
  CancelExtensionTool,
} from "../../wailsjs/go/extension/Extension";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import type { ExtAPI, ExtCallOptions, ExtEvent } from "./types";

// One running action. The invocation id is minted here rather than returned by
// the backend because the caller needs it before the call it is about to make
// returns: it is what tells this run's events apart from every other run of the
// same extension, and what cancel names.
export interface ExtActionRun {
  invocationId: string;
  result: Promise<unknown>;
  cancel(): Promise<void>;
}

// The action surface a caller needs to run several actions of one extension at
// once — `executeAction` alone can only await, which is enough for a one-shot
// call and not enough for an upload queue.
export interface ExtActionAPI extends ExtAPI {
  startAction(
    extName: string,
    action: string,
    args: unknown,
    onEvent?: (e: ExtEvent) => void,
    assetId?: number
  ): ExtActionRun;
}

// crypto.randomUUID is available in every WebView the app supports; a collision
// would make two runs indistinguishable, so this is not somewhere to hand-roll a
// counter that resets when the page reloads.
function newInvocationId(): string {
  return crypto.randomUUID();
}

interface ActionEventPayload {
  extension: string;
  invocationId: string;
  eventType: string;
  data: unknown;
}

export function createExtensionAPI(): ExtActionAPI {
  const api: ExtActionAPI = {
    async callTool(
      extName: string,
      tool: string,
      args: unknown,
      assetId?: number,
      options?: ExtCallOptions
    ): Promise<unknown> {
      // A call scoped to an asset now clears the desktop's policy/approval gate
      // (internal/app/opsctl's RunPageToolCall) instead of dialing the plugin
      // directly. The invocation id is this call's own correlation token — the
      // same per-call convention startAction already uses, reused here rather
      // than inventing a second one — not the identity an "always allow" grant
      // persists under; the backend derives that from the asset (for the current
      // desktop run) so a grant outlives the one call that requested it. It is
      // also what cancel names.
      const signal = options?.signal;
      signal?.throwIfAborted();
      const invocationId = newInvocationId();
      const argsJSON = JSON.stringify(args ?? {});
      const call = CallExtensionTool(extName, tool, argsJSON, invocationId, assetId ?? 0);
      if (!signal) return parseResult(await call);

      // Abort settles the promise with the signal's reason once the backend has
      // taken the cancel — not when the canceled call finally unwinds, which a
      // pending approval dialog can hold up. A failed cancel is what the caller
      // sees instead, rather than an abort that silently did nothing.
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
    },

    startAction(
      extName: string,
      action: string,
      args: unknown,
      onEvent?: (e: ExtEvent) => void,
      assetId?: number
    ): ExtActionRun {
      const invocationId = newInvocationId();
      let unsubscribe: (() => void) | undefined;

      if (onEvent) {
        // EventsOn's return value is this listener's own unsubscribe. EventsOff
        // takes only the event name, so calling it would drop every other
        // running action's listener along with this one.
        unsubscribe = EventsOn("ext:action:event", (event: ActionEventPayload) => {
          if (event.extension === extName && event.invocationId === invocationId) {
            onEvent({ eventType: event.eventType, data: event.data });
          }
        });
      }

      const result = (async () => {
        try {
          const argsJSON = JSON.stringify(args ?? {});
          return parseResult(await CallExtensionAction(extName, action, argsJSON, invocationId, assetId ?? 0));
        } finally {
          unsubscribe?.();
        }
      })();

      return {
        invocationId,
        result,
        cancel: () => CancelExtensionAction(extName, invocationId),
      };
    },

    executeAction(
      extName: string,
      action: string,
      args: unknown,
      onEvent?: (e: ExtEvent) => void,
      assetId?: number
    ): Promise<unknown> {
      return api.startAction(extName, action, args, onEvent, assetId).result;
    },
  };
  return api;
}

function parseResult(result: string): unknown {
  if (!result) return null;
  try {
    return JSON.parse(result);
  } catch {
    return result;
  }
}

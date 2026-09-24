// frontend/src/extension/api.ts
import { CallExtensionAction, CallExtensionTool, CancelExtensionAction } from "../../wailsjs/go/extension/Extension";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import type { ExtAPI, ExtEvent } from "./types";

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
    async callTool(extName: string, tool: string, args: unknown, assetId?: number): Promise<unknown> {
      const argsJSON = JSON.stringify(args ?? {});
      const result = await CallExtensionTool(extName, tool, argsJSON, assetId ?? 0);
      return parseResult(result);
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

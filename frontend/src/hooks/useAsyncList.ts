import { useCallback, useEffect, useRef, useState } from "react";
import type { AsyncStatus } from "@/components/common/AsyncState";
import { errorMessage } from "@/lib/errorMessage";

/** Load state of a list behind a dropdown / picker; shaped for `<AsyncState status error onRetry>`. */
export interface ListLoad {
  status: AsyncStatus;
  error: string | null;
  /** Retry from scratch: shows the loading state until the answer arrives. */
  reload: () => void;
}

/**
 * Loads a list once on mount. `status` starts as `loading` and only becomes `ready` after a
 * successful request, so a failed load never looks like an empty list. Only the latest request's
 * answer is applied: an earlier one that answers last is dropped.
 */
export function useAsyncList<T>(fetcher: () => Promise<T[] | null | undefined>): {
  items: T[];
  setItems: (items: T[]) => void;
  load: ListLoad;
  /** Re-fetch after a change (install, toggle, ...): the current list stays on screen meanwhile. */
  refresh: () => void;
} {
  const [items, setItems] = useState<T[]>([]);
  const [status, setStatus] = useState<AsyncStatus>("loading");
  const [error, setError] = useState<string | null>(null);
  const fetcherRef = useRef(fetcher);
  useEffect(() => {
    fetcherRef.current = fetcher;
  });
  const latestRef = useRef(0);

  const refresh = useCallback(() => {
    const request = ++latestRef.current;
    fetcherRef.current().then(
      (list) => {
        if (request !== latestRef.current) return;
        setItems(list || []);
        setError(null);
        setStatus("ready");
      },
      (e) => {
        if (request !== latestRef.current) return;
        setError(errorMessage(e));
        setStatus("error");
      }
    );
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const reload = useCallback(() => {
    setStatus("loading");
    setError(null);
    refresh();
  }, [refresh]);

  return { items, setItems, load: { status, error, reload }, refresh };
}

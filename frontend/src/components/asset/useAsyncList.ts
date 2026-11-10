import { useCallback, useEffect, useRef, useState } from "react";
import type { AsyncStatus } from "@/components/common/AsyncState";
import { errorMessage } from "@/lib/errorMessage";

/** Load state of a list behind a dropdown / picker; shaped for `<AsyncState status error onRetry>`. */
export interface ListLoad {
  status: AsyncStatus;
  error: string | null;
  reload: () => void;
}

/**
 * Loads a list once on mount. `status` starts as `loading` and only becomes `ready` after a
 * successful request, so a failed load never looks like an empty list. `reload` retries.
 */
export function useAsyncList<T>(fetcher: () => Promise<T[] | null | undefined>): {
  items: T[];
  setItems: (items: T[]) => void;
  load: ListLoad;
} {
  const [items, setItems] = useState<T[]>([]);
  const [status, setStatus] = useState<AsyncStatus>("loading");
  const [error, setError] = useState<string | null>(null);
  const fetcherRef = useRef(fetcher);
  useEffect(() => {
    fetcherRef.current = fetcher;
  });

  const run = useCallback(() => {
    fetcherRef.current().then(
      (list) => {
        setItems(list || []);
        setStatus("ready");
      },
      (e) => {
        setError(errorMessage(e));
        setStatus("error");
      }
    );
  }, []);

  useEffect(() => {
    run();
  }, [run]);

  const reload = useCallback(() => {
    setStatus("loading");
    setError(null);
    run();
  }, [run]);

  return { items, setItems, load: { status, error, reload } };
}

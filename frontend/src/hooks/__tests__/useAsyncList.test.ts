import { describe, expect, it } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";
import { useAsyncList } from "../useAsyncList";

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe("useAsyncList", () => {
  it("keeps the latest request's result when an earlier one answers last", async () => {
    const calls: ReturnType<typeof deferred<string[]>>[] = [];
    const { result } = renderHook(() =>
      useAsyncList<string>(() => {
        const d = deferred<string[]>();
        calls.push(d);
        return d.promise;
      })
    );
    await act(async () => calls[0].resolve(["a"]));
    act(() => result.current.load.reload());
    act(() => result.current.load.reload());

    await act(async () => calls[2].resolve(["latest"]));
    await act(async () => calls[1].resolve(["stale"]));

    expect(result.current.items).toEqual(["latest"]);
    expect(result.current.load.status).toBe("ready");
  });

  it("refreshes in the background without dropping the loaded list, and reports a failed refresh", async () => {
    const calls: ReturnType<typeof deferred<string[]>>[] = [];
    const { result } = renderHook(() =>
      useAsyncList<string>(() => {
        const d = deferred<string[]>();
        calls.push(d);
        return d.promise;
      })
    );
    await act(async () => calls[0].resolve(["a"]));

    act(() => result.current.refresh());
    expect(result.current.load.status).toBe("ready");
    expect(result.current.items).toEqual(["a"]);

    await act(async () => calls[1].reject(new Error("gone")));
    await waitFor(() => expect(result.current.load.status).toBe("error"));
    expect(result.current.load.error).toBe("gone");
  });

  it("reports a failed load and recovers on reload", async () => {
    let fail = true;
    const { result } = renderHook(() =>
      useAsyncList<string>(() => (fail ? Promise.reject(new Error("vault locked")) : Promise.resolve(["p"])))
    );
    await waitFor(() => expect(result.current.load.status).toBe("error"));
    expect(result.current.load.error).toBe("vault locked");

    fail = false;
    act(() => result.current.load.reload());
    expect(result.current.load.status).toBe("loading");
    await waitFor(() => expect(result.current.load.status).toBe("ready"));
    expect(result.current.items).toEqual(["p"]);
    expect(result.current.load.error).toBeNull();
  });
});

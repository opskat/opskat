import { describe, it, expect, vi } from "vitest";
import { createOrderedQueue } from "../lib/orderedQueue";
import { orderedBySession } from "../stores/terminalStore";

const flush = () => new Promise((r) => setTimeout(r, 0));

function deferred() {
  let resolve!: () => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<void>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe("createOrderedQueue", () => {
  it("runs the first task synchronously (no added latency for idle case)", () => {
    const q = createOrderedQueue();
    const task = vi.fn().mockResolvedValue(undefined);
    q.push(task);
    expect(task).toHaveBeenCalledTimes(1); // 同步发起，未等微任务
    expect(q.idle).toBe(false);
  });

  it("does not start task N+1 until task N settles (serialization)", async () => {
    const q = createOrderedQueue();
    const d1 = deferred();
    const t1 = vi.fn().mockReturnValue(d1.promise);
    const t2 = vi.fn().mockResolvedValue(undefined);
    const t3 = vi.fn().mockResolvedValue(undefined);

    q.push(t1);
    q.push(t2);
    q.push(t3);
    await flush();
    expect(t1).toHaveBeenCalledTimes(1);
    expect(t2).not.toHaveBeenCalled();
    expect(t3).not.toHaveBeenCalled();

    d1.resolve();
    await flush();
    expect(t2).toHaveBeenCalledTimes(1);
    expect(t3).toHaveBeenCalledTimes(1); // t2 立即 resolve → t3 紧随
  });

  it("keeps ordering and stays drainable even if a task rejects", async () => {
    const q = createOrderedQueue();
    const order: number[] = [];
    q.push(() => Promise.reject(new Error("boom"))).catch(() => order.push(0));
    q.push(() => {
      order.push(1);
      return Promise.resolve();
    });
    await q.drain();
    expect(order).toEqual([0, 1]);
    expect(q.idle).toBe(true);
  });

  it("drain resolves immediately when idle and becomes idle after the last task", async () => {
    const q = createOrderedQueue();
    await expect(q.drain()).resolves.toBeUndefined();
    const d = deferred();
    q.push(() => d.promise);
    let drained = false;
    void q.drain().then(() => (drained = true));
    await flush();
    expect(drained).toBe(false);
    d.resolve();
    await flush();
    expect(drained).toBe(true);
    expect(q.idle).toBe(true);
  });
});

describe("orderedBySession", () => {
  it("serializes writes within a session (next not issued until prev resolves)", async () => {
    const resolvers: Array<() => void> = [];
    const raw = vi.fn().mockImplementation(() => new Promise<void>((res) => resolvers.push(res)));
    const write = orderedBySession(raw);

    write("s1", "a");
    write("s1", "b");
    write("s1", "c");
    await flush();
    expect(raw).toHaveBeenCalledTimes(1);
    expect(raw).toHaveBeenNthCalledWith(1, "s1", "a");

    resolvers[0]();
    await flush();
    expect(raw).toHaveBeenCalledTimes(2);
    expect(raw).toHaveBeenNthCalledWith(2, "s1", "b");
  });

  it("does not serialize across different sessions (independent order)", async () => {
    const pending: Record<string, Array<() => void>> = {};
    const raw = vi.fn().mockImplementation(
      (id: string) =>
        new Promise<void>((res) => {
          (pending[id] ??= []).push(res);
        })
    );
    const write = orderedBySession(raw);

    write("s1", "a"); // 在途、未 resolve
    write("s2", "x"); // 另一会话不应被 s1 阻塞
    await flush();

    expect(raw).toHaveBeenCalledWith("s1", "a");
    expect(raw).toHaveBeenCalledWith("s2", "x");
    expect(raw).toHaveBeenCalledTimes(2);
  });
});

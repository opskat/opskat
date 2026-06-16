// 顺序队列：把异步任务串成一条 promise 链，保证 任务N+1 在 任务N settle 后才发起。
//
// 为什么需要：跨 Wails 边界的后端调用是 fire-and-forget，而 Wails 给每条 IPC 消息各起一个
// goroutine 并发执行(internal/frontend/desktop/darwin/frontend.go: `go ...ProcessMessage`)，
// 互斥锁只保证排他、不保证 FIFO。于是连续的写会乱序抵达后端——破坏任何有序字节流：
// ZMODEM 子包错序→CRC 失败→二进制损坏；大段粘贴乱序→文本错乱。串行化后严格按提交顺序抵达。
//
// 空闲时首个任务同步发起(不引入额外微任务延迟，逐键输入零开销)，仅在有在途任务时才排队。
export interface OrderedQueue {
  /** 入队一个任务，返回该任务自身的 promise(可能 reject，调用方按需处理)。 */
  push<T>(task: () => Promise<T>): Promise<T>;
  /** 等到当前已入队任务全部 settle。 */
  drain(): Promise<void>;
  /** 队列是否空闲(无在途任务)。 */
  readonly idle: boolean;
}

export function createOrderedQueue(): OrderedQueue {
  let tail: Promise<unknown> | null = null;
  return {
    push<T>(task: () => Promise<T>): Promise<T> {
      const result: Promise<T> = tail ? tail.then(() => task()) : task();
      // 链路用「吞掉异常」的版本续接：单个任务失败不破坏后续顺序；调用方仍从 result 拿到原始结果。
      const guarded = result.catch(() => undefined);
      tail = guarded;
      void guarded.then(() => {
        if (tail === guarded) tail = null; // 排空即复位，下一个任务又能同步发起。
      });
      return result;
    },
    drain() {
      return (tail ?? Promise.resolve()).then(() => undefined);
    },
    get idle() {
      return tail === null;
    },
  };
}

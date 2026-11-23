import { useCallback, useEffect, useRef } from "react";
import { toast } from "sonner";
import { EventsOn } from "../../../wailsjs/runtime/runtime";
import { errorMessage } from "@/lib/errorMessage";

/** 一类后台任务(运行 SQL 文件、转储 SQL 文件):在模块里定义一次,身份不变。 */
export interface BackendJobSpec<E> {
  /** 后端发出任务事件的事件名。 */
  eventName: string;
  /** 事件属于哪一次任务。 */
  jobId: (event: E) => string;
  /** 任务的最后一个事件:之后不再有这次任务的事件。 */
  isFinal: (event: E) => boolean;
  /** 让后端停下一次任务。 */
  stop: (id: string) => Promise<unknown>;
}

/** 停下一次任务;停不下来是用户该知道的事。 */
async function requestStop<E>(spec: BackendJobSpec<E>, id: string) {
  try {
    await spec.stop(id);
  } catch (e) {
    toast.error(errorMessage(e));
  }
}

/**
 * 对话框里的一次后台任务:先订阅再开始,任务 id 回来之前到达的事件先攒着,拿到 id 后只认自己的;
 * 对话框消失(资产标签页关了)时停下进行中的任务,等 id 的时候就消失的,拿到 id 立刻停下。
 */
export function useBackendJob<E>(spec: BackendJobSpec<E>, apply: (event: E) => void) {
  const unsubscribeRef = useRef<(() => void) | null>(null);
  /** 进行中的任务 id(未开始或已结束时为 null)。 */
  const activeRef = useRef<string | null>(null);
  const mountedRef = useRef(true);

  useEffect(() => {
    // StrictMode 开发期会先清理再重跑 effect,重跑时要把「已挂载」恢复。
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      unsubscribeRef.current?.();
      unsubscribeRef.current = null;
      if (activeRef.current) void requestStop(spec, activeRef.current);
    };
  }, [spec]);

  /**
   * 开始一次任务:begin 向后端要任务 id;拿到后先调 onStarted,再交出 id 回来之前的事件。
   * begin 失败时取消订阅并把错误抛给调用方。
   */
  const start = useCallback(
    async (begin: () => Promise<string>, onStarted: () => void) => {
      let id: string | null = null;
      const early: E[] = [];
      const deliver = (event: E) => {
        apply(event);
        if (!spec.isFinal(event)) return;
        activeRef.current = null;
        unsubscribeRef.current?.();
        unsubscribeRef.current = null;
      };
      unsubscribeRef.current = EventsOn(spec.eventName, (event: E) => {
        if (id === null) early.push(event);
        else if (spec.jobId(event) === id) deliver(event);
      });
      let started: string;
      try {
        started = await begin();
      } catch (e) {
        unsubscribeRef.current?.();
        unsubscribeRef.current = null;
        throw e;
      }
      if (!mountedRef.current) {
        void requestStop(spec, started);
        return;
      }
      id = started;
      activeRef.current = started;
      onStarted();
      for (const event of early) {
        if (spec.jobId(event) === started) deliver(event);
      }
    },
    [apply, spec]
  );

  /** 停下进行中的任务;没有进行中的任务时返回 false。 */
  const stopActive = useCallback(() => {
    if (!activeRef.current) return false;
    void requestStop(spec, activeRef.current);
    return true;
  }, [spec]);

  return { start, stopActive };
}

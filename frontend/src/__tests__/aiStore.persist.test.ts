/* eslint-disable @typescript-eslint/no-explicit-any */
import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";

vi.mock("../i18n", () => ({
  default: { t: (key: string, fallback: string) => fallback || key },
}));

import { useAIStore } from "../stores/aiStore";
import { useTabStore } from "../stores/tabStore";
import { LoadConversationMessages, SaveConversationMessages, SendAIMessage } from "../../wailsjs/go/ai/AI";
import { EventsOn } from "../../wailsjs/runtime/runtime";

// 假后端：按「从 from 起替换」的契约维护每个会话的落库行。
function installFakeBackend() {
  const db = new Map<number, any[]>();
  vi.mocked(SaveConversationMessages).mockImplementation((async (convId: number, from: number, tail: any[]) => {
    const rows = db.get(convId) ?? [];
    rows.splice(from, rows.length - from, ...JSON.parse(JSON.stringify(tail)));
    db.set(convId, rows);
  }) as any);
  vi.mocked(LoadConversationMessages).mockImplementation((async (convId: number) =>
    JSON.parse(JSON.stringify(db.get(convId) ?? []))) as any);
  return db;
}

function historyRows(n: number) {
  return Array.from({ length: n }, (_, i) => ({
    role: i % 2 === 0 ? "user" : "assistant",
    content: `msg-${i}`,
    blocks: [{ type: "text", content: `msg-${i}` }],
  }));
}

// 只比较落库后用户能看到的字段。
function visible(messages: any[]) {
  return messages.map((m) => ({
    role: m.role,
    content: m.content,
    blocks: (m.blocks || []).map((b: any) => ({
      type: b.type,
      content: b.content,
      toolName: b.toolName,
      toolCallId: b.toolCallId,
      status: b.status,
    })),
  }));
}

async function flushMicrotasks() {
  for (let i = 0; i < 10; i++) await Promise.resolve();
}

async function openLoadedConversation(convId: number) {
  const callbacks: Array<(event: any) => void> = [];
  vi.mocked(EventsOn).mockImplementation(((_name: string, handler: (event: any) => void) => {
    callbacks.push(handler);
    return () => {};
  }) as any);
  useAIStore.setState({ conversations: [{ ID: convId, Title: "t", Updatetime: 0 } as any] });
  const tabId = await useAIStore.getState().openConversationTab(convId);
  return { tabId, callbacks };
}

describe("aiStore conversation persistence (tail saves)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    useTabStore.setState({ tabs: [], activeTabId: null });
    useAIStore.setState({
      configured: true,
      tabStates: {},
      conversations: [],
      conversationMessages: {},
      conversationStreaming: {},
      sidebarTabs: [],
      activeSidebarTabId: null,
    });
    vi.mocked(SendAIMessage).mockResolvedValue(undefined as any);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("appending a tool event to a loaded 200-message conversation sends only the tail", async () => {
    const db = installFakeBackend();
    db.set(900, historyRows(200));
    const { tabId, callbacks } = await openLoadedConversation(900);
    // 刚加载的历史与落库一致，不发 IPC。
    expect(SaveConversationMessages).not.toHaveBeenCalled();

    await useAIStore.getState().sendToTab(tabId, "run it");
    await flushMicrotasks();
    expect(vi.mocked(SaveConversationMessages).mock.calls.map((c) => [c[1], (c[2] as any[]).length])).toEqual([
      [200, 1],
    ]);

    vi.mocked(SaveConversationMessages).mockClear();
    callbacks[0]({ type: "tool_start", tool_name: "exec", tool_input: "{}", tool_call_id: "c1" });
    await flushMicrotasks();

    expect(SaveConversationMessages).toHaveBeenCalledTimes(1);
    const call = vi.mocked(SaveConversationMessages).mock.calls[0];
    expect(call[1]).toBe(201);
    expect((call[2] as any[]).map((m) => m.role)).toEqual(["assistant"]);

    const afterBytes = JSON.stringify(call).length;
    // 修改前每次保存都发整段快照，即这次保存后的全部落库行。
    const beforeBytes = JSON.stringify([900, db.get(900)]).length;
    process.stdout.write(`[R17] append tool event to 200 msgs: payload before=${beforeBytes}B after=${afterBytes}B\n`);

    callbacks[0]({ type: "tool_result", tool_name: "exec", tool_call_id: "c1", content: "ok" });
    callbacks[0]({ type: "done" });
    await flushMicrotasks();

    // 重新加载后的历史与全量保存一致。
    expect(db.get(900)).toHaveLength(202);
    expect(visible(db.get(900)!)).toEqual(visible(useAIStore.getState().conversationMessages[900]));
  });

  it("edit-resend from the middle replaces rows from that index and drops later ones", async () => {
    const db = installFakeBackend();
    db.set(901, historyRows(10));
    await openLoadedConversation(901);

    await useAIStore.getState().editAndResendConversation(901, 4, "edited");
    await flushMicrotasks();

    const froms = vi.mocked(SaveConversationMessages).mock.calls.map((c) => c[1]);
    expect(froms[0]).toBe(4);
    expect(Math.min(...froms)).toBe(4);
    expect(db.get(901)!.map((m) => m.content)).toEqual(["msg-0", "msg-1", "msg-2", "msg-3", "edited"]);
  });

  it("does not call IPC when the snapshot equals the last persisted one", async () => {
    const db = installFakeBackend();
    db.set(902, historyRows(6));
    const { tabId } = await openLoadedConversation(902);

    useTabStore.getState().closeTab(tabId);
    await flushMicrotasks();

    expect(SaveConversationMessages).not.toHaveBeenCalled();
  });

  it("a failed save is logged and resent from the same index next time", async () => {
    const db = installFakeBackend();
    db.set(903, historyRows(4));
    const { tabId, callbacks } = await openLoadedConversation(903);
    const errorSpy = vi.spyOn(console, "error").mockImplementation(() => {});
    const fake = vi.mocked(SaveConversationMessages).getMockImplementation()!;
    vi.mocked(SaveConversationMessages).mockImplementationOnce(async () => {
      throw new Error("disk full");
    });

    await useAIStore.getState().sendToTab(tabId, "next");
    await flushMicrotasks();
    expect(errorSpy).toHaveBeenCalled();
    expect(String(errorSpy.mock.calls.flat().join(" "))).toContain("disk full");
    expect(db.get(903)).toHaveLength(4);

    vi.mocked(SaveConversationMessages).mockImplementation(fake);
    callbacks[0]({ type: "tool_start", tool_name: "exec", tool_input: "{}", tool_call_id: "c1" });
    await flushMicrotasks();

    const retry = vi.mocked(SaveConversationMessages).mock.calls.at(-1)!;
    // 失败那次的用户消息随下一次保存一起重发。
    expect(retry[1]).toBe(4);
    expect((retry[2] as any[]).map((m) => m.role)).toEqual(["user", "assistant"]);

    callbacks[0]({ type: "tool_result", tool_name: "exec", tool_call_id: "c1", content: "ok" });
    callbacks[0]({ type: "done" });
    await flushMicrotasks();
    expect(visible(db.get(903)!)).toEqual(visible(useAIStore.getState().conversationMessages[903]));
  });

  it("serializes saves for one conversation and diffs each against the confirmed result", async () => {
    const db = installFakeBackend();
    db.set(904, historyRows(2));
    const { tabId, callbacks } = await openLoadedConversation(904);
    const fake = vi.mocked(SaveConversationMessages).getMockImplementation()!;
    let release!: () => void;
    vi.mocked(SaveConversationMessages).mockImplementationOnce((async (...args: any[]) => {
      await new Promise<void>((resolve) => (release = resolve));
      return (fake as any)(...args);
    }) as any);

    await useAIStore.getState().sendToTab(tabId, "q");
    callbacks[0]({ type: "tool_start", tool_name: "exec", tool_input: "{}", tool_call_id: "c1" });
    callbacks[0]({ type: "tool_result", tool_name: "exec", tool_call_id: "c1", content: "ok" });
    await flushMicrotasks();
    // 第一次保存未完成前不发第二次。
    expect(SaveConversationMessages).toHaveBeenCalledTimes(1);

    release();
    await flushMicrotasks();
    // 排队的两次合并成一次，基于第一次成功落库的结果算尾部。
    expect(vi.mocked(SaveConversationMessages).mock.calls.map((c) => c[1])).toEqual([2, 3]);
    expect(visible(db.get(904)!)).toEqual(visible(useAIStore.getState().conversationMessages[904]));
  });
});

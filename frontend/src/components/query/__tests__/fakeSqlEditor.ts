// Minimal stand-in for Monaco's standalone editor: text + one selection (offsets),
// just the surface SqlEditorTab touches.
const CTRL_CMD = 2048;
const SHIFT = 1024;
const ENTER = 3;

export interface Decoration {
  range: { startLineNumber: number; endLineNumber: number };
  options: Record<string, unknown>;
}

export function createFakeSqlEditor(initial: string) {
  let text = initial;
  let anchor = 0;
  let head = 0;
  let focused = false;
  let decorations: Decoration[] = [];
  const commands = new Map<number, () => void>();
  const contentListeners: Array<() => void> = [];
  const cursorListeners: Array<() => void> = [];
  const focusListeners: Array<() => void> = [];
  const blurListeners: Array<() => void> = [];

  const positionAt = (offset: number) => {
    const before = text.slice(0, offset).split("\n");
    return { lineNumber: before.length, column: before[before.length - 1].length + 1 };
  };
  const model = {
    getValue: () => text,
    getValueInRange: () => text.slice(Math.min(anchor, head), Math.max(anchor, head)),
    getOffsetAt: () => head,
    getPositionAt: positionAt,
    onDidChangeContent: (cb: () => void) => void contentListeners.push(cb),
  };
  const editor = {
    addCommand: (keybinding: number, handler: () => void) => void commands.set(keybinding, handler),
    getModel: () => model,
    getValue: () => text,
    getSelection: () => ({ isEmpty: () => anchor === head }),
    getPosition: () => positionAt(head),
    hasTextFocus: () => focused,
    onDidBlurEditorWidget: (cb: () => void) => void blurListeners.push(cb),
    onDidFocusEditorWidget: (cb: () => void) => void focusListeners.push(cb),
    onDidChangeCursorSelection: (cb: () => void) => void cursorListeners.push(cb),
    createDecorationsCollection: () => ({
      set: (next: Decoration[]) => void (decorations = next),
      clear: () => void (decorations = []),
    }),
    setValue: (v: string) => {
      text = v;
      contentListeners.forEach((cb) => cb());
    },
  };
  return {
    editor,
    monaco: { KeyMod: { CtrlCmd: CTRL_CMD, Shift: SHIFT }, KeyCode: { Enter: ENTER } },
    /** 光标放在 offset 处（无选区）。 */
    setCursor(offset: number) {
      anchor = head = offset;
      cursorListeners.forEach((cb) => cb());
    },
    select(from: number, to: number) {
      anchor = from;
      head = to;
      cursorListeners.forEach((cb) => cb());
    },
    setFocus(next: boolean) {
      focused = next;
      (next ? focusListeners : blurListeners).forEach((cb) => cb());
    },
    runMod: () => commands.get(CTRL_CMD | ENTER)?.(),
    runModShift: () => commands.get(CTRL_CMD | SHIFT | ENTER)?.(),
    decorations: () => decorations,
  };
}

export type FakeSqlEditor = ReturnType<typeof createFakeSqlEditor>;

/** 每个渲染出的 CodeEditor 都会登记到这里；测试读最后一个。 */
export const mounted: { current: FakeSqlEditor | null } = { current: null };

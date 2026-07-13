import { describe, expect, it, vi } from "vitest";
import { decodeVNCClipboardText, pasteVNCClipboardText } from "@/lib/vncClipboard";

describe("VNC clipboard encoding", () => {
  it("sends mixed Chinese and English as one GBK legacy clipboard message", async () => {
    const clipboardPasteFrom = vi.fn();
    const sendKey = vi.fn();
    const encodeLegacy = vi.fn().mockResolvedValue([0x61, 0x62, 0x63, 0xd6, 0xd0, 0xce, 0xc4, 0x58, 0x59, 0x5a]);

    await pasteVNCClipboardText({ clipboardPasteFrom, sendKey }, "abc中文XYZ", encodeLegacy);

    expect(encodeLegacy).toHaveBeenCalledWith("abc中文XYZ");
    expect(sendKey).not.toHaveBeenCalled();
    const sent = clipboardPasteFrom.mock.calls[0][0] as string;
    expect(Array.from(sent, (char) => char.charCodeAt(0))).toEqual([
      0x61, 0x62, 0x63, 0xd6, 0xd0, 0xce, 0xc4, 0x58, 0x59, 0x5a,
    ]);
  });

  it("keeps Unicode text unchanged when extended clipboard is available", () => {
    const clipboardPasteFrom = vi.fn();

    pasteVNCClipboardText(
      {
        clipboardPasteFrom,
        sendKey: vi.fn(),
        _clipboardServerCapabilitiesFormats: { 1: true },
        _clipboardServerCapabilitiesActions: { [1 << 27]: true },
      },
      "中文abc"
    );

    expect(clipboardPasteFrom).toHaveBeenCalledWith("中文abc");
  });

  it("falls back to paced Unicode keysyms when GBK cannot encode the text", async () => {
    const sendKey = vi.fn();

    await pasteVNCClipboardText(
      { clipboardPasteFrom: vi.fn(), sendKey },
      "中文\r\n下一行",
      vi.fn().mockRejectedValue(new Error("not representable in GBK"))
    );

    expect(sendKey).toHaveBeenCalledWith(0xff0d, "");
    expect(sendKey).toHaveBeenCalledTimes(6);
  });

  it("reports whether the text was placed on the clipboard so the caller can gate Ctrl+V", async () => {
    const setViaGbk = await pasteVNCClipboardText(
      { clipboardPasteFrom: vi.fn(), sendKey: vi.fn() },
      "中文",
      vi.fn().mockResolvedValue([0xd6, 0xd0, 0xce, 0xc4])
    );
    expect(setViaGbk).toBe(true);

    const setViaExtended = await pasteVNCClipboardText(
      {
        clipboardPasteFrom: vi.fn(),
        sendKey: vi.fn(),
        _clipboardServerCapabilitiesFormats: { 1: true },
        _clipboardServerCapabilitiesActions: { [1 << 27]: true },
      },
      "中文"
    );
    expect(setViaExtended).toBe(true);

    const typedDirectly = await pasteVNCClipboardText(
      { clipboardPasteFrom: vi.fn(), sendKey: vi.fn() },
      "😀",
      vi.fn().mockRejectedValue(new Error("not representable in GBK"))
    );
    expect(typedDirectly).toBe(false);
  });

  it("decodes UTF-8 bytes received through the legacy clipboard path", () => {
    const wireText = String.fromCharCode(0xe4, 0xb8, 0xad, 0xe6, 0x96, 0x87, 0x61, 0x62, 0x63);

    expect(decodeVNCClipboardText(wireText)).toBe("中文abc");
  });

  it("decodes GBK bytes received from Chinese Windows RealVNC", () => {
    const wireText = String.fromCharCode(0x61, 0x62, 0x63, 0xd6, 0xd0, 0xce, 0xc4, 0x58, 0x59, 0x5a);

    expect(decodeVNCClipboardText(wireText)).toBe("abc中文XYZ");
  });

  it("preserves legacy Latin-1 when the byte sequence is not valid UTF-8", () => {
    expect(decodeVNCClipboardText("caf\u00e9")).toBe("caf\u00e9");
  });
});

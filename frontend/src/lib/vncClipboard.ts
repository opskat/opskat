interface VNCClipboardClient {
  clipboardPasteFrom(text: string): void;
  sendKey(keysym: number, code: string): void;
  _clipboardServerCapabilitiesFormats?: Record<number, boolean>;
  _clipboardServerCapabilitiesActions?: Record<number, boolean>;
}

type LegacyClipboardEncoder = (text: string) => Promise<number[]>;

const EXTENDED_CLIPBOARD_FORMAT_TEXT = 1;
const EXTENDED_CLIPBOARD_ACTION_NOTIFY = 1 << 27;

function supportsExtendedTextClipboard(client: VNCClipboardClient): boolean {
  return Boolean(
    client._clipboardServerCapabilitiesFormats?.[EXTENDED_CLIPBOARD_FORMAT_TEXT] &&
    client._clipboardServerCapabilitiesActions?.[EXTENDED_CLIPBOARD_ACTION_NOTIFY]
  );
}

function unicodeToKeysym(codePoint: number): number {
  if (codePoint === 0x0a || codePoint === 0x0d) return 0xff0d;
  if (codePoint === 0x09) return 0xff09;
  if (codePoint <= 0xff) return codePoint;
  return 0x01000000 | codePoint;
}

function bytesToBinaryString(bytes: number[]): string {
  let result = "";
  const chunkSize = 0x8000;
  for (let offset = 0; offset < bytes.length; offset += chunkSize) {
    result += String.fromCharCode(...bytes.slice(offset, offset + chunkSize));
  }
  return result;
}

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => window.setTimeout(resolve, ms));
}

export async function pasteVNCClipboardText(
  client: VNCClipboardClient,
  text: string,
  encodeLegacy?: LegacyClipboardEncoder
): Promise<void> {
  if (supportsExtendedTextClipboard(client) || Array.from(text).every((char) => char.codePointAt(0)! <= 0xff)) {
    client.clipboardPasteFrom(text);
    return;
  }

  if (encodeLegacy) {
    try {
      client.clipboardPasteFrom(bytesToBinaryString(await encodeLegacy(text)));
      return;
    } catch {
      // Characters outside GBK fall back to Unicode keysyms below.
    }
  }

  for (const char of text.replace(/\r\n?/g, "\n")) {
    client.sendKey(unicodeToKeysym(char.codePointAt(0)!), "");
    await delay(8);
  }
}

export function decodeVNCClipboardText(text: string): string {
  if (!text || Array.from(text).some((char) => char.codePointAt(0)! > 0xff)) {
    return text;
  }

  const bytes = Uint8Array.from(text, (char) => char.charCodeAt(0));
  try {
    return new TextDecoder("utf-8", { fatal: true }).decode(bytes);
  } catch {
    try {
      return new TextDecoder("gb18030", { fatal: true }).decode(bytes);
    } catch {
      return text;
    }
  }
}

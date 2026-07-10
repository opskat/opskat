/** Side-effect-free frame payload decoding for the RDP viewer. */

interface Uint8ArrayWithFromBase64 {
  fromBase64?: (data: string) => Uint8Array<ArrayBuffer>;
}

/** Fallback decoder: atob + per-byte copy (~16ms for a full 1080p frame).
 * With dirty-region streaming it only handles small payloads, so the cost is
 * acceptable on WebKit versions without Uint8Array.fromBase64. */
export function decodeBase64Fallback(data: string): Uint8ClampedArray<ArrayBuffer> {
  const bin = atob(data);
  const out = new Uint8ClampedArray(new ArrayBuffer(bin.length));
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

/** Decode a base64 RGBA frame payload, preferring the native (fast)
 * Uint8Array.fromBase64 when the runtime provides it. */
export function decodeFrameBytes(data: string): Uint8ClampedArray<ArrayBuffer> {
  const fromBase64 = (Uint8Array as Uint8ArrayWithFromBase64).fromBase64;
  if (typeof fromBase64 === "function") {
    const bytes = fromBase64(data);
    return new Uint8ClampedArray(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  }
  return decodeBase64Fallback(data);
}

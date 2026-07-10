import { describe, expect, it } from "vitest";
import { decodeBase64Fallback, decodeFrameBytes } from "../rdpFrame";

function sampleBytes(length: number): Uint8Array {
  const bytes = new Uint8Array(length);
  for (let i = 0; i < length; i++) bytes[i] = (i * 7) & 0xff;
  return bytes;
}

function toBase64(bytes: Uint8Array): string {
  return btoa(String.fromCharCode(...bytes));
}

describe("decodeBase64Fallback", () => {
  it("decodes base64 back to the original bytes", () => {
    const bytes = sampleBytes(64 * 4);
    expect(Array.from(decodeBase64Fallback(toBase64(bytes)))).toEqual(Array.from(bytes));
  });

  it("decodes an empty payload to an empty array", () => {
    expect(decodeBase64Fallback("").length).toBe(0);
  });
});

describe("decodeFrameBytes", () => {
  it("matches the fallback decoder regardless of the native path", () => {
    const bytes = sampleBytes(257); // non-multiple-of-3 length exercises padding
    const b64 = toBase64(bytes);
    expect(Array.from(decodeFrameBytes(b64))).toEqual(Array.from(decodeBase64Fallback(b64)));
  });

  it("returns a Uint8ClampedArray usable for ImageData", () => {
    const out = decodeFrameBytes(toBase64(sampleBytes(16)));
    expect(out).toBeInstanceOf(Uint8ClampedArray);
    expect(out.length).toBe(16);
  });
});

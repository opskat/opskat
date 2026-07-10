/** Remote-cursor rendering for the RDP viewer. Cursor shapes arrive as
 * pointer events (RDP never draws the cursor into the framebuffer), and the
 * viewer shows them by swapping the CSS cursor of the canvas. */

import { decodeFrameBytes } from "./rdpFrame";

export interface RDPPointerPayload {
  pointerType?: string;
  hotspotX?: number;
  hotspotY?: number;
  width?: number;
  height?: number;
  data?: string;
}

/** Rasterize top-down RGBA cursor pixels into a PNG data URL via an
 * offscreen canvas. Returns null when the runtime has no 2D canvas. */
export function rgbaCursorDataURL(bytes: Uint8ClampedArray<ArrayBuffer>, width: number, height: number): string | null {
  const canvas = document.createElement("canvas");
  canvas.width = width;
  canvas.height = height;
  const ctx = canvas.getContext("2d");
  if (!ctx) return null;
  ctx.putImageData(new ImageData(bytes, width, height), 0, 0);
  return canvas.toDataURL();
}

/** Map a pointer event to the CSS cursor value of the RDP canvas: "none" to
 * hide, an image cursor anchored at the remote hotspot for a shape, and the
 * default arrow otherwise (including malformed shape payloads). */
export function pointerCursorStyle(
  evt: RDPPointerPayload,
  makeDataURL: (
    bytes: Uint8ClampedArray<ArrayBuffer>,
    width: number,
    height: number
  ) => string | null = rgbaCursorDataURL
): string {
  if (evt.pointerType === "hidden") return "none";
  if (evt.pointerType === "shape" && evt.data && evt.width && evt.height) {
    const bytes = decodeFrameBytes(evt.data);
    if (bytes.length === evt.width * evt.height * 4) {
      const url = makeDataURL(bytes, evt.width, evt.height);
      if (url) return `url(${url}) ${evt.hotspotX || 0} ${evt.hotspotY || 0}, default`;
    }
  }
  return "default";
}

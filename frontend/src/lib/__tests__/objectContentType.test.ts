import { describe, it, expect } from "vitest";
import { FileImage, FileVideo, FileJson, File as FileIcon } from "lucide-react";
import { isImage, typeIcon } from "../objectContentType";

describe("isImage", () => {
  it("detects image content-types", () => {
    expect(isImage("image/png")).toBe(true);
    expect(isImage("image/jpeg")).toBe(true);
    expect(isImage("application/octet-stream")).toBe(false);
    expect(isImage("")).toBe(false);
  });
  it("falls back to the key extension when content-type is blank", () => {
    expect(isImage("", "photos/a.PNG")).toBe(true);
    expect(isImage("", "docs/a.txt")).toBe(false);
    expect(isImage("application/octet-stream", "a.webp")).toBe(true);
  });
});

describe("typeIcon", () => {
  it("maps families and defaults to a generic file icon", () => {
    expect(typeIcon("image/png", "a.png")).toBe(FileImage);
    expect(typeIcon("video/mp4", "a.mp4")).toBe(FileVideo);
    expect(typeIcon("application/json", "a.json")).toBe(FileJson);
    expect(typeIcon("", "a.json")).toBe(FileJson);
    expect(typeIcon("application/octet-stream", "a.bin")).toBe(FileIcon);
  });
});

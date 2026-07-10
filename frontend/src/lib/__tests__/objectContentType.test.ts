import { describe, it, expect } from "vitest";
import {
  Database,
  FileArchive,
  FileCode,
  FileCog,
  FileImage,
  FileJson,
  FileKey,
  FileSpreadsheet,
  FileText,
  FileType,
  FileVideo,
  Presentation,
  File as FileIcon,
} from "lucide-react";
import { isImage, typeIcon, typeIconColor } from "../objectContentType";

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

  it.each([
    ["application/pdf", "report.pdf", FileText],
    ["application/octet-stream", "backup.tar.gz", FileArchive],
    ["application/octet-stream", "sheet.xlsx", FileSpreadsheet],
    ["text/csv", "data.csv", FileSpreadsheet],
    ["application/vnd.ms-powerpoint", "slides.ppt", Presentation],
    ["application/octet-stream", "main.tsx", FileCode],
    ["text/plain", ".env.production", FileCog],
    ["application/x-sqlite3", "cache.sqlite3", Database],
    ["application/octet-stream", "font.woff2", FileType],
    ["application/x-pem-file", "server.pem", FileKey],
  ])("maps %s / %s to a specific icon", (contentType, key, icon) => {
    expect(typeIcon(contentType, key)).toBe(icon);
  });

  it("uses design-system category tokens for icon colors", () => {
    expect(typeIconColor("image/png", "a.png")).toBe("text-chart-2");
    expect(typeIconColor("application/pdf", "a.pdf")).toBe("text-destructive");
    expect(typeIconColor("text/csv", "a.csv")).toBe("text-success");
    expect(typeIconColor("application/octet-stream", "main.go")).toBe("text-info");
    expect(typeIconColor("application/octet-stream", "a.bin")).toBe("text-muted-foreground");
  });
});

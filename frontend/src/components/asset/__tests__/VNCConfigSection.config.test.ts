import { describe, expect, it } from "vitest";
import { parseVNCConfig, parseVNCPasswordCredentialConfig } from "@/components/asset/VNCConfigSection.config";

describe("VNC config parsing", () => {
  it("surfaces malformed saved config instead of replacing it with defaults", () => {
    expect(() => parseVNCConfig("{bad-json")).toThrow();
    expect(() => parseVNCPasswordCredentialConfig("{bad-json")).toThrow();
  });
});

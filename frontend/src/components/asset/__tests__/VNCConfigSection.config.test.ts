import { describe, expect, it } from "vitest";
import {
  buildVNCConfig,
  parseVNCConfig,
  parseVNCPasswordCredentialConfig,
  VNC_DEFAULTS,
} from "@/components/asset/VNCConfigSection.config";
import type { VNCEncryptionPolicy } from "@/lib/vncSecurity";

describe("VNC config parsing", () => {
  it("surfaces malformed saved config instead of replacing it with defaults", () => {
    expect(() => parseVNCConfig("{bad-json")).toThrow();
    expect(() => parseVNCPasswordCredentialConfig("{bad-json")).toThrow();
  });

  it("treats missing and empty encryption as server policy", () => {
    expect(parseVNCConfig('{"host":"vnc.example.com"}').encryption).toBe("server");
    expect(parseVNCConfig('{"host":"vnc.example.com","encryption":""}').encryption).toBe("server");
  });

  it("rejects an unknown saved encryption token", () => {
    expect(() => parseVNCConfig('{"host":"vnc.example.com","encryption":"downgrade"}')).toThrow(/downgrade/);
  });

  it("round-trips all five encryption tokens and omits the server default", async () => {
    const policies: VNCEncryptionPolicy[] = ["server", "always_maximum", "always_on", "prefer_on", "prefer_off"];
    for (const encryption of policies) {
      const encoded = await buildVNCConfig(
        { ...VNC_DEFAULTS, host: "vnc.example.com", encryption },
        {},
        async (plain) => plain
      );
      const json = JSON.parse(encoded);
      if (encryption === "server") expect(json).not.toHaveProperty("encryption");
      else expect(json.encryption).toBe(encryption);
      expect(parseVNCConfig(encoded).encryption).toBe(encryption);
    }
  });
});

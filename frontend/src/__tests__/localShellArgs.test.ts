import { describe, expect, it } from "vitest";
import { formatLocalShellArgs, parseLocalShellArgs } from "@/lib/localShellArgs";

describe("localShellArgs", () => {
  it("parses whitespace-separated args", () => {
    expect(parseLocalShellArgs("  --login   -i  ")).toEqual(["--login", "-i"]);
  });

  it("preserves spaces inside quoted args", () => {
    expect(parseLocalShellArgs('-d "Ubuntu 22.04 LTS"')).toEqual(["-d", "Ubuntu 22.04 LTS"]);
    expect(parseLocalShellArgs("bash '/Users/me/My Scripts/start.sh'")).toEqual([
      "bash",
      "/Users/me/My Scripts/start.sh",
    ]);
  });

  it("supports escaped spaces and quotes", () => {
    expect(parseLocalShellArgs(String.raw`--name Ubuntu\ 22 \"quoted\"`)).toEqual(["--name", "Ubuntu 22", '"quoted"']);
    expect(parseLocalShellArgs(String.raw`"C:\Program Files\Git\bin\bash.exe"`)).toEqual([
      String.raw`C:\Program Files\Git\bin\bash.exe`,
    ]);
  });

  it("keeps unquoted Windows paths intact", () => {
    expect(parseLocalShellArgs(String.raw`--profile C:\Users\me\profile.ps1`)).toEqual([
      "--profile",
      String.raw`C:\Users\me\profile.ps1`,
    ]);
  });

  it("keeps empty quoted args", () => {
    expect(parseLocalShellArgs(`--empty "" ''`)).toEqual(["--empty", "", ""]);
  });

  it("rejects invalid quoting", () => {
    expect(() => parseLocalShellArgs(`"Ubuntu`)).toThrow("unclosed quote");
    expect(() => parseLocalShellArgs(`Ubuntu\\`)).toThrow("unfinished escape");
  });

  it("formats args so parsing round-trips spaces and quotes", () => {
    const args = ["-d", "Ubuntu 22.04 LTS", `C:\\Program Files\\Git\\bin\\bash.exe`, `say "hi"`, ""];
    expect(parseLocalShellArgs(formatLocalShellArgs(args))).toEqual(args);
  });
});

import { describe, it, expect } from "vitest";
import { buildLocalConfig, parseLocalConfig, LOCAL_DEFAULTS } from "@/components/asset/LocalConfigSection";

describe("buildLocalConfig (锁旧 handleSubmit local 分支字节一致)", () => {
  it("shell+args+cwd 全有", () => {
    expect(buildLocalConfig({ shell: "/bin/zsh", args: "-l", cwd: "~" })).toBe(
      '{"shell":"/bin/zsh","args":["-l"],"cwd":"~"}'
    );
  });
  it("空 shell/args 省略,保留 cwd", () => {
    expect(buildLocalConfig({ shell: "", args: "", cwd: "~" })).toBe('{"cwd":"~"}');
  });
  it("空 cwd 省略", () => {
    expect(buildLocalConfig({ shell: "/bin/sh", args: "", cwd: "" })).toBe('{"shell":"/bin/sh"}');
  });
  it("args 非法时抛错(由调用方 toast)", () => {
    expect(() => buildLocalConfig({ shell: "", args: '"abc', cwd: "" })).toThrow("unclosed quote");
  });
});

describe("parseLocalConfig (锁旧 loadLocalConfig)", () => {
  it("回填 shell/args/cwd", () => {
    expect(parseLocalConfig('{"shell":"/bin/zsh","args":["-l","-i"],"cwd":"/root"}')).toEqual({
      shell: "/bin/zsh",
      args: "-l -i",
      cwd: "/root",
    });
  });
  it("缺字段用默认(cwd 缺→~)", () => {
    expect(parseLocalConfig("{}")).toEqual({ shell: "", args: "", cwd: "~" });
  });
  it("非法 JSON 回退默认", () => {
    expect(parseLocalConfig("not json")).toEqual(LOCAL_DEFAULTS);
  });
});

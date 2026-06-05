import { formatLocalShellArgs, parseLocalShellArgs } from "@/lib/localShellArgs";

export interface LocalFormState {
  shell: string;
  args: string;
  cwd: string;
}

export const LOCAL_DEFAULTS: LocalFormState = { shell: "", args: "", cwd: "~" };

/** 保存序列化:镜像旧 handleSubmit 的 local 分支(行为保持)。args 非法→抛(调用方 toast)。 */
export function buildLocalConfig(state: LocalFormState): string {
  const cfg: Record<string, unknown> = {};
  if (state.shell) cfg.shell = state.shell;
  const argList = parseLocalShellArgs(state.args);
  if (argList.length) cfg.args = argList;
  if (state.cwd) cfg.cwd = state.cwd;
  return JSON.stringify(cfg);
}

/** 编辑态回填:镜像旧 loadLocalConfig。解析失败→默认值。 */
export function parseLocalConfig(configJSON: string): LocalFormState {
  try {
    const cfg = JSON.parse(configJSON || "{}");
    return {
      shell: cfg.shell || "",
      args: formatLocalShellArgs(cfg.args || []),
      cwd: cfg.cwd || "~",
    };
  } catch {
    return { ...LOCAL_DEFAULTS };
  }
}

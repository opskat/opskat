import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Input, Label, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@opskat/ui";
import { ListLocalShells } from "../../../wailsjs/go/local/Local";
import type { localterm_svc } from "../../../wailsjs/go/models";
import { formatLocalShellArgs, parseLocalShellArgs } from "@/lib/localShellArgs";

type ShellInfo = localterm_svc.ShellInfo;

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

export interface LocalConfigSectionProps {
  shell: string;
  setShell: (v: string) => void;
  args: string;
  setArgs: (v: string) => void;
  cwd: string;
  setCwd: (v: string) => void;
}

export function LocalConfigSection({ shell, setShell, args, setArgs, cwd, setCwd }: LocalConfigSectionProps) {
  const { t } = useTranslation();
  const [shells, setShells] = useState<ShellInfo[]>([]);

  useEffect(() => {
    ListLocalShells()
      .then((list) => setShells(list || []))
      .catch(() => setShells([]));
  }, []);

  const onSelectPreset = (val: string) => {
    if (val === "__default__") {
      setShell("");
      setArgs("");
      return;
    }
    const s = shells[Number(val)];
    if (s) {
      setShell(s.path);
      setArgs(formatLocalShellArgs(s.args || []));
    }
  };

  return (
    <div className="grid gap-3 border rounded-lg p-4">
      <div className="grid gap-2">
        <Label>{t("asset.localShell")}</Label>
        <Select onValueChange={onSelectPreset}>
          <SelectTrigger>
            <SelectValue placeholder={t("asset.localShellPreset")} />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="__default__">{t("asset.localDefaultShell")}</SelectItem>
            {shells.map((s, i) => (
              <SelectItem key={`${s.path}-${i}`} value={String(i)}>
                {s.name}
                {s.args && s.args.length ? ` (${s.path} ${formatLocalShellArgs(s.args)})` : ` (${s.path})`}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Input
          value={shell}
          onChange={(e) => setShell(e.target.value)}
          placeholder={t("asset.localShellPlaceholder")}
          className="font-mono"
        />
      </div>
      <div className="grid gap-2">
        <Label>{t("asset.localArgs")}</Label>
        <Input
          value={args}
          onChange={(e) => setArgs(e.target.value)}
          placeholder={t("asset.localArgsPlaceholder")}
          className="font-mono"
        />
      </div>
      <div className="grid gap-2">
        <Label>{t("asset.localCwd")}</Label>
        <Input
          value={cwd}
          onChange={(e) => setCwd(e.target.value)}
          placeholder={t("asset.localCwdPlaceholder")}
          className="font-mono"
        />
      </div>
    </div>
  );
}

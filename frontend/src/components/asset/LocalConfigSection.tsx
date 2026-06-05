import { forwardRef, useEffect, useImperativeHandle, useState } from "react";
import { useTranslation } from "react-i18next";
import { Input, Label, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@opskat/ui";
import { ListLocalShells } from "../../../wailsjs/go/local/Local";
import type { localterm_svc } from "../../../wailsjs/go/models";
import { formatLocalShellArgs } from "@/lib/localShellArgs";
import type { AssetFormHandle, ConfigSectionProps } from "@/lib/assetTypes/formContract";
import { buildLocalConfig, parseLocalConfig, LOCAL_DEFAULTS, type LocalFormState } from "./LocalConfigSection.config";

type ShellInfo = localterm_svc.ShellInfo;

export const LocalConfigSection = forwardRef<AssetFormHandle, ConfigSectionProps>(function LocalConfigSection(
  { editAsset, onValidityChange },
  ref
) {
  const { t } = useTranslation();
  const [shells, setShells] = useState<ShellInfo[]>([]);
  const [state, setState] = useState<LocalFormState>(() =>
    editAsset ? parseLocalConfig(editAsset.Config) : { ...LOCAL_DEFAULTS }
  );

  useEffect(() => {
    ListLocalShells()
      .then((list) => setShells(list || []))
      .catch(() => setShells([]));
  }, []);

  // local 无必填校验:始终可保存、不可测试(onValidityChange 为壳 setState,身份稳定)。
  useEffect(() => {
    onValidityChange({ canTest: false, canSave: true });
  }, [onValidityChange]);

  useImperativeHandle(
    ref,
    () => ({
      buildConfig: async () => ({ configJSON: buildLocalConfig(state), sshTunnelId: 0 }),
      buildTestConfig: null,
    }),
    [state]
  );

  const patch = (p: Partial<LocalFormState>) => setState((s) => ({ ...s, ...p }));

  const onSelectPreset = (val: string) => {
    if (val === "__default__") {
      patch({ shell: "", args: "" });
      return;
    }
    const s = shells[Number(val)];
    if (s) patch({ shell: s.path, args: formatLocalShellArgs(s.args || []) });
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
          value={state.shell}
          onChange={(e) => patch({ shell: e.target.value })}
          placeholder={t("asset.localShellPlaceholder")}
          className="font-mono"
        />
      </div>
      <div className="grid gap-2">
        <Label>{t("asset.localArgs")}</Label>
        <Input
          value={state.args}
          onChange={(e) => patch({ args: e.target.value })}
          placeholder={t("asset.localArgsPlaceholder")}
          className="font-mono"
        />
      </div>
      <div className="grid gap-2">
        <Label>{t("asset.localCwd")}</Label>
        <Input
          value={state.cwd}
          onChange={(e) => patch({ cwd: e.target.value })}
          placeholder={t("asset.localCwdPlaceholder")}
          className="font-mono"
        />
      </div>
    </div>
  );
});

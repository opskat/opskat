import { forwardRef, useEffect, useImperativeHandle, useState } from "react";
import { useTranslation } from "react-i18next";
import { Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@opskat/ui";
import { Field } from "@/components/asset/fields";
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
    <div className="flex flex-col gap-4">
      <Field label={t("asset.localShell")}>
        <Select onValueChange={onSelectPreset}>
          <SelectTrigger className="w-full">
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
      </Field>
      <Field label={t("asset.localArgs")}>
        <Input
          value={state.args}
          onChange={(e) => patch({ args: e.target.value })}
          placeholder={t("asset.localArgsPlaceholder")}
          className="font-mono"
        />
      </Field>
      <Field label={t("asset.localCwd")}>
        <Input
          value={state.cwd}
          onChange={(e) => patch({ cwd: e.target.value })}
          placeholder={t("asset.localCwdPlaceholder")}
          className="font-mono"
        />
      </Field>
    </div>
  );
});

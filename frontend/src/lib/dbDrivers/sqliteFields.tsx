import { useTranslation } from "react-i18next";
import { Button, Input } from "@opskat/ui";
import { Field, Segmented } from "@/components/asset/fields";
import { AssetSelect } from "@/components/asset/AssetSelect";
import type { DatabaseFormState } from "@/components/asset/DatabaseConfigSection.config";
import { SelectSQLiteFile } from "../../../wailsjs/go/system/System";
import { registerDriverFormField } from "./formFields";

type Patch = (p: Partial<DatabaseFormState>) => void;

/** 数据库文件的来源:本机,或经 SSH 资产读取的远端文件。 */
export function SQLiteSourceField({ state, patch }: { state: DatabaseFormState; patch: Patch }) {
  const { t } = useTranslation();
  return (
    <Field label={t("asset.sqliteSource")}>
      <Segmented
        value={state.sqliteSource}
        onChange={(v) => {
          if (v === "remote_ssh_vfs") {
            patch({ sqliteSource: "remote_ssh_vfs", connectionType: "jumphost" });
          } else {
            patch({ sqliteSource: "local", sshTunnelId: 0, connectionType: "direct" });
          }
        }}
        aria-label={t("asset.sqliteSource")}
        options={[
          { value: "local", label: t("asset.sqliteSourceLocal"), testid: "database-sqlite-source-option-local" },
          {
            value: "remote_ssh_vfs",
            label: t("asset.sqliteSourceRemoteSSH"),
            testid: "database-sqlite-source-option-remote",
          },
        ]}
      />
    </Field>
  );
}

/** 数据库文件路径;本机文件可用系统对话框选择。 */
export function SQLitePathField({ state, patch }: { state: DatabaseFormState; patch: Patch }) {
  const { t } = useTranslation();
  const isRemote = state.sqliteSource === "remote_ssh_vfs";
  return (
    <Field label={t("asset.sqliteFilePath")}>
      <div className="flex gap-2">
        <Input
          data-testid="database-sqlite-path-input"
          value={state.path}
          onChange={(e) => patch({ path: e.target.value })}
          placeholder={isRemote ? "/var/lib/app/app.db" : t("asset.sqliteFilePathPlaceholder")}
        />
        {!isRemote && (
          <Button
            type="button"
            variant="secondary"
            onClick={async () => {
              const selected = await SelectSQLiteFile();
              if (selected) patch({ path: selected });
            }}
          >
            {t("asset.sqliteFilePathBrowse")}
          </Button>
        )}
      </div>
    </Field>
  );
}

/** 远端文件所在的 SSH 资产。 */
export function SQLiteSSHAssetField({ state, patch }: { state: DatabaseFormState; patch: Patch }) {
  const { t } = useTranslation();
  return (
    <Field label={t("asset.sqliteRemoteSSH")}>
      <AssetSelect
        value={state.sshTunnelId}
        onValueChange={(v) => patch({ sshTunnelId: v, connectionType: "jumphost" })}
        filterType="ssh"
        placeholder={t("asset.jumpHostNone")}
        testId="database-sqlite-ssh-select"
      />
    </Field>
  );
}

registerDriverFormField("sqlite_source", [
  { kind: "custom", render: (state, patch) => <SQLiteSourceField state={state} patch={patch} /> },
]);
registerDriverFormField("path", [
  { kind: "custom", render: (state, patch) => <SQLitePathField state={state} patch={patch} /> },
]);
registerDriverFormField("ssh_asset_id", [
  {
    kind: "custom",
    visibleWhen: (s) => s.sqliteSource === "remote_ssh_vfs",
    render: (state, patch) => <SQLiteSSHAssetField state={state} patch={patch} />,
  },
]);

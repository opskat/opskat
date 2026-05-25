import { useTranslation } from "react-i18next";
import { Input, Label, Switch, Textarea } from "@opskat/ui";
import { AssetSelect } from "@/components/asset/AssetSelect";
import { PasswordSourceField } from "@/components/asset/PasswordSourceField";
import { credential_entity } from "../../../wailsjs/go/models";

export interface EtcdConfigSectionProps {
  endpoints: string;
  setEndpoints: (v: string) => void;
  username: string;
  setUsername: (v: string) => void;
  password: string;
  setPassword: (v: string) => void;
  encryptedPassword: string;
  passwordSource: "inline" | "managed";
  setPasswordSource: (v: "inline" | "managed") => void;
  passwordCredentialId: number;
  setPasswordCredentialId: (v: number) => void;
  managedPasswords: credential_entity.Credential[];
  editAssetId?: number;
  tls: boolean;
  setTls: (v: boolean) => void;
  tlsInsecure: boolean;
  setTlsInsecure: (v: boolean) => void;
  tlsServerName: string;
  setTlsServerName: (v: string) => void;
  tlsCAFile: string;
  setTlsCAFile: (v: string) => void;
  tlsCertFile: string;
  setTlsCertFile: (v: string) => void;
  tlsKeyFile: string;
  setTlsKeyFile: (v: string) => void;
  dialTimeoutSeconds: number;
  setDialTimeoutSeconds: (v: number) => void;
  commandTimeoutSeconds: number;
  setCommandTimeoutSeconds: (v: number) => void;
  sshTunnelId: number;
  setSshTunnelId: (v: number) => void;
}

export function EtcdConfigSection({
  endpoints,
  setEndpoints,
  username,
  setUsername,
  password,
  setPassword,
  encryptedPassword,
  passwordSource,
  setPasswordSource,
  passwordCredentialId,
  setPasswordCredentialId,
  managedPasswords,
  editAssetId,
  tls,
  setTls,
  tlsInsecure,
  setTlsInsecure,
  tlsServerName,
  setTlsServerName,
  tlsCAFile,
  setTlsCAFile,
  tlsCertFile,
  setTlsCertFile,
  tlsKeyFile,
  setTlsKeyFile,
  dialTimeoutSeconds,
  setDialTimeoutSeconds,
  commandTimeoutSeconds,
  setCommandTimeoutSeconds,
  sshTunnelId,
  setSshTunnelId,
}: EtcdConfigSectionProps) {
  const { t } = useTranslation();

  return (
    <>
      {/* Connection & Auth (single visual block) */}
      <div className="grid gap-3 border rounded-lg p-3">
        <div className="grid gap-2">
          <Label>{t("etcd.form.endpoints")}</Label>
          <Textarea
            value={endpoints}
            onChange={(e) => setEndpoints(e.target.value)}
            rows={3}
            className="font-mono text-sm"
            placeholder={"10.0.0.1:2379\n10.0.0.2:2379"}
          />
          <p className="text-xs text-muted-foreground">{t("etcd.form.endpointsHint")}</p>
        </div>

        <div className="grid gap-2">
          <Label>{t("asset.username")}</Label>
          <Input value={username} onChange={(e) => setUsername(e.target.value)} />
        </div>

        <PasswordSourceField
          source={passwordSource}
          onSourceChange={setPasswordSource}
          password={password}
          onPasswordChange={setPassword}
          credentialId={passwordCredentialId}
          onCredentialIdChange={setPasswordCredentialId}
          managedPasswords={managedPasswords}
          hasExistingPassword={!!encryptedPassword}
          editAssetId={editAssetId}
          onUsernameChange={setUsername}
        />
      </div>

      {/* TLS */}
      <div className="flex items-center justify-between">
        <Label>{t("asset.tls")}</Label>
        <Switch checked={tls} onCheckedChange={setTls} />
      </div>

      {tls && (
        <>
          <div className="flex items-center justify-between">
            <Label>{t("etcd.form.tlsInsecure")}</Label>
            <Switch checked={tlsInsecure} onCheckedChange={setTlsInsecure} />
          </div>

          <div className="grid gap-2">
            <Label>{t("etcd.form.tlsServerName")}</Label>
            <Input
              value={tlsServerName}
              onChange={(e) => setTlsServerName(e.target.value)}
              placeholder="etcd.example.com"
            />
          </div>

          <div className="grid gap-2">
            <Label>{t("etcd.form.tlsCAFile")}</Label>
            <Input value={tlsCAFile} onChange={(e) => setTlsCAFile(e.target.value)} placeholder="/path/to/ca.pem" />
          </div>

          <div className="grid gap-2">
            <Label>{t("etcd.form.tlsCertFile")}</Label>
            <Input
              value={tlsCertFile}
              onChange={(e) => setTlsCertFile(e.target.value)}
              placeholder="/path/to/client.crt"
            />
          </div>

          <div className="grid gap-2">
            <Label>{t("etcd.form.tlsKeyFile")}</Label>
            <Input
              value={tlsKeyFile}
              onChange={(e) => setTlsKeyFile(e.target.value)}
              placeholder="/path/to/client.key"
            />
          </div>
        </>
      )}

      <div className="grid grid-cols-2 gap-3">
        <div className="grid gap-2">
          <Label>{t("etcd.form.dialTimeout")}</Label>
          <Input
            className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
            type="number"
            min={0}
            value={dialTimeoutSeconds}
            onChange={(e) => setDialTimeoutSeconds(Math.max(0, Number(e.target.value) || 0))}
          />
        </div>
        <div className="grid gap-2">
          <Label>{t("etcd.form.commandTimeout")}</Label>
          <Input
            className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
            type="number"
            min={0}
            value={commandTimeoutSeconds}
            onChange={(e) => setCommandTimeoutSeconds(Math.max(0, Number(e.target.value) || 0))}
          />
        </div>
      </div>

      <div className="grid gap-2">
        <Label>{t("asset.sshTunnel")}</Label>
        <AssetSelect
          value={sshTunnelId}
          onValueChange={setSshTunnelId}
          filterType="ssh"
          placeholder={t("asset.sshTunnelNone")}
        />
      </div>
    </>
  );
}

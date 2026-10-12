import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { ClipboardPaste, FolderOpen } from "lucide-react";
import { Button, Input, Textarea } from "@opskat/ui";
import { Field, FieldLabel, Segmented } from "@/components/asset/fields";
import type { TLSCertFormFields, TLSCertSource } from "@/components/asset/tlsCertConfig";
import { SelectTLSCertFile } from "../../../wailsjs/go/system/System";

interface TLSCertFieldsetProps {
  state: TLSCertFormFields;
  patch: (p: Partial<TLSCertFormFields>) => void;
}

type PathKey = "tlsCAFile" | "tlsCertFile" | "tlsKeyFile";
type PEMKey = "tlsCAPEM" | "tlsCertPEM" | "tlsKeyPEM";

/**
 * TLS 证书材料的输入区:标题行右侧切换来源,下面是 CA 证书一行、客户端证书与私钥成对一行。
 * 路径可以手填,也可以点输入框里的文件夹图标从系统文件框里选;证书内容的文本框定高滚动,
 * 贴进一整张证书也不会把表单撑长。
 */
export function TLSCertFieldset({ state, patch }: TLSCertFieldsetProps) {
  const { t } = useTranslation();
  const fromFile = state.tlsCertSource === "file";

  const pathInput = (key: PathKey, testid: string, placeholder: string) => (
    <div className="relative">
      <Input
        data-testid={testid}
        value={state[key]}
        onChange={(e) => patch({ [key]: e.target.value })}
        placeholder={placeholder}
        spellCheck={false}
        className="pr-9 font-mono text-xs md:text-xs"
      />
      <Button
        type="button"
        variant="ghost"
        size="icon"
        className="absolute right-1 top-1/2 h-7 w-7 -translate-y-1/2 text-muted-foreground hover:text-foreground"
        data-testid={`${testid}-browse`}
        aria-label={t("asset.tlsCertBrowse")}
        title={t("asset.tlsCertBrowse")}
        onClick={async () => {
          try {
            const selected = await SelectTLSCertFile();
            if (selected) patch({ [key]: selected });
          } catch (e) {
            toast.error(String(e));
          }
        }}
      >
        <FolderOpen className="h-3.5 w-3.5" aria-hidden />
      </Button>
    </div>
  );

  const pemInput = (key: PEMKey, testid: string, placeholder: string) => (
    <Textarea
      data-testid={testid}
      value={state[key]}
      onChange={(e) => patch({ [key]: e.target.value })}
      placeholder={placeholder}
      spellCheck={false}
      className="h-[88px] resize-none field-sizing-fixed overflow-y-auto break-all font-mono text-[11px] leading-relaxed md:text-[11px]"
    />
  );

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between gap-4">
        <div className="flex min-w-0 flex-col gap-0.5">
          <FieldLabel>{t("asset.tlsCertSource")}</FieldLabel>
          <span className="text-[11px] leading-snug text-muted-foreground/70">
            {t(fromFile ? "asset.tlsCertSourceFileHint" : "asset.tlsCertSourcePEMHint")}
          </span>
        </div>
        <Segmented<TLSCertSource>
          value={state.tlsCertSource}
          onChange={(tlsCertSource) => patch({ tlsCertSource })}
          aria-label={t("asset.tlsCertSource")}
          className="h-8 w-auto shrink-0 [&>button]:px-3 [&>button]:text-xs [&>button]:whitespace-nowrap"
          options={[
            { value: "pem", label: t("asset.tlsCertSourcePEM"), icon: ClipboardPaste, testid: "tls-cert-source-pem" },
            { value: "file", label: t("asset.tlsCertSourceFile"), icon: FolderOpen, testid: "tls-cert-source-file" },
          ]}
        />
      </div>

      {fromFile ? (
        <>
          <Field label={t("asset.tlsCACert")}>{pathInput("tlsCAFile", "tls-ca-file", "/path/to/ca.pem")}</Field>
          <div className="flex gap-3">
            <Field label={t("asset.tlsClientCert")} className="min-w-0 flex-1">
              {pathInput("tlsCertFile", "tls-cert-file", "/path/to/client.crt")}
            </Field>
            <Field label={t("asset.tlsClientKey")} className="min-w-0 flex-1">
              {pathInput("tlsKeyFile", "tls-key-file", "/path/to/client.key")}
            </Field>
          </div>
        </>
      ) : (
        <>
          <Field label={t("asset.tlsCACert")}>
            {pemInput("tlsCAPEM", "tls-ca-pem", "-----BEGIN CERTIFICATE-----")}
          </Field>
          <div className="flex gap-3">
            <Field label={t("asset.tlsClientCert")} className="min-w-0 flex-1">
              {pemInput("tlsCertPEM", "tls-cert-pem", "-----BEGIN CERTIFICATE-----")}
            </Field>
            <Field label={t("asset.tlsClientKey")} className="min-w-0 flex-1">
              {pemInput(
                "tlsKeyPEM",
                "tls-key-pem",
                state.encryptedTLSKeyPEM ? t("asset.passwordUnchanged") : "-----BEGIN PRIVATE KEY-----"
              )}
            </Field>
          </div>
        </>
      )}
    </div>
  );
}

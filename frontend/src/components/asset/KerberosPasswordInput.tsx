import { useTranslation } from "react-i18next";
import { Input } from "@opskat/ui";
import { Field } from "@/components/asset/fields";
import type { KerberosFormFields } from "@/components/asset/kerberosConfig";

/** Kerberos 口令输入:已存的口令不回显,留空即沿用。 */
export function KerberosPasswordInput({
  state,
  patch,
}: {
  state: KerberosFormFields;
  patch: (p: Partial<KerberosFormFields>) => void;
}) {
  const { t } = useTranslation();
  return (
    <Field label={t("asset.krbPassword")} required={!state.encryptedKrbPassword}>
      <Input
        type="password"
        data-testid="krb-password"
        autoComplete="new-password"
        value={state.krbPassword}
        placeholder={state.encryptedKrbPassword ? t("asset.passwordUnchanged") : undefined}
        onChange={(e) => patch({ krbPassword: e.target.value })}
      />
    </Field>
  );
}

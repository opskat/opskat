import { useState, useCallback } from "react";
import { useTranslation } from "react-i18next";
import { Eye, EyeOff, Loader2, Pencil, Shield } from "lucide-react";
import { Button, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@opskat/ui";
import { Field, Segmented } from "@/components/asset/fields";
import { credential_entity } from "../../../wailsjs/go/models";
import { GetAssetPassword } from "../../../wailsjs/go/system/System";

interface PasswordSourceFieldProps {
  source: "inline" | "managed";
  onSourceChange: (source: "inline" | "managed") => void;
  password: string;
  onPasswordChange: (password: string) => void;
  credentialId: number;
  onCredentialIdChange: (id: number) => void;
  managedPasswords: credential_entity.Credential[];
  /** Placeholder for inline password input when no existing password */
  placeholder?: string;
  /** Placeholder shown when an existing encrypted password is set */
  hasExistingPassword?: boolean;
  /** Asset ID for decrypting existing password on reveal */
  editAssetId?: number;
  /** Override label for inline secret input. */
  secretLabel?: string;
  /** Override label for managed secret selector. */
  selectSecretLabel?: string;
  /** Fires when the selected credential has a non-empty username; passes it to the parent. */
  onUsernameChange?: (username: string) => void;
}

export function PasswordSourceField({
  source,
  onSourceChange,
  password,
  onPasswordChange,
  credentialId,
  onCredentialIdChange,
  managedPasswords,
  placeholder = "",
  hasExistingPassword = false,
  editAssetId,
  secretLabel,
  selectSecretLabel,
  onUsernameChange,
}: PasswordSourceFieldProps) {
  const { t } = useTranslation();
  const [showPassword, setShowPassword] = useState(false);
  const [decrypting, setDecrypting] = useState(false);
  const [decryptedOnce, setDecryptedOnce] = useState(false);

  const handleTogglePassword = useCallback(async () => {
    // 如果要显示密码，且有已保存的密码但用户未输入新密码，调用后端解密
    if (!showPassword && hasExistingPassword && !password && editAssetId && !decryptedOnce) {
      setDecrypting(true);
      try {
        const plaintext = await GetAssetPassword(editAssetId);
        if (plaintext) {
          onPasswordChange(plaintext);
          setDecryptedOnce(true);
        }
      } catch {
        // 解密失败时静默处理，仍然切换显示状态
      } finally {
        setDecrypting(false);
      }
    }
    setShowPassword(!showPassword);
  }, [showPassword, hasExistingPassword, password, editAssetId, decryptedOnce, onPasswordChange]);

  return (
    <div className="flex flex-col gap-3">
      <Field label={t("asset.passwordSource")}>
        <Segmented
          value={source}
          onChange={onSourceChange}
          aria-label={t("asset.passwordSource")}
          options={[
            { value: "inline", label: t("asset.passwordSourceInline"), icon: Pencil, testid: "password-source-inline" },
            {
              value: "managed",
              label: t("asset.passwordSourceManaged"),
              icon: Shield,
              testid: "password-source-managed",
            },
          ]}
        />
      </Field>
      {source === "inline" ? (
        <Field label={secretLabel || t("asset.password")}>
          <div className="relative">
            <Input
              type={showPassword ? "text" : "password"}
              value={password}
              onChange={(e) => onPasswordChange(e.target.value)}
              placeholder={hasExistingPassword && !decryptedOnce ? t("asset.passwordUnchanged") : placeholder}
              className="pr-9"
            />
            <Button
              type="button"
              variant="ghost"
              size="icon"
              className="absolute right-1 top-1/2 -translate-y-1/2 h-8 w-8"
              onClick={handleTogglePassword}
              disabled={decrypting}
            >
              {decrypting ? (
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
              ) : showPassword ? (
                <EyeOff className="h-3.5 w-3.5" />
              ) : (
                <Eye className="h-3.5 w-3.5" />
              )}
            </Button>
          </div>
        </Field>
      ) : (
        <Field label={selectSecretLabel || t("asset.selectPassword")}>
          {managedPasswords.length > 0 ? (
            <Select
              value={String(credentialId)}
              onValueChange={(v) => {
                const id = Number(v);
                onCredentialIdChange(id);
                if (id !== 0 && onUsernameChange) {
                  const cred = managedPasswords.find((p) => p.id === id);
                  if (cred && cred.username) {
                    onUsernameChange(cred.username);
                  }
                }
              }}
            >
              <SelectTrigger className="w-full">
                <SelectValue placeholder={t("asset.selectPasswordPlaceholder")} />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="0">{t("asset.selectPasswordPlaceholder")}</SelectItem>
                {managedPasswords.map((p) => (
                  <SelectItem key={p.id} value={String(p.id)}>
                    {p.name}
                    {p.username ? ` (${p.username})` : ""}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          ) : (
            <p className="text-xs text-muted-foreground">{t("asset.noManagedPasswords")}</p>
          )}
        </Field>
      )}
    </div>
  );
}

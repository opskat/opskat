import { useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@opskat/ui";
import { AssetSelect } from "@/components/asset/AssetSelect";
import { ConfigTabs } from "@/components/asset/ConfigTabs";
import { buildConfigGroups, type ConfigGroupSchema } from "@/components/asset/configFields";
import { Field } from "@/components/asset/fields";
import { ServerIdentityPrompt } from "@/components/remote/ServerIdentityPrompt";
import { useConfigSection } from "@/components/asset/useConfigSection";
import { resolveSaveCredential, resolveTestCredential } from "./credentialConfig";
import { proxyChainValidationKey } from "./proxyConfig";
import { useAssetCredential } from "./useAssetCredential";
import {
  buildVNCConfig,
  parseVNCConfig,
  parseVNCPasswordCredentialConfig,
  VNC_DEFAULTS,
  type VNCFormState,
} from "./VNCConfigSection.config";
import type { AssetFormHandle, AssetTestAttempt, ConfigSectionProps } from "@/lib/assetTypes/formContract";
import {
  CheckVNCServerKey,
  ConnectVNCTemporary,
  DisconnectVNC,
  StartVNCStream,
  TrustVNCServerKey,
} from "../../../wailsjs/go/vnc/VNC";
import { WailsRfbChannel } from "@/lib/wailsRfbChannel";
import {
  startVNCClient,
  vncClientFailureMessageKey,
  VNCClientError,
  type VNCClientHandle,
  type VNCNegotiatedSecurity,
  type VNCServerKeyCheck,
} from "@/lib/vncClient";
import {
  securityPolicyForVNCEncryption,
  VNC_ENCRYPTION_POLICIES,
  vncEncryptionLabelKey,
  type VNCEncryptionPolicy,
} from "@/lib/vncSecurity";

const VNC_ENCRYPTION_HINT_KEYS: Partial<Record<VNCEncryptionPolicy, string>> = {
  prefer_on: "vnc.encryptionPreferOnHint",
  prefer_off: "vnc.encryptionPreferOffHint",
};

interface VNCTemporarySession {
  id: string;
  username?: string;
  password?: string;
  encryption?: VNCEncryptionPolicy;
}

function negotiatedSecurityDetail(
  t: (key: string, options?: Record<string, unknown>) => string,
  security: VNCNegotiatedSecurity
) {
  const securityLabel = t(
    security.sessionEncrypted
      ? "vnc.security.sessionEncrypted"
      : security.authenticationEncrypted
        ? "vnc.security.authenticationOnly"
        : "vnc.security.unencrypted",
    { aesBits: security.aesBits }
  );
  return `${security.name} — ${securityLabel}`;
}

export function VNCConfigSection({ editAsset, onValidityChange, ref }: ConfigSectionProps) {
  const { t } = useTranslation();
  const [serverIdentity, setServerIdentity] = useState<VNCServerKeyCheck | null>(null);
  const activeAttemptRef = useRef<AssetTestAttempt | null>(null);
  const activeAttemptTokenRef = useRef<symbol | null>(null);
  const trustDecisionRef = useRef<((trusted: boolean) => void) | null>(null);
  const mountedRef = useRef(true);
  const passwordCredentialConfig = useMemo(
    () => (editAsset ? parseVNCPasswordCredentialConfig(editAsset.Config) : undefined),
    [editAsset]
  );
  const cred = useAssetCredential(editAsset, passwordCredentialConfig);
  const baseHandleRef = useRef<AssetFormHandle>(null);

  const startTemporaryTest = useCallback(
    (s: VNCFormState): AssetTestAttempt => {
      activeAttemptRef.current?.cancel();
      const attemptToken = Symbol("vnc-form-test");
      let active = true;
      let sessionID = "";
      let backendDisconnected = false;
      let client: VNCClientHandle | null = null;
      let channel: WailsRfbChannel | null = null;
      let rejectCancelled!: (reason: VNCClientError) => void;
      const cancelled = new Promise<never>((_resolve, reject) => {
        rejectCancelled = reject;
      });
      void cancelled.catch(() => undefined);

      const clearTrustPrompt = () => {
        if (activeAttemptTokenRef.current !== attemptToken) return;
        trustDecisionRef.current?.(false);
        trustDecisionRef.current = null;
        if (mountedRef.current) setServerIdentity(null);
      };
      const teardown = () => {
        clearTrustPrompt();
        client?.cleanup();
        client = null;
        channel?.close();
        channel = null;
        if (sessionID && !backendDisconnected) {
          backendDisconnected = true;
          void DisconnectVNC(sessionID);
        }
      };
      const run = async () => {
        const plainPassword = cred.value.password || s.password;
        const cfg = JSON.parse(await buildVNCConfig(s, resolveTestCredential(cred.value), async (plain) => plain));
        if (plainPassword) cfg.password = plainPassword;
        const session = (await ConnectVNCTemporary(JSON.stringify(cfg), plainPassword)) as VNCTemporarySession;
        sessionID = session.id;
        if (!active) {
          teardown();
          throw new VNCClientError("cancelled");
        }
        const target = document.createElement("div");
        channel = new WailsRfbChannel(session.id);
        client = startVNCClient({
          target,
          source: channel,
          sessionId: session.id,
          username: session.username,
          password: session.password,
          securityPolicy: securityPolicyForVNCEncryption(session.encryption ?? s.encryption),
          checkServerKey: CheckVNCServerKey,
          trustServerKey: TrustVNCServerKey,
          requestServerTrust: (check) =>
            new Promise<boolean>((resolve) => {
              if (!active) {
                resolve(false);
                return;
              }
              trustDecisionRef.current = resolve;
              setServerIdentity(check);
            }),
          openSource: () => channel?.markOpen(),
          startTransport: () => StartVNCStream(session.id),
        });
        const security = await client.result;
        if (!active) throw new VNCClientError("cancelled");
        return { successDetail: negotiatedSecurityDetail(t, security) };
      };

      const result = Promise.race([run(), cancelled]).finally(() => {
        active = false;
        teardown();
        if (activeAttemptTokenRef.current === attemptToken) {
          activeAttemptRef.current = null;
          activeAttemptTokenRef.current = null;
        }
      });
      const attempt: AssetTestAttempt = {
        result,
        cancel: () => {
          if (!active) return;
          active = false;
          rejectCancelled(new VNCClientError("cancelled"));
          teardown();
        },
        errorMessage: (error) =>
          error instanceof VNCClientError ? t(vncClientFailureMessageKey(error.code)) : undefined,
      };
      activeAttemptRef.current = attempt;
      activeAttemptTokenRef.current = attemptToken;
      return attempt;
    },
    [cred.value, t]
  );

  const { state, patch } = useConfigSection<VNCFormState>({
    ref: baseHandleRef,
    editAsset,
    onValidityChange,
    init: (a) => (a ? parseVNCConfig(a.Config) : { ...VNC_DEFAULTS }),
    validate: (s) => {
      const baseOk = !!s.host.trim() && s.port > 0 && s.port <= 65535;
      const proxyChainError = proxyChainValidationKey(s.proxyChainLayers);
      const canUse = baseOk && !proxyChainError;
      return {
        canTest: canUse,
        canSave: canUse,
        saveDisabledReason: baseOk ? proxyChainError : "asset.formMissingHost",
      };
    },
    build: async (s, ctx) => ({
      configJSON: await buildVNCConfig(
        s,
        await resolveSaveCredential(cred.value, ctx.encryptPassword),
        ctx.encryptPassword
      ),
      sshTunnelId: 0,
    }),
    buildTest: async (s) => {
      const plainPassword = cred.value.password || s.password;
      const cfg = JSON.parse(await buildVNCConfig(s, resolveTestCredential(cred.value), async (plain) => plain));
      if (plainPassword) cfg.password = plainPassword;
      return {
        assetType: "vnc",
        configJSON: JSON.stringify(cfg),
        password: plainPassword,
      };
    },
    deps: [cred.value],
  });

  useImperativeHandle(
    ref,
    () => ({
      buildConfig: (ctx) => baseHandleRef.current!.buildConfig(ctx),
      buildTestConfig: (ctx) => baseHandleRef.current!.buildTestConfig!(ctx),
      startTest: () => startTemporaryTest(state),
    }),
    [state, startTemporaryTest]
  );

  useEffect(
    () => () => {
      mountedRef.current = false;
      activeAttemptRef.current?.cancel();
    },
    []
  );

  const approveVNCServer = () => {
    const decide = trustDecisionRef.current;
    trustDecisionRef.current = null;
    setServerIdentity(null);
    decide?.(true);
  };

  const rejectVNCServer = () => {
    const decide = trustDecisionRef.current;
    trustDecisionRef.current = null;
    setServerIdentity(null);
    decide?.(false);
  };

  const groups: ConfigGroupSchema<VNCFormState>[] = [
    {
      key: "connection",
      label: "asset.tabConnection",
      fields: [
        {
          kind: "row",
          fields: [
            {
              kind: "text",
              key: "host",
              label: "asset.host",
              required: true,
              placeholder: "vnc.example.com",
              width: "flex-1",
              testid: "vnc-host-input",
            },
            {
              kind: "number",
              key: "port",
              label: "asset.port",
              width: "w-[110px] shrink-0",
              placeholder: String(VNC_DEFAULTS.port),
              testid: "vnc-port-input",
            },
          ],
        },
        { kind: "text", key: "username", label: "asset.username", testid: "vnc-username-input" },
        { kind: "password", placeholder: "asset.passwordPlaceholder" },
      ],
    },
    {
      key: "tunnel",
      label: "asset.tabTunnel",
      fields: [{ kind: "tunnel" }],
    },
    {
      key: "files",
      label: "vnc.files",
      fields: [
        {
          kind: "custom",
          render: () => (
            <Field label={t("vnc.fileSshAsset")}>
              <AssetSelect
                value={state.fileSshAssetId}
                onValueChange={(fileSshAssetId) => patch({ fileSshAssetId })}
                filterType="ssh"
                placeholder={t("vnc.fileSshAssetPlaceholder")}
                testId="vnc-file-ssh-select"
              />
            </Field>
          ),
        },
      ],
    },
    {
      key: "advanced",
      label: "asset.tabAdvanced",
      fields: [
        {
          kind: "custom",
          render: () => {
            const selectedHint = VNC_ENCRYPTION_HINT_KEYS[state.encryption];
            return (
              <Field label={t("vnc.encryptionPolicy")}>
                <Select
                  value={state.encryption}
                  onValueChange={(encryption) => patch({ encryption: encryption as VNCEncryptionPolicy })}
                >
                  <SelectTrigger data-testid="vnc-encryption-select" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {VNC_ENCRYPTION_POLICIES.map((policy) => (
                      <SelectItem key={policy} value={policy}>
                        {t(vncEncryptionLabelKey(policy))}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                {selectedHint && <p className="text-xs text-muted-foreground">{t(selectedHint)}</p>}
              </Field>
            );
          },
        },
      ],
    },
  ];

  return (
    <div className="flex flex-col gap-4">
      {serverIdentity && (
        <div className="rounded-md border bg-muted/20 p-4">
          <div className="mb-3 text-sm font-medium">{t("vnc.verifyServerTitle")}</div>
          <ServerIdentityPrompt
            identity={{
              host: serverIdentity.host,
              port: serverIdentity.port,
              keyType: "VNC RSA SHA-256",
              fingerprint: serverIdentity.newFingerprint,
              oldFingerprint: serverIdentity.oldFingerprint,
              isChanged: serverIdentity.state === "changed",
            }}
            changedWarning={t("vnc.serverKeyChangedWarning")}
            oldFingerprintLabel={t("vnc.oldFingerprint")}
            rejectLabel={t("action.cancel")}
            trustLabel={serverIdentity.state === "changed" ? t("vnc.replaceServerKey") : t("vnc.trustAndConnect")}
            trustDestructive={serverIdentity.state === "changed"}
            onReject={rejectVNCServer}
            onTrust={approveVNCServer}
            testIdPrefix="vnc-test-verify"
          />
        </div>
      )}
      <ConfigTabs groups={buildConfigGroups(groups, { state, patch, ctx: { cred, editAsset } })} />
    </div>
  );
}

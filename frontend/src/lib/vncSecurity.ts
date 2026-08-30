export type VNCEncryptionPolicy = "server" | "always_maximum" | "always_on" | "prefer_on" | "prefer_off";

export const VNC_ENCRYPTION_POLICIES: readonly VNCEncryptionPolicy[] = [
  "server",
  "always_maximum",
  "always_on",
  "prefer_on",
  "prefer_off",
];

const VNC_ENCRYPTION_POLICY_SET = new Set<string>(VNC_ENCRYPTION_POLICIES);

const VNC_ENCRYPTION_LABEL_KEYS: Record<VNCEncryptionPolicy, string> = {
  server: "vnc.encryptionServer",
  always_maximum: "vnc.encryptionAlwaysMaximum",
  always_on: "vnc.encryptionAlwaysOn",
  prefer_on: "vnc.encryptionPreferOn",
  prefer_off: "vnc.encryptionPreferOff",
};

const FULL_SESSION_SECURITY_TYPES = [5, 13, 129, 133];
const MAXIMUM_SECURITY_TYPES = [129, 133];
const NON_FULL_SESSION_SECURITY_TYPES = [1, 2, 6, 16, 19, 22, 30, 113, 130];

export function normalizeVNCEncryptionPolicy(value?: string): VNCEncryptionPolicy {
  const normalized = value || "server";
  if (!VNC_ENCRYPTION_POLICY_SET.has(normalized)) {
    throw new Error(`Unknown VNC encryption policy: ${value}`);
  }
  return normalized as VNCEncryptionPolicy;
}

export function vncEncryptionLabelKey(value?: string): string {
  return VNC_ENCRYPTION_LABEL_KEYS[normalizeVNCEncryptionPolicy(value)];
}

/** Maps the persisted OpsKat policy to noVNC's ordered security preference groups. */
export function securityPolicyForVNCEncryption(value?: string): number[][] {
  switch (normalizeVNCEncryptionPolicy(value)) {
    case "server":
      return [];
    case "always_maximum":
      return [[...MAXIMUM_SECURITY_TYPES]];
    case "always_on":
      return [[...FULL_SESSION_SECURITY_TYPES]];
    case "prefer_on":
      return [[...FULL_SESSION_SECURITY_TYPES], [...NON_FULL_SESSION_SECURITY_TYPES]];
    case "prefer_off":
      return [[...NON_FULL_SESSION_SECURITY_TYPES], [...FULL_SESSION_SECURITY_TYPES]];
  }
}

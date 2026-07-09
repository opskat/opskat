import type { CredentialFragment } from "./credentialConfig";

interface RDPConfig {
  host: string;
  port: number;
  username: string;
  password?: string;
  credential_id?: number;
  domain?: string;
  width?: number;
  height?: number;
  clipboard: boolean;
}

export interface RDPFormState {
  host: string;
  port: number;
  username: string;
  domain: string;
  width: number;
  height: number;
  clipboard: boolean;
}

export const RDP_DEFAULTS: RDPFormState = {
  host: "",
  port: 3389,
  username: "Administrator",
  domain: "",
  width: 1280,
  height: 720,
  clipboard: true,
};

export function buildRDPConfig(state: RDPFormState, cred: CredentialFragment): string {
  const cfg: RDPConfig = {
    host: state.host,
    port: state.port || 3389,
    username: state.username,
    clipboard: state.clipboard,
  };
  if (state.domain.trim()) cfg.domain = state.domain.trim();
  if (state.width > 0) cfg.width = state.width;
  if (state.height > 0) cfg.height = state.height;
  if (cred.credential_id) cfg.credential_id = cred.credential_id;
  else if (cred.password) cfg.password = cred.password;
  return JSON.stringify(cfg);
}

export function parseRDPConfig(configJSON: string): RDPFormState {
  try {
    const cfg: RDPConfig = JSON.parse(configJSON || "{}");
    return {
      host: cfg.host || "",
      port: cfg.port || 3389,
      username: cfg.username || "Administrator",
      domain: cfg.domain || "",
      width: cfg.width || 1280,
      height: cfg.height || 720,
      clipboard: cfg.clipboard !== false,
    };
  } catch {
    return { ...RDP_DEFAULTS };
  }
}

export function parseRDPCredentialConfig(configJSON: string): CredentialFragment {
  try {
    const cfg: RDPConfig = JSON.parse(configJSON || "{}");
    return { credential_id: cfg.credential_id, password: cfg.password };
  } catch {
    return {};
  }
}

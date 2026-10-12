import { describe, it, expect, vi } from "vitest";
import { useState } from "react";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { SelectTLSCertFile } from "../../../../wailsjs/go/system/System";
import { Fields } from "@/components/asset/configFields";
import {
  TLS_CERT_DEFAULTS,
  buildTLSCerts,
  parseTLSCerts,
  resolveTLSKey,
  tlsCertFields,
  tlsCertsFromJSON,
  tlsCertsToJSON,
  type TLSCertFormFields,
} from "@/components/asset/tlsCertConfig";

const encrypt = (p: string) => Promise.resolve(`ENC(${p})`);
const CA = "-----BEGIN CERTIFICATE-----\nca\n-----END CERTIFICATE-----";
const CERT = "-----BEGIN CERTIFICATE-----\nclient\n-----END CERTIFICATE-----";
const KEY = "-----BEGIN PRIVATE KEY-----\nkey\n-----END PRIVATE KEY-----";

describe("parseTLSCerts", () => {
  it("opens stored paths as the file source", () => {
    expect(parseTLSCerts({ caFile: "/ca.pem", certFile: "/c.crt", keyFile: "/c.key" })).toEqual({
      ...TLS_CERT_DEFAULTS,
      tlsCertSource: "file",
      tlsCAFile: "/ca.pem",
      tlsCertFile: "/c.crt",
      tlsKeyFile: "/c.key",
    });
  });

  it("opens stored content as the content source, keeping the key's ciphertext out of the input", () => {
    expect(parseTLSCerts({ caPEM: CA, certPEM: CERT, keyPEM: "CIPHER" })).toEqual({
      ...TLS_CERT_DEFAULTS,
      tlsCertSource: "pem",
      tlsCAPEM: CA,
      tlsCertPEM: CERT,
      tlsKeyPEM: "",
      encryptedTLSKeyPEM: "CIPHER",
    });
  });
});

describe("buildTLSCerts", () => {
  const both: TLSCertFormFields = {
    tlsCertSource: "file",
    tlsCAFile: " /ca.pem ",
    tlsCertFile: "/c.crt",
    tlsKeyFile: "/c.key",
    tlsCAPEM: `${CA}\n`,
    tlsCertPEM: CERT,
    tlsKeyPEM: "",
    encryptedTLSKeyPEM: "CIPHER",
  };

  it("saves only the selected source: paths", () => {
    expect(buildTLSCerts(both, "CIPHER")).toEqual({ caFile: "/ca.pem", certFile: "/c.crt", keyFile: "/c.key" });
  });

  it("saves only the selected source: content, with the key as the ciphertext handed in", () => {
    expect(buildTLSCerts({ ...both, tlsCertSource: "pem" }, "CIPHER")).toEqual({
      caPEM: CA,
      certPEM: CERT,
      keyPEM: "CIPHER",
    });
  });

  it("drops the key once the client certificate is removed", () => {
    expect(buildTLSCerts({ ...both, tlsCertSource: "pem", tlsCertPEM: " " }, "CIPHER")).toEqual({ caPEM: CA });
  });
});

describe("resolveTLSKey", () => {
  const state: TLSCertFormFields = { ...TLS_CERT_DEFAULTS, tlsCertSource: "pem", encryptedTLSKeyPEM: "CIPHER" };

  it("keeps the stored ciphertext while the key is untouched", async () => {
    expect(await resolveTLSKey(state, encrypt)).toBe("CIPHER");
  });

  it("encrypts a newly typed key", async () => {
    expect(await resolveTLSKey({ ...state, tlsKeyPEM: `${KEY}\n` }, encrypt)).toBe(`ENC(${KEY})`);
  });
});

describe("tlsCertFields", () => {
  function Harness({ initial }: { initial: TLSCertFormFields }) {
    const [state, setState] = useState(initial);
    return <Fields fields={tlsCertFields()} state={state} patch={(p) => setState((s) => ({ ...s, ...p }))} />;
  }

  it("shows path inputs or content inputs depending on the chosen source, keeping both sets of values", () => {
    render(<Harness initial={{ ...TLS_CERT_DEFAULTS, tlsCertSource: "file", tlsCAFile: "/ca.pem" }} />);
    expect(screen.getByTestId("tls-ca-file")).toHaveValue("/ca.pem");
    expect(screen.queryByTestId("tls-ca-pem")).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId("tls-cert-source-pem"));
    expect(screen.queryByTestId("tls-ca-file")).not.toBeInTheDocument();
    fireEvent.change(screen.getByTestId("tls-ca-pem"), { target: { value: CA } });
    expect(screen.getByTestId("tls-cert-pem")).toBeInTheDocument();
    expect(screen.getByTestId("tls-key-pem")).toBeInTheDocument();

    fireEvent.click(screen.getByTestId("tls-cert-source-file"));
    expect(screen.getByTestId("tls-ca-file")).toHaveValue("/ca.pem");
    fireEvent.click(screen.getByTestId("tls-cert-source-pem"));
    expect(screen.getByTestId("tls-ca-pem")).toHaveValue(CA);
  });

  // 路径既可以手填，也可以点按钮从系统文件选择框里挑。
  it.each(["ca", "cert", "key"])("the %s path can be typed or picked with the native file dialog", async (item) => {
    vi.mocked(SelectTLSCertFile).mockResolvedValue(`/picked/${item}.pem`);
    render(<Harness initial={{ ...TLS_CERT_DEFAULTS, tlsCertSource: "file" }} />);
    const input = screen.getByTestId(`tls-${item}-file`);

    fireEvent.change(input, { target: { value: `/typed/${item}.pem` } });
    expect(input).toHaveValue(`/typed/${item}.pem`);

    await act(async () => {
      fireEvent.click(screen.getByTestId(`tls-${item}-file-browse`));
    });
    expect(input).toHaveValue(`/picked/${item}.pem`);
  });

  it("cancelling the file dialog keeps the path already there", async () => {
    vi.mocked(SelectTLSCertFile).mockResolvedValue("");
    render(<Harness initial={{ ...TLS_CERT_DEFAULTS, tlsCertSource: "file", tlsCAFile: "/ca.pem" }} />);

    await act(async () => {
      fireEvent.click(screen.getByTestId("tls-ca-file-browse"));
    });
    expect(screen.getByTestId("tls-ca-file")).toHaveValue("/ca.pem");
  });

  it("shows a stored key as set, never as its ciphertext", () => {
    render(<Harness initial={{ ...TLS_CERT_DEFAULTS, tlsCertSource: "pem", encryptedTLSKeyPEM: "CIPHER" }} />);
    const key = screen.getByTestId("tls-key-pem");
    expect(key).toHaveValue("");
    expect(key).toHaveAttribute("placeholder", "asset.passwordUnchanged");
  });
});

describe("built-in config keys", () => {
  it("round-trips paths and content through the tls_* keys", () => {
    const file = tlsCertsFromJSON({ tls_ca_file: "/ca.pem", tls_cert_file: "/c.crt", tls_key_file: "/c.key" });
    expect(JSON.stringify(tlsCertsToJSON(file, ""))).toBe(
      '{"tls_ca_file":"/ca.pem","tls_cert_file":"/c.crt","tls_key_file":"/c.key"}'
    );
    const pem = tlsCertsFromJSON({ tls_ca_pem: CA, tls_cert_pem: CERT, tls_key_pem: "CIPHER" });
    expect(tlsCertsToJSON(pem, pem.encryptedTLSKeyPEM)).toEqual({
      tls_ca_pem: CA,
      tls_cert_pem: CERT,
      tls_key_pem: "CIPHER",
    });
  });
});

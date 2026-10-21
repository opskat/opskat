import { tlsCertFields } from "@/components/asset/tlsCertConfig";
import type { DatabaseFormState } from "@/components/asset/DatabaseConfigSection.config";
import { registerDriverFormField } from "./formFields";

registerDriverFormField("host", [
  {
    kind: "row",
    fields: [
      {
        kind: "text",
        key: "host",
        label: "asset.host",
        required: true,
        placeholder: "example.com",
        width: "flex-1",
        testid: "database-host-input",
      },
      {
        kind: "number",
        key: "port",
        label: "asset.port",
        width: "w-[110px] shrink-0",
        blankWhenZero: true,
        testid: "database-port-input",
      },
    ],
  },
]);
registerDriverFormField("port", [
  { kind: "number", key: "port", label: "asset.port", blankWhenZero: true, testid: "database-port-input" },
]);
registerDriverFormField("username", [
  { kind: "text", key: "username", label: "asset.username", testid: "database-username-input" },
]);
registerDriverFormField("password", [{ kind: "password" }]);
registerDriverFormField("database", [
  {
    kind: "text",
    key: "database",
    label: "asset.database",
    placeholder: "asset.databasePlaceholder",
    testid: "database-name-input",
  },
]);
registerDriverFormField("tunnel", [{ kind: "tunnel" }]);
registerDriverFormField("ssl_mode", [
  {
    kind: "select",
    key: "sslMode",
    label: "asset.sslMode",
    options: [
      { value: "disable", label: "disable" },
      { value: "require", label: "require" },
      { value: "verify-ca", label: "verify-ca" },
      { value: "verify-full", label: "verify-full" },
    ],
  },
]);
registerDriverFormField("tls", [{ kind: "switch", key: "tls", label: "TLS" }]);
// 证书字段组自带启用开关,与 Redis 的 TLS 分组同款;其余控件在启用后才显示。
registerDriverFormField("tls_cert", [
  { kind: "switch", key: "tls", label: "asset.tls" },
  { kind: "switch", key: "tlsInsecure", label: "asset.tlsInsecure", visibleWhen: (s) => s.tls },
  { kind: "text", key: "tlsServerName", label: "asset.tlsServerName", visibleWhen: (s) => s.tls },
  ...tlsCertFields<DatabaseFormState>((s) => s.tls),
]);
registerDriverFormField("params", [
  { kind: "text", key: "params", label: "asset.params", placeholder: "asset.paramsPlaceholder" },
]);
registerDriverFormField("read_only", [{ kind: "switch", key: "readOnly", label: "asset.readOnly" }]);
registerDriverFormField("query_timeout_seconds", [
  {
    kind: "number",
    key: "queryTimeoutSeconds",
    label: "asset.queryTimeout",
    min: 5,
    max: 600,
    width: "flex-1",
    testid: "database-query-timeout-input",
  },
]);

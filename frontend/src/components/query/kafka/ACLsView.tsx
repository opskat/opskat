import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Loader2, Plus, Trash2 } from "lucide-react";
import {
  Button,
  ConfirmDialog,
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Input,
} from "@opskat/ui";
import { type KafkaACL, type KafkaACLMutationRequest, type KafkaTabState, useKafkaStore } from "@/stores/kafkaStore";
import { CompactSelect, EmptyState, LoadingBlock, StatusPill } from "./shared";

const ACL_RESOURCE_TYPES = ["any", "topic", "group", "cluster", "transactional_id", "delegation_token"];
const ACL_MUTATION_RESOURCE_TYPES = ["topic", "group", "cluster", "transactional_id", "delegation_token"];
const ACL_FILTER_PATTERNS = ["any", "match", "literal", "prefixed"];
const ACL_MUTATION_PATTERNS = ["literal", "prefixed"];
const ACL_OPERATIONS = [
  "any",
  "all",
  "read",
  "write",
  "create",
  "delete",
  "alter",
  "describe",
  "describe_configs",
  "alter_configs",
  "idempotent_write",
  "cluster_action",
];
const ACL_MUTATION_OPERATIONS = ACL_OPERATIONS.filter((item) => item !== "any");
const ACL_PERMISSIONS = ["any", "allow", "deny"];
const ACL_MUTATION_PERMISSIONS = ["allow", "deny"];

export function ACLsView({ tabId, state }: { tabId: string; state: KafkaTabState }) {
  const { t } = useTranslation();
  const [createOpen, setCreateOpen] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<KafkaACL | null>(null);
  const setACLFilters = useKafkaStore((s) => s.setACLFilters);
  const loadACLs = useKafkaStore((s) => s.loadACLs);
  const deleteACL = useKafkaStore((s) => s.deleteACL);
  const filters = state.aclFilters || {
    resourceType: "any",
    resourceName: "",
    patternType: "any",
    principal: "",
    host: "",
    operation: "any",
    permission: "any",
  };

  return (
    <div className="flex h-full flex-col">
      <div className="grid shrink-0 gap-2 border-b px-4 py-2 xl:grid-cols-[150px_1fr_150px_1fr_150px_150px_150px_auto_auto]">
        <CompactSelect
          value={filters.resourceType}
          onChange={(value) => setACLFilters(tabId, { resourceType: value })}
          items={ACL_RESOURCE_TYPES}
        />
        <Input
          className="h-8 font-mono text-xs"
          value={filters.resourceName}
          onChange={(e) => setACLFilters(tabId, { resourceName: e.target.value })}
          placeholder={t("query.kafkaACLResourceName")}
        />
        <CompactSelect
          value={filters.patternType}
          onChange={(value) => setACLFilters(tabId, { patternType: value })}
          items={ACL_FILTER_PATTERNS}
        />
        <Input
          className="h-8 font-mono text-xs"
          value={filters.principal}
          onChange={(e) => setACLFilters(tabId, { principal: e.target.value })}
          placeholder={t("query.kafkaACLPrincipal")}
        />
        <Input
          className="h-8 font-mono text-xs"
          value={filters.host}
          onChange={(e) => setACLFilters(tabId, { host: e.target.value })}
          placeholder={t("query.kafkaACLHost")}
        />
        <CompactSelect
          value={filters.operation}
          onChange={(value) => setACLFilters(tabId, { operation: value })}
          items={ACL_OPERATIONS}
        />
        <CompactSelect
          value={filters.permission}
          onChange={(value) => setACLFilters(tabId, { permission: value })}
          items={ACL_PERMISSIONS}
        />
        <Button variant="outline" size="sm" className="h-8" onClick={() => loadACLs(tabId)}>
          {t("query.applyFilter")}
        </Button>
        <Button variant="outline" size="sm" className="h-8 gap-1.5" onClick={() => setCreateOpen(true)}>
          <Plus className="h-3.5 w-3.5" />
          {t("query.kafkaCreateACL")}
        </Button>
      </div>
      <div className="min-h-0 flex-1 overflow-auto">
        {state.loadingACLs && !state.acls.length ? (
          <LoadingBlock />
        ) : state.acls.length === 0 ? (
          <EmptyState text={t("query.kafkaNoACLs")} />
        ) : (
          <ACLTable acls={state.acls} onDelete={setDeleteTarget} />
        )}
      </div>
      <div className="shrink-0 border-t px-4 py-2 text-xs text-muted-foreground">
        {t("query.kafkaACLTotal", { count: state.aclsTotal })}
      </div>
      <CreateACLDialog tabId={tabId} open={createOpen} onOpenChange={setCreateOpen} />
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null);
        }}
        title={t("query.kafkaDeleteACL")}
        description={t("query.kafkaDeleteACLConfirmDesc", {
          principal: deleteTarget?.principal || "",
          resource: deleteTarget ? `${deleteTarget.resourceType}:${deleteTarget.resourceName}` : "",
        })}
        cancelText={t("action.cancel")}
        confirmText={t("action.delete")}
        onConfirm={async () => {
          if (!deleteTarget) return;
          await deleteACL(tabId, deleteTarget);
          setDeleteTarget(null);
        }}
      />
    </div>
  );
}

function ACLTable({ acls, onDelete }: { acls: KafkaACL[]; onDelete: (acl: KafkaACL) => void }) {
  const { t } = useTranslation();
  return (
    <table className="w-full text-sm">
      <thead className="sticky top-0 bg-muted/90 text-xs text-muted-foreground backdrop-blur">
        <tr>
          <th className="px-3 py-2 text-left font-medium">{t("query.kafkaACLResource")}</th>
          <th className="px-3 py-2 text-left font-medium">{t("query.kafkaACLPrincipal")}</th>
          <th className="px-3 py-2 text-left font-medium">{t("query.kafkaACLHost")}</th>
          <th className="px-3 py-2 text-left font-medium">{t("query.kafkaACLOperation")}</th>
          <th className="px-3 py-2 text-left font-medium">{t("query.kafkaACLPermission")}</th>
          <th className="w-12 px-3 py-2 text-right font-medium"></th>
        </tr>
      </thead>
      <tbody>
        {acls.map((acl) => (
          <tr key={aclKey(acl)} className="border-t">
            <td className="max-w-[360px] px-3 py-2">
              <div className="truncate font-mono text-xs">{acl.resourceName || "-"}</div>
              <div className="mt-0.5 flex flex-wrap gap-1 text-[10px] uppercase text-muted-foreground">
                <span>{acl.resourceType}</span>
                <span>{acl.patternType}</span>
              </div>
            </td>
            <td className="max-w-[280px] truncate px-3 py-2 font-mono text-xs">{acl.principal}</td>
            <td className="px-3 py-2 font-mono text-xs">{acl.host}</td>
            <td className="px-3 py-2">
              <StatusPill value={acl.operation} />
            </td>
            <td className="px-3 py-2">
              <StatusPill value={acl.permission} />
            </td>
            <td className="px-3 py-2 text-right">
              <Button
                variant="ghost"
                size="icon"
                className="h-7 w-7 text-destructive hover:text-destructive"
                onClick={() => onDelete(acl)}
              >
                <Trash2 className="h-3.5 w-3.5" />
              </Button>
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function CreateACLDialog({
  tabId,
  open,
  onOpenChange,
}: {
  tabId: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useTranslation();
  const createACL = useKafkaStore((s) => s.createACL);
  const state = useKafkaStore((s) => s.states[tabId]);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [form, setForm] = useState<KafkaACLMutationRequest>({
    resourceType: "topic",
    resourceName: "",
    patternType: "literal",
    principal: "",
    host: "*",
    operation: "read",
    permission: "allow",
  });

  const update = (patch: Partial<KafkaACLMutationRequest>) => setForm((current) => ({ ...current, ...patch }));
  const resourceNameRequired = form.resourceType !== "cluster";
  const canSubmit =
    form.resourceType &&
    form.principal.trim() &&
    form.operation &&
    form.permission &&
    (!resourceNameRequired || form.resourceName?.trim());

  const submit = async () => {
    await createACL(tabId, {
      ...form,
      resourceName: form.resourceName?.trim(),
      principal: form.principal.trim(),
      host: form.host?.trim() || "*",
    });
    setForm({
      resourceType: "topic",
      resourceName: "",
      patternType: "literal",
      principal: "",
      host: "*",
      operation: "read",
      permission: "allow",
    });
    onOpenChange(false);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t("query.kafkaCreateACL")}</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <div className="grid grid-cols-2 gap-2">
            <CompactSelect
              value={form.resourceType}
              onChange={(value) => update({ resourceType: value })}
              items={ACL_MUTATION_RESOURCE_TYPES}
            />
            <CompactSelect
              value={form.patternType || "literal"}
              onChange={(value) => update({ patternType: value })}
              items={ACL_MUTATION_PATTERNS}
            />
          </div>
          <Input
            className="h-8 font-mono text-xs"
            value={form.resourceName || ""}
            disabled={form.resourceType === "cluster"}
            onChange={(e) => update({ resourceName: e.target.value })}
            placeholder={form.resourceType === "cluster" ? "kafka-cluster" : t("query.kafkaACLResourceName")}
          />
          <div className="grid grid-cols-2 gap-2">
            <Input
              className="h-8 font-mono text-xs"
              value={form.principal}
              onChange={(e) => update({ principal: e.target.value })}
              placeholder={t("query.kafkaACLPrincipalPlaceholder")}
            />
            <Input
              className="h-8 font-mono text-xs"
              value={form.host || ""}
              onChange={(e) => update({ host: e.target.value })}
              placeholder="*"
            />
          </div>
          <div className="grid grid-cols-2 gap-2">
            <CompactSelect
              value={form.operation}
              onChange={(value) => update({ operation: value })}
              items={ACL_MUTATION_OPERATIONS}
            />
            <CompactSelect
              value={form.permission}
              onChange={(value) => update({ permission: value })}
              items={ACL_MUTATION_PERMISSIONS}
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("action.cancel")}
          </Button>
          <Button disabled={state?.aclAdminLoading || !canSubmit} onClick={() => setConfirmOpen(true)}>
            {state?.aclAdminLoading && <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />}
            {t("query.kafkaCreateACL")}
          </Button>
        </DialogFooter>
        <ConfirmDialog
          open={confirmOpen}
          onOpenChange={setConfirmOpen}
          title={t("query.kafkaCreateACL")}
          description={t("query.kafkaCreateACLConfirmDesc", {
            principal: form.principal.trim(),
            resource: `${form.resourceType}:${form.resourceType === "cluster" ? "kafka-cluster" : form.resourceName}`,
          })}
          cancelText={t("action.cancel")}
          confirmText={t("query.kafkaCreateACL")}
          onConfirm={submit}
        />
      </DialogContent>
    </Dialog>
  );
}

function aclKey(acl: KafkaACL): string {
  return [
    acl.resourceType,
    acl.resourceName,
    acl.patternType,
    acl.principal,
    acl.host,
    acl.operation,
    acl.permission,
  ].join("|");
}

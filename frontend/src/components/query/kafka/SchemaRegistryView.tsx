import { useState } from "react";
import { useTranslation } from "react-i18next";
import { FileJson, Loader2, Plus, Trash2 } from "lucide-react";
import {
  Button,
  ConfirmDialog,
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Input,
  Textarea,
} from "@opskat/ui";
import {
  type KafkaRegisterSchemaRequest,
  type KafkaSchemaCompatibilityResponse,
  type KafkaSchemaReference,
  type KafkaTabState,
  useKafkaStore,
} from "@/stores/kafkaStore";
import { CompactSelect, EmptyState, LoadingBlock, Metric, StatusPill } from "./shared";
import { parseSchemaReferences, formatSchema } from "./utils";

export function SchemaRegistryView({ tabId, state }: { tabId: string; state: KafkaTabState }) {
  const { t } = useTranslation();
  const [registerOpen, setRegisterOpen] = useState(false);
  const loadSchemaSubjects = useKafkaStore((s) => s.loadSchemaSubjects);
  const loadSchemaVersions = useKafkaStore((s) => s.loadSchemaVersions);

  return (
    <div className="flex h-full flex-col">
      <div className="flex shrink-0 items-center gap-2 border-b px-4 py-2">
        <Button variant="outline" size="sm" className="h-8" onClick={() => loadSchemaSubjects(tabId)}>
          {state.loadingSchemaSubjects ? <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" /> : null}
          {t("query.refreshTree")}
        </Button>
        <Button variant="outline" size="sm" className="h-8 gap-1.5" onClick={() => setRegisterOpen(true)}>
          <Plus className="h-3.5 w-3.5" />
          {t("query.kafkaRegisterSchema")}
        </Button>
        <span className="ml-auto text-xs text-muted-foreground">
          {t("query.kafkaSchemaSubjectTotal", { count: state.schemaSubjects.length })}
        </span>
      </div>
      <div className="grid min-h-0 flex-1 grid-cols-[minmax(320px,0.8fr)_minmax(460px,1.2fr)]">
        <div className="min-h-0 overflow-auto border-r">
          {state.loadingSchemaSubjects && !state.schemaSubjects.length ? (
            <LoadingBlock />
          ) : state.schemaSubjects.length === 0 ? (
            <EmptyState text={t("query.kafkaNoSchemaSubjects")} />
          ) : (
            <SchemaSubjectTable
              subjects={state.schemaSubjects}
              selected={state.selectedSchemaSubject}
              onSelect={(subject) => loadSchemaVersions(tabId, subject)}
            />
          )}
        </div>
        <div className="min-h-0 overflow-auto">
          <SchemaDetailPanel tabId={tabId} state={state} />
        </div>
      </div>
      <RegisterSchemaDialog tabId={tabId} open={registerOpen} onOpenChange={setRegisterOpen} />
    </div>
  );
}

function SchemaSubjectTable({
  subjects,
  selected,
  onSelect,
}: {
  subjects: string[];
  selected?: string;
  onSelect: (subject: string) => void;
}) {
  const { t } = useTranslation();
  return (
    <table className="w-full text-sm">
      <thead className="sticky top-0 bg-muted/90 text-xs text-muted-foreground backdrop-blur">
        <tr>
          <th className="px-3 py-2 text-left font-medium">{t("query.kafkaSchemaSubject")}</th>
        </tr>
      </thead>
      <tbody>
        {subjects.map((subject) => (
          <tr
            key={subject}
            className={`cursor-pointer border-t hover:bg-muted/40 ${selected === subject ? "bg-muted/60" : ""}`}
            onClick={() => onSelect(subject)}
          >
            <td className="max-w-[420px] truncate px-3 py-2 font-mono text-xs">{subject}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function SchemaDetailPanel({ tabId, state }: { tabId: string; state: KafkaTabState }) {
  const { t } = useTranslation();
  const [deleteVersionOpen, setDeleteVersionOpen] = useState(false);
  const [deleteSubjectOpen, setDeleteSubjectOpen] = useState(false);
  const loadSchema = useKafkaStore((s) => s.loadSchema);
  const deleteSchema = useKafkaStore((s) => s.deleteSchema);
  if (state.loadingSchemaDetail) return <LoadingBlock />;
  if (!state.selectedSchemaSubject) return <EmptyState text={t("query.kafkaSelectSchemaSubject")} />;
  const detail = state.schemaDetail;
  const versions = state.schemaVersions?.versions || [];
  if (!detail) return <EmptyState text={t("query.kafkaNoSchemaDetail")} />;

  const deleteVersion = async () => {
    await deleteSchema(tabId, { subject: detail.subject, version: String(detail.version) });
    setDeleteVersionOpen(false);
  };
  const deleteSubject = async () => {
    await deleteSchema(tabId, { subject: detail.subject });
    setDeleteSubjectOpen(false);
  };

  return (
    <div className="space-y-4 p-4">
      <div className="flex items-center gap-2">
        <FileJson className="h-4 w-4 text-muted-foreground" />
        <div className="min-w-0 flex-1 truncate font-mono text-sm font-semibold">{detail.subject}</div>
        <StatusPill value={detail.schemaType || "schema"} />
        <Button
          variant="ghost"
          size="icon"
          className="h-7 w-7 text-destructive hover:text-destructive"
          onClick={() => setDeleteVersionOpen(true)}
        >
          <Trash2 className="h-3.5 w-3.5" />
        </Button>
      </div>
      <div className="grid grid-cols-3 gap-2">
        <Metric label="ID" value={detail.id} />
        <Metric label={t("query.kafkaSchemaVersion")} value={detail.version} />
        <Metric label={t("query.kafkaSchemaVersions")} value={versions.length} />
      </div>
      <div className="flex flex-wrap gap-1">
        {versions.map((version) => (
          <Button
            key={version}
            variant={version === detail.version ? "default" : "outline"}
            size="sm"
            className="h-7 px-2 font-mono text-xs"
            onClick={() => loadSchema(tabId, detail.subject, String(version))}
          >
            v{version}
          </Button>
        ))}
        <Button variant="ghost" size="sm" className="h-7 text-destructive" onClick={() => setDeleteSubjectOpen(true)}>
          {t("query.kafkaDeleteSubject")}
        </Button>
      </div>
      {detail.references?.length ? <SchemaReferencesTable references={detail.references} /> : null}
      <pre className="max-h-[520px] overflow-auto whitespace-pre-wrap break-all rounded-md border bg-muted/30 p-3 font-mono text-xs leading-relaxed">
        {formatSchema(detail.schema)}
      </pre>
      <ConfirmDialog
        open={deleteVersionOpen}
        onOpenChange={setDeleteVersionOpen}
        title={t("query.kafkaDeleteSchemaVersion")}
        description={t("query.kafkaDeleteSchemaVersionConfirmDesc", {
          subject: detail.subject,
          version: detail.version,
        })}
        cancelText={t("action.cancel")}
        confirmText={t("action.delete")}
        onConfirm={deleteVersion}
      />
      <ConfirmDialog
        open={deleteSubjectOpen}
        onOpenChange={setDeleteSubjectOpen}
        title={t("query.kafkaDeleteSubject")}
        description={t("query.kafkaDeleteSubjectConfirmDesc", { subject: detail.subject })}
        cancelText={t("action.cancel")}
        confirmText={t("action.delete")}
        onConfirm={deleteSubject}
      />
    </div>
  );
}

function SchemaReferencesTable({ references }: { references: KafkaSchemaReference[] }) {
  const { t } = useTranslation();
  return (
    <div className="overflow-hidden rounded-md border">
      <table className="w-full text-sm">
        <thead className="bg-muted/40 text-xs text-muted-foreground">
          <tr>
            <th className="px-3 py-2 text-left font-medium">{t("query.kafkaSchemaReference")}</th>
            <th className="px-3 py-2 text-left font-medium">{t("query.kafkaSchemaSubject")}</th>
            <th className="px-3 py-2 text-right font-medium">{t("query.kafkaSchemaVersion")}</th>
          </tr>
        </thead>
        <tbody>
          {references.map((reference) => (
            <tr key={`${reference.name}:${reference.subject}:${reference.version}`} className="border-t">
              <td className="px-3 py-2 font-mono text-xs">{reference.name}</td>
              <td className="px-3 py-2 font-mono text-xs">{reference.subject}</td>
              <td className="px-3 py-2 text-right font-mono text-xs">{reference.version}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function RegisterSchemaDialog({
  tabId,
  open,
  onOpenChange,
}: {
  tabId: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useTranslation();
  const registerSchema = useKafkaStore((s) => s.registerSchema);
  const checkSchemaCompatibility = useKafkaStore((s) => s.checkSchemaCompatibility);
  const state = useKafkaStore((s) => s.states[tabId]);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [subject, setSubject] = useState("");
  const [schemaType, setSchemaType] = useState("AVRO");
  const [schema, setSchema] = useState("");
  const [references, setReferences] = useState("");
  const [compatibility, setCompatibility] = useState<KafkaSchemaCompatibilityResponse | null>(null);
  const canSubmit = subject.trim() && schema.trim();

  const request = (): KafkaRegisterSchemaRequest => ({
    subject: subject.trim(),
    schema,
    schemaType: schemaType.trim() || undefined,
    references: parseSchemaReferences(references),
  });

  const check = async () => {
    const result = await checkSchemaCompatibility(tabId, { ...request(), version: "latest" });
    setCompatibility(result || null);
  };

  const submit = async () => {
    await registerSchema(tabId, request());
    setSubject("");
    setSchema("");
    setReferences("");
    setCompatibility(null);
    onOpenChange(false);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>{t("query.kafkaRegisterSchema")}</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <div className="grid grid-cols-[1fr_140px] gap-2">
            <Input
              className="h-8 font-mono text-xs"
              value={subject}
              onChange={(e) => setSubject(e.target.value)}
              placeholder={t("query.kafkaSchemaSubject")}
            />
            <CompactSelect value={schemaType} onChange={setSchemaType} items={["AVRO", "JSON", "PROTOBUF"]} />
          </div>
          <Textarea
            className="min-h-64 font-mono text-xs"
            value={schema}
            onChange={(e) => {
              setSchema(e.target.value);
              setCompatibility(null);
            }}
            placeholder={t("query.kafkaSchemaPlaceholder")}
          />
          <Textarea
            className="min-h-16 font-mono text-xs"
            value={references}
            onChange={(e) => setReferences(e.target.value)}
            placeholder={t("query.kafkaSchemaReferencesPlaceholder")}
          />
          {compatibility && (
            <div
              className={`rounded-md border px-3 py-2 text-sm ${
                compatibility.compatible ? "border-success/30 bg-success/10" : "border-destructive/30 bg-destructive/10"
              }`}
            >
              {compatibility.compatible ? t("query.kafkaSchemaCompatible") : t("query.kafkaSchemaIncompatible")}
            </div>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("action.cancel")}
          </Button>
          <Button variant="outline" disabled={state?.schemaAdminLoading || !canSubmit} onClick={check}>
            {state?.schemaAdminLoading && <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />}
            {t("query.kafkaCheckCompatibility")}
          </Button>
          <Button disabled={state?.schemaAdminLoading || !canSubmit} onClick={() => setConfirmOpen(true)}>
            {state?.schemaAdminLoading && <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />}
            {t("query.kafkaRegisterSchema")}
          </Button>
        </DialogFooter>
        <ConfirmDialog
          open={confirmOpen}
          onOpenChange={setConfirmOpen}
          title={t("query.kafkaRegisterSchema")}
          description={t("query.kafkaRegisterSchemaConfirmDesc", { subject: subject.trim() })}
          cancelText={t("action.cancel")}
          confirmText={t("query.kafkaRegisterSchema")}
          onConfirm={submit}
        />
      </DialogContent>
    </Dialog>
  );
}

import { useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@opskat/ui";
import {
  DownloadRDPExcelTemplate,
  ImportRDPExcelSelected,
  ImportRDPSelected,
  ImportSSHConfigSelected,
  ImportTabbySelected,
  ImportWindTermSelected,
  PreviewRDPExcel,
  PreviewRDPFiles,
  PreviewSSHConfig,
  PreviewTabbyConfig,
  PreviewWindTermConfig,
} from "../../../wailsjs/go/system/System";
import { import_svc } from "../../../wailsjs/go/models";
import { ImportDialog, ImportCallOptions } from "@/components/settings/ImportDialog";
import { CircleHelp, FileSpreadsheet, Import } from "lucide-react";
import { toast } from "sonner";
import { notifySuccess } from "@/lib/notify";

const errMsg = (e: unknown) => (e instanceof Error ? e.message : String(e));

export function ImportSection() {
  const { t } = useTranslation();

  const [importPreview, setImportPreview] = useState<import_svc.PreviewResult | null>(null);
  const [importDialogOpen, setImportDialogOpen] = useState(false);
  const [importDialogTitle, setImportDialogTitle] = useState("");
  const [importFn, setImportFn] = useState<
    ((indexes: number[], options: ImportCallOptions) => Promise<import_svc.ImportResult>) | null
  >(null);
  const [tabbyLoading, setTabbyLoading] = useState(false);
  const [windTermLoading, setWindTermLoading] = useState(false);
  const [sshConfigLoading, setSSHConfigLoading] = useState(false);
  const [rdpLoading, setRDPLoading] = useState(false);
  const [rdpExcelLoading, setRDPExcelLoading] = useState(false);
  const [templateLoading, setTemplateLoading] = useState(false);

  const handlePreviewTabby = async () => {
    setTabbyLoading(true);
    try {
      const result = await PreviewTabbyConfig();
      if (result) {
        setImportPreview(result);
        setImportDialogTitle(t("import.tabby"));
        setImportFn(
          () => (indexes: number[], opts: ImportCallOptions) =>
            ImportTabbySelected(indexes, opts.passphrase, opts.overwrite)
        );
        setImportDialogOpen(true);
      }
    } catch (e: unknown) {
      toast.error(errMsg(e));
    } finally {
      setTabbyLoading(false);
    }
  };

  const handlePreviewWindTerm = async () => {
    setWindTermLoading(true);
    try {
      const result = await PreviewWindTermConfig();
      const preview = result?.preview;
      if (preview) {
        setImportPreview(preview);
        setImportDialogTitle(t("import.windTerm"));
        setImportFn(
          () => (indexes: number[], opts: ImportCallOptions) =>
            ImportWindTermSelected(result.sourceId, indexes, opts.overwrite)
        );
        setImportDialogOpen(true);
      }
    } catch (e: unknown) {
      toast.error(errMsg(e));
    } finally {
      setWindTermLoading(false);
    }
  };

  const handlePreviewSSHConfig = async () => {
    setSSHConfigLoading(true);
    try {
      const result = await PreviewSSHConfig();
      if (result) {
        setImportPreview(result);
        setImportDialogTitle(t("import.sshConfig"));
        setImportFn(
          () => (indexes: number[], opts: ImportCallOptions) => ImportSSHConfigSelected(indexes, opts.overwrite)
        );
        setImportDialogOpen(true);
      }
    } catch (e: unknown) {
      toast.error(errMsg(e));
    } finally {
      setSSHConfigLoading(false);
    }
  };

  const handlePreviewRDP = async () => {
    setRDPLoading(true);
    try {
      const result = await PreviewRDPFiles();
      const preview = result?.preview;
      if (preview) {
        setImportPreview(preview);
        setImportDialogTitle(t("import.rdp"));
        setImportFn(
          () => (indexes: number[], opts: ImportCallOptions) =>
            ImportRDPSelected(result.sourceId, indexes, opts.overwrite)
        );
        setImportDialogOpen(true);
      }
    } catch (e: unknown) {
      toast.error(errMsg(e));
    } finally {
      setRDPLoading(false);
    }
  };

  const handlePreviewRDPExcel = async () => {
    setRDPExcelLoading(true);
    try {
      const result = await PreviewRDPExcel();
      const preview = result?.preview;
      if (preview) {
        setImportPreview(preview);
        setImportDialogTitle(t("import.rdpExcel"));
        setImportFn(
          () => (indexes: number[], opts: ImportCallOptions) =>
            ImportRDPExcelSelected(result.sourceId, indexes, opts.overwrite)
        );
        setImportDialogOpen(true);
      }
    } catch (e: unknown) {
      toast.error(errMsg(e));
    } finally {
      setRDPExcelLoading(false);
    }
  };

  const handleDownloadTemplate = async () => {
    setTemplateLoading(true);
    try {
      const path = await DownloadRDPExcelTemplate();
      if (path) notifySuccess(t("import.rdpExcelTemplateSaved", { path }));
    } catch (e: unknown) {
      toast.error(errMsg(e));
    } finally {
      setTemplateLoading(false);
    }
  };

  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Tabby</CardTitle>
          <CardDescription>{t("import.tabbyDesc")}</CardDescription>
        </CardHeader>
        <CardContent>
          <Button onClick={handlePreviewTabby} disabled={tabbyLoading} variant="outline" className="gap-1">
            <Import className="h-4 w-4" />
            {tabbyLoading ? t("import.importing") : t("import.tabby")}
          </Button>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-1.5 text-base">
            <span>WindTerm</span>
            <Tooltip>
              <TooltipTrigger asChild>
                <CircleHelp className="h-3.5 w-3.5 cursor-help text-muted-foreground" />
              </TooltipTrigger>
              <TooltipContent className="max-w-64 text-xs">{t("import.windTermHint")}</TooltipContent>
            </Tooltip>
          </CardTitle>
          <CardDescription>{t("import.windTermDesc")}</CardDescription>
        </CardHeader>
        <CardContent>
          <Button onClick={handlePreviewWindTerm} disabled={windTermLoading} variant="outline" className="gap-1">
            <Import className="h-4 w-4" />
            {windTermLoading ? t("import.importing") : t("import.windTerm")}
          </Button>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle className="text-base">SSH Config</CardTitle>
          <CardDescription>{t("import.sshConfigDesc")}</CardDescription>
        </CardHeader>
        <CardContent>
          <Button onClick={handlePreviewSSHConfig} disabled={sshConfigLoading} variant="outline" className="gap-1">
            <Import className="h-4 w-4" />
            {sshConfigLoading ? t("import.importing") : t("import.sshConfig")}
          </Button>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle className="text-base">RDP</CardTitle>
          <CardDescription>{t("import.rdpDesc")}</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-wrap gap-2">
          <Button onClick={handlePreviewRDP} disabled={rdpLoading} variant="outline" className="gap-1">
            <Import className="h-4 w-4" />
            {rdpLoading ? t("import.importing") : t("import.rdp")}
          </Button>
          <Button onClick={handleDownloadTemplate} disabled={templateLoading} variant="outline" className="gap-1">
            <FileSpreadsheet className="h-4 w-4" />
            {templateLoading ? t("import.importing") : t("import.rdpExcelTemplate")}
          </Button>
          <Button onClick={handlePreviewRDPExcel} disabled={rdpExcelLoading} variant="outline" className="gap-1">
            <Import className="h-4 w-4" />
            {rdpExcelLoading ? t("import.importing") : t("import.rdpExcel")}
          </Button>
        </CardContent>
      </Card>

      <ImportDialog
        open={importDialogOpen}
        onOpenChange={setImportDialogOpen}
        preview={importPreview}
        title={importDialogTitle}
        onImport={importFn!}
      />
    </>
  );
}

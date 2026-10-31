import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Input,
  Label,
} from "@opskat/ui";
import { CheckCircle2, Loader2, PlugZap, XCircle } from "lucide-react";
import { CancelTest, TestNetworkProxy } from "../../../wailsjs/go/system/System";
import type { system } from "../../../wailsjs/go/models";
import { isNetworkProxyFormValid, toNetworkProxyInput, type NetworkProxyForm } from "./networkProxyForm";

const LAST_URL_KEY = "opskat.networkProxy.lastTestUrl";

const errMsg = (e: unknown) => (e instanceof Error ? e.message : String(e));
const isHttpUrl = (s: string) => /^https?:\/\/\S+$/i.test(s);

type TestState =
  | { state: "idle" }
  | { state: "testing" }
  | { state: "done"; result: system.NetworkProxyTestResult }
  | { state: "rejected"; message: string };

const resultBox = "flex items-start gap-2 rounded-md border px-3 py-2 text-xs";

// 测试连接对话框：用卡片上当前填写（可能未保存）的设置访问用户给定的地址；只测试，不保存。
// 关闭对话框（任何方式）会取消进行中的测试并丢弃其结果。
export function NetworkProxyTestDialog({
  open,
  form,
  onOpenChange,
}: {
  open: boolean;
  form: NetworkProxyForm;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useTranslation();
  const [url, setUrl] = useState(() => localStorage.getItem(LAST_URL_KEY) ?? "");
  const [test, setTest] = useState<TestState>({ state: "idle" });
  // 进行中的测试 id；结束、关闭或被新一轮取代后清空，晚到的结果据此丢弃。
  const activeId = useRef<string | null>(null);

  const cancelActive = useCallback(() => {
    if (activeId.current) void CancelTest(activeId.current);
    activeId.current = null;
  }, []);

  // 卸载（如设置页被关掉）同关闭对话框：取消进行中的测试。
  useEffect(() => cancelActive, [cancelActive]);

  const trimmed = url.trim();
  const urlInvalid = trimmed !== "" && !isHttpUrl(trimmed);
  const formValid = isNetworkProxyFormValid(form);
  const testing = test.state === "testing";

  const handleRun = async () => {
    const id = crypto.randomUUID();
    activeId.current = id;
    localStorage.setItem(LAST_URL_KEY, trimmed);
    setTest({ state: "testing" });
    let next: TestState;
    try {
      next = { state: "done", result: await TestNetworkProxy(id, toNetworkProxyInput(form), trimmed) };
    } catch (e: unknown) {
      next = { state: "rejected", message: errMsg(e) };
    }
    if (activeId.current !== id) return;
    activeId.current = null;
    setTest(next);
  };

  const handleOpenChange = (next: boolean) => {
    if (!next) cancelActive();
    onOpenChange(next);
  };

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("network.proxy.test.title")}</DialogTitle>
          <DialogDescription>{t("network.proxy.test.desc")}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-3">
          <div className="grid gap-1.5">
            <Label htmlFor="network-proxy-test-url">{t("network.proxy.test.url")}</Label>
            <Input
              id="network-proxy-test-url"
              className="font-mono text-xs"
              value={url}
              disabled={testing}
              placeholder={t("network.proxy.test.urlPlaceholder")}
              aria-invalid={urlInvalid ? true : undefined}
              onChange={(e) => setUrl(e.target.value)}
            />
            {urlInvalid && (
              <p role="alert" className="text-xs text-destructive">
                {t("network.proxy.test.urlInvalid")}
              </p>
            )}
          </div>
          {!formValid && (
            <p role="alert" className="text-xs text-destructive">
              {t("network.proxy.test.fixForm")}
            </p>
          )}
          {test.state === "done" && test.result.ok && <SuccessBox result={test.result} />}
          {test.state === "done" && !test.result.ok && (
            <div className={`${resultBox} border-destructive/30 bg-destructive/10 text-destructive`}>
              <XCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              <div className="grid min-w-0 gap-1">
                <span className="font-medium">{t("network.proxy.test.failed")}</span>
                <span>{t(`network.proxy.test.reason.${test.result.reason}`)}</span>
                <span className="break-all font-mono text-[11px] opacity-80">{test.result.detail}</span>
              </div>
            </div>
          )}
          {test.state === "rejected" && (
            <div className={`${resultBox} border-destructive/30 bg-destructive/10 text-destructive`}>
              <XCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              <div className="grid min-w-0 gap-1">
                <span className="font-medium">{t("network.proxy.test.failed")}</span>
                <span className="break-all">{test.message}</span>
              </div>
            </div>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => handleOpenChange(false)}>
            {t("action.close")}
          </Button>
          <Button
            className="gap-1.5"
            disabled={testing || trimmed === "" || urlInvalid || !formValid}
            onClick={() => void handleRun()}
          >
            {testing ? <Loader2 className="h-4 w-4 animate-spin" /> : <PlugZap className="h-4 w-4" />}
            {testing ? t("network.proxy.test.running") : t("network.proxy.test.run")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function SuccessBox({ result }: { result: system.NetworkProxyTestResult }) {
  const { t } = useTranslation();
  return (
    <div className={`${resultBox} border-success/30 bg-success/10 text-success`}>
      <CheckCircle2 className="mt-0.5 h-3.5 w-3.5 shrink-0" />
      <div className="grid min-w-0 gap-1">
        <span className="font-medium">{t("network.proxy.test.success")}</span>
        <Fact
          label={t("network.proxy.test.via")}
          value={result.via === "direct" ? t("network.proxy.test.direct") : result.via}
        />
        <Fact label={t("network.proxy.test.host")} value={result.host} />
        <Fact label={t("network.proxy.test.status")} value={String(result.status)} />
        <Fact label={t("network.proxy.test.elapsed")} value={`${result.elapsedMs} ms`} />
      </div>
    </div>
  );
}

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex gap-2">
      <span className="shrink-0 opacity-70">{label}</span>
      <span className="break-all font-mono">{value}</span>
    </div>
  );
}

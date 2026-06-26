import { forwardRef, useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@opskat/ui";
import { RefreshCw } from "lucide-react";
import { Field } from "@/components/asset/fields";
import { useConfigSection } from "@/components/asset/useConfigSection";
import { ListSerialPorts } from "../../../wailsjs/go/serial/Serial";
import type { AssetFormHandle, ConfigSectionProps } from "@/lib/assetTypes/formContract";
import {
  buildSerialConfig,
  parseSerialConfig,
  SERIAL_DEFAULTS,
  type SerialFormState,
} from "./SerialConfigSection.config";

interface SerialPortInfo {
  name: string;
  displayName: string;
  productId?: string;
  vendorId?: string;
  serialNumber?: string;
}

const CUSTOM_PORT = "__custom__";
const NO_PORTS_PLACEHOLDER = "__no_ports__";
const BAUD_RATES = [9600, 19200, 38400, 57600, 115200, 230400, 460800, 921600];
const DATA_BITS_OPTIONS = [5, 6, 7, 8];
const STOP_BITS_OPTIONS = ["1", "1.5", "2"];
const PARITY_OPTIONS = ["none", "odd", "even", "mark", "space"];
// "hardware" 走 serial_svc.enableHardwareFlowControl（直接 ioctl 设 CRTSCTS / DCB），
// 因为 go.bug.st/serial v1.6.4 自身不暴露这条配置，而 nativeOpen 会强制关闭它。
const FLOW_CONTROL_OPTIONS = ["none", "hardware"];

export const SerialConfigSection = forwardRef<AssetFormHandle, ConfigSectionProps>(function SerialConfigSection(
  { editAsset, onValidityChange },
  ref
) {
  const { t } = useTranslation();
  const [ports, setPorts] = useState<SerialPortInfo[]>([]);
  const [loadingPorts, setLoadingPorts] = useState(false);
  const [customMode, setCustomMode] = useState(false);
  const { state, patch } = useConfigSection<SerialFormState>({
    ref,
    editAsset,
    onValidityChange,
    init: (a) => (a ? parseSerialConfig(a.Config) : { ...SERIAL_DEFAULTS }),
    validate: (s) => {
      const ok = !!s.portPath.trim();
      return { canTest: ok, canSave: ok, saveDisabledReason: ok ? "" : "asset.formMissingSerialPort" };
    },
    build: async (s) => ({ configJSON: buildSerialConfig(s), sshTunnelId: 0 }),
    buildTest: async (s) => ({ assetType: "serial", configJSON: buildSerialConfig(s), password: "" }),
  });

  const fetchPorts = useCallback(async () => {
    setLoadingPorts(true);
    try {
      const list = await ListSerialPorts();
      setPorts(list || []);
    } catch {
      setPorts([]);
    } finally {
      setLoadingPorts(false);
    }
  }, []);

  useEffect(() => {
    fetchPorts();
  }, [fetchPorts]);

  // 已保存的端口在当前列表里没出现时（设备拔走、跨平台路径等），自动切到手动输入模式，
  // 让用户能看到原值。注意：这里只单向"开"不"关"——一旦进入手动模式就保留，
  // 用户主动从下拉里选了某个端口才会通过 handlePortSelect 切回非手动模式。
  // 这样刷新串口列表不会把正在编辑的内容覆盖掉。
  useEffect(() => {
    if (state.portPath && !ports.some((p) => p.name === state.portPath)) {
      setCustomMode(true);
    }
  }, [ports, state.portPath]);

  const selectValue = customMode ? CUSTOM_PORT : state.portPath;

  const handlePortSelect = (value: string) => {
    if (value === CUSTOM_PORT) {
      setCustomMode(true);
    } else {
      setCustomMode(false);
      patch({ portPath: value });
    }
  };

  return (
    <div className="flex flex-col gap-4">
      <Field
        label={
          <span className="flex w-full items-center justify-between">
            {t("asset.serialPortPath")}
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="-my-1 h-6 px-2 text-xs"
              onClick={fetchPorts}
              disabled={loadingPorts}
            >
              <RefreshCw className={`h-3 w-3 mr-1 ${loadingPorts ? "animate-spin" : ""}`} />
              {t("asset.serialRefreshPorts")}
            </Button>
          </span>
        }
        required
      >
        <Select value={selectValue} onValueChange={handlePortSelect}>
          <SelectTrigger className="w-full">
            <SelectValue placeholder={t("asset.serialPortPathPlaceholder")} />
          </SelectTrigger>
          <SelectContent>
            {ports.map((p) => (
              <SelectItem key={p.name} value={p.name}>
                {p.displayName}
                {p.serialNumber ? ` (${p.serialNumber})` : ""}
              </SelectItem>
            ))}
            {ports.length === 0 && !loadingPorts && (
              <SelectItem value={NO_PORTS_PLACEHOLDER} disabled>
                {t("asset.serialNoPortsDetected")}
              </SelectItem>
            )}
            <SelectItem value={CUSTOM_PORT}>{t("asset.serialManualInput")}</SelectItem>
          </SelectContent>
        </Select>
        {customMode && (
          <Input
            value={state.portPath}
            onChange={(e) => patch({ portPath: e.target.value })}
            placeholder={t("asset.serialPortPathPlaceholder")}
            className="mt-2 font-mono"
          />
        )}
      </Field>

      <div className="flex items-end gap-3">
        <Field label={t("asset.serialBaudRate")} className="flex-1">
          <Select value={String(state.baudRate)} onValueChange={(v) => patch({ baudRate: Number(v) })}>
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {BAUD_RATES.map((rate) => (
                <SelectItem key={rate} value={String(rate)}>
                  {rate}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <Field label={t("asset.serialDataBits")} className="flex-1">
          <Select value={String(state.dataBits)} onValueChange={(v) => patch({ dataBits: Number(v) })}>
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {DATA_BITS_OPTIONS.map((bits) => (
                <SelectItem key={bits} value={String(bits)}>
                  {bits}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
      </div>

      <div className="flex items-end gap-3">
        <Field label={t("asset.serialStopBits")} className="flex-1">
          <Select value={state.stopBits} onValueChange={(v) => patch({ stopBits: v })}>
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {STOP_BITS_OPTIONS.map((bits) => (
                <SelectItem key={bits} value={bits}>
                  {bits}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <Field label={t("asset.serialParity")} className="flex-1">
          <Select value={state.parity} onValueChange={(v) => patch({ parity: v })}>
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {PARITY_OPTIONS.map((p) => (
                <SelectItem key={p} value={p}>
                  {p}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <Field label={t("asset.serialFlowControl")} className="flex-1">
          <Select value={state.flowControl} onValueChange={(v) => patch({ flowControl: v })}>
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {FLOW_CONTROL_OPTIONS.map((fc) => (
                <SelectItem key={fc} value={fc}>
                  {fc}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
      </div>
    </div>
  );
});

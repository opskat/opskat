import { useState, useEffect, useCallback } from "react";
import { useTranslation } from "react-i18next";
import { ArrowRightLeft, Plus, X, CircleAlert, CircleCheck } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  AddPortForward,
  RemovePortForward,
  ListPortForwards,
} from "../../../wailsjs/go/main/App";
import { ssh_svc } from "../../../wailsjs/go/models";

interface PortForwardPopoverProps {
  sessionId: string | undefined;
  disabled: boolean;
}

export function PortForwardPopover({
  sessionId,
  disabled,
}: PortForwardPopoverProps) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [forwards, setForwards] = useState<ssh_svc.PortForwardInfo[]>([]);
  const [adding, setAdding] = useState(false);

  // 新建表单
  const [fwType, setFwType] = useState("local");
  const [localHost, setLocalHost] = useState("127.0.0.1");
  const [localPort, setLocalPort] = useState("");
  const [remoteHost, setRemoteHost] = useState("127.0.0.1");
  const [remotePort, setRemotePort] = useState("");

  const refresh = useCallback(() => {
    if (!sessionId) {
      setForwards([]);
      return;
    }
    ListPortForwards(sessionId).then((list) => {
      setForwards(list || []);
    });
  }, [sessionId]);

  // sessionId 变化时（首次连接、重连）自动拉取
  useEffect(() => {
    refresh();
  }, [refresh]);

  // 打开弹窗时刷新
  useEffect(() => {
    if (open) {
      refresh();
    }
  }, [open, refresh]);

  const handleAdd = async () => {
    if (!sessionId || !localPort || !remotePort) return;
    try {
      await AddPortForward({
        sessionId,
        type: fwType,
        localHost,
        localPort: parseInt(localPort, 10),
        remoteHost,
        remotePort: parseInt(remotePort, 10),
      });
      setAdding(false);
      setLocalPort("");
      setRemotePort("");
      refresh();
    } catch (e: unknown) {
      console.error("AddPortForward failed:", e);
    }
  };

  const handleRemove = async (id: string) => {
    await RemovePortForward(id);
    refresh();
  };

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          variant="ghost"
          size="icon-xs"
          title={t("ssh.session.portForward")}
          disabled={disabled}
          className="relative"
        >
          <ArrowRightLeft className="h-3.5 w-3.5" />
          {forwards.length > 0 && (
            <span className={`absolute -top-0.5 -right-0.5 h-3 w-3 rounded-full text-[8px] flex items-center justify-center leading-none ${
              forwards.some((f) => f.error) ? "bg-destructive text-destructive-foreground" : "bg-primary text-primary-foreground"
            }`}>
              {forwards.length}
            </span>
          )}
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-80 p-3" align="end">
        <div className="flex items-center justify-between mb-2">
          <span className="text-sm font-medium">
            {t("ssh.session.portForward")}
          </span>
          <Button
            variant="ghost"
            size="icon-xs"
            onClick={() => setAdding(!adding)}
            title={t("ssh.session.addForward")}
          >
            <Plus className="h-3.5 w-3.5" />
          </Button>
        </div>

        {/* 当前列表 */}
        {forwards.length === 0 && !adding && (
          <div className="text-xs text-muted-foreground py-2 text-center">
            {t("ssh.session.noForwards")}
          </div>
        )}
        {forwards.map((fw) => {
          const prefix = fw.type === "remote" ? "R" : "L";
          const label = `${prefix}:${fw.localPort}\u2192${fw.remoteHost || "localhost"}:${fw.remotePort}`;
          return (
            <div
              key={fw.id}
              className="flex items-center gap-1.5 py-1 text-xs font-mono"
            >
              {fw.error ? (
                <span title={fw.error} className="shrink-0 cursor-help">
                  <CircleAlert className="h-3.5 w-3.5 text-destructive" />
                </span>
              ) : (
                <CircleCheck className="h-3.5 w-3.5 shrink-0 text-green-500" />
              )}
              <span className="truncate flex-1" title={fw.error || undefined}>
                {label}
              </span>
              <Button
                variant="ghost"
                size="icon-xs"
                onClick={() => handleRemove(fw.id)}
              >
                <X className="h-3 w-3" />
              </Button>
            </div>
          );
        })}

        {/* 添加表单 */}
        {adding && (
          <div className="border-t pt-2 mt-2 space-y-2">
            <Select value={fwType} onValueChange={setFwType}>
              <SelectTrigger className="h-7 text-xs">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="local">Local (L)</SelectItem>
                <SelectItem value="remote">Remote (R)</SelectItem>
              </SelectContent>
            </Select>
            <div className="flex gap-1 items-center text-xs">
              <Input
                className="h-7 text-xs flex-1"
                placeholder="127.0.0.1"
                value={localHost}
                onChange={(e) => setLocalHost(e.target.value)}
              />
              <span>:</span>
              <Input
                className="h-7 text-xs w-16"
                placeholder={t("ssh.session.port")}
                value={localPort}
                onChange={(e) => setLocalPort(e.target.value)}
              />
            </div>
            <div className="text-center text-muted-foreground text-xs">&darr;</div>
            <div className="flex gap-1 items-center text-xs">
              <Input
                className="h-7 text-xs flex-1"
                placeholder="127.0.0.1"
                value={remoteHost}
                onChange={(e) => setRemoteHost(e.target.value)}
              />
              <span>:</span>
              <Input
                className="h-7 text-xs w-16"
                placeholder={t("ssh.session.port")}
                value={remotePort}
                onChange={(e) => setRemotePort(e.target.value)}
              />
            </div>
            <Button
              size="sm"
              className="w-full h-7 text-xs"
              onClick={handleAdd}
              disabled={!localPort || !remotePort}
            >
              {t("ssh.session.addForward")}
            </Button>
          </div>
        )}
      </PopoverContent>
    </Popover>
  );
}

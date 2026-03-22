import { useState, useEffect } from "react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { useAssetStore } from "@/stores/assetStore";
import { asset_entity } from "../../../wailsjs/go/models";

interface AssetFormProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  editAsset?: asset_entity.Asset | null;
  defaultGroupId?: number;
}

interface SSHConfig {
  host: string;
  port: number;
  username: string;
  auth_type: string;
}

export function AssetForm({
  open,
  onOpenChange,
  editAsset,
  defaultGroupId = 0,
}: AssetFormProps) {
  const { t } = useTranslation();
  const { createAsset, updateAsset, groups } = useAssetStore();

  const [name, setName] = useState("");
  const [groupId, setGroupId] = useState(0);
  const [description, setDescription] = useState("");
  const [host, setHost] = useState("");
  const [port, setPort] = useState(22);
  const [username, setUsername] = useState("root");
  const [authType, setAuthType] = useState("password");
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (open) {
      if (editAsset) {
        setName(editAsset.Name);
        setGroupId(editAsset.GroupID);
        setDescription(editAsset.Description);
        try {
          const cfg: SSHConfig = JSON.parse(editAsset.Config || "{}");
          setHost(cfg.host || "");
          setPort(cfg.port || 22);
          setUsername(cfg.username || "root");
          setAuthType(cfg.auth_type || "password");
        } catch {
          setHost("");
          setPort(22);
          setUsername("root");
          setAuthType("password");
        }
      } else {
        setName("");
        setGroupId(defaultGroupId);
        setDescription("");
        setHost("");
        setPort(22);
        setUsername("root");
        setAuthType("password");
      }
    }
  }, [open, editAsset, defaultGroupId]);

  const handleSubmit = async () => {
    const config = JSON.stringify({
      host,
      port,
      username,
      auth_type: authType,
    } satisfies SSHConfig);

    const asset = new asset_entity.Asset({
      ...(editAsset || {}),
      Name: name,
      Type: "ssh",
      GroupID: groupId,
      Description: description,
      Config: config,
    });

    setSaving(true);
    try {
      if (editAsset?.ID) {
        asset.ID = editAsset.ID;
        await updateAsset(asset);
      } else {
        await createAsset(asset);
      }
      onOpenChange(false);
    } catch (e) {
      toast.error(String(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>
            {editAsset ? t("action.edit") : t("action.add")} SSH{" "}
            {t("asset.title")}
          </DialogTitle>
        </DialogHeader>
        <div className="grid gap-4 py-2">
          <div className="grid gap-2">
            <Label>{t("asset.name")}</Label>
            <Input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="web-01"
            />
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div className="grid gap-2">
              <Label>{t("asset.host")}</Label>
              <Input
                value={host}
                onChange={(e) => setHost(e.target.value)}
                placeholder="192.168.1.1"
              />
            </div>
            <div className="grid gap-2">
              <Label>{t("asset.port")}</Label>
              <Input
                type="number"
                value={port}
                onChange={(e) => setPort(Number(e.target.value))}
              />
            </div>
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div className="grid gap-2">
              <Label>{t("asset.username")}</Label>
              <Input
                value={username}
                onChange={(e) => setUsername(e.target.value)}
              />
            </div>
            <div className="grid gap-2">
              <Label>{t("asset.authType")}</Label>
              <Select value={authType} onValueChange={setAuthType}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="password">
                    {t("asset.authPassword")}
                  </SelectItem>
                  <SelectItem value="key">{t("asset.authKey")}</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>
          <div className="grid gap-2">
            <Label>{t("asset.group")}</Label>
            <Select
              value={String(groupId)}
              onValueChange={(v) => setGroupId(Number(v))}
            >
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="0">{t("asset.ungrouped")}</SelectItem>
                {groups.map((g) => (
                  <SelectItem key={g.ID} value={String(g.ID)}>
                    {g.Name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="grid gap-2">
            <Label>{t("asset.description")}</Label>
            <Textarea
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              rows={2}
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("action.cancel")}
          </Button>
          <Button onClick={handleSubmit} disabled={saving || !name || !host}>
            {t("action.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

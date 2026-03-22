import { useState } from "react";
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

interface ConnectDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  assetName: string;
  authType: string;
  onConnect: (password: string) => void;
}

export function ConnectDialog({
  open,
  onOpenChange,
  assetName,
  authType,
  onConnect,
}: ConnectDialogProps) {
  const { t } = useTranslation();
  const [password, setPassword] = useState("");
  const [connecting, setConnecting] = useState(false);

  const handleConnect = async () => {
    setConnecting(true);
    try {
      onConnect(password);
      onOpenChange(false);
      setPassword("");
    } finally {
      setConnecting(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>
            {t("ssh.connect")} - {assetName}
          </DialogTitle>
        </DialogHeader>
        <div className="grid gap-4 py-2">
          {authType === "password" && (
            <div className="grid gap-2">
              <Label>{t("ssh.password")}</Label>
              <Input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                onKeyDown={(e) => e.key === "Enter" && handleConnect()}
                autoFocus
              />
            </div>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("action.cancel")}
          </Button>
          <Button onClick={handleConnect} disabled={connecting}>
            {t("ssh.connect")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

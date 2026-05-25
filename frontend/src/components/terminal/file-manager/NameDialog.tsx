import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, Button, Input } from "@opskat/ui";

interface NameDialogProps {
  open: boolean;
  title: string;
  placeholder: string;
  onCancel: () => void;
  onSubmit: (name: string) => void;
}

interface NameDialogBodyProps {
  title: string;
  placeholder: string;
  onCancel: () => void;
  onSubmit: (name: string) => void;
}

function NameDialogBody({ title, placeholder, onCancel, onSubmit }: NameDialogBodyProps) {
  const { t } = useTranslation();
  const [name, setName] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    requestAnimationFrame(() => inputRef.current?.focus());
  }, []);

  const submit = () => {
    const trimmed = name.trim();
    if (!trimmed) return;
    onSubmit(trimmed);
  };

  return (
    <DialogContent className="sm:max-w-sm">
      <DialogHeader>
        <DialogTitle>{title}</DialogTitle>
      </DialogHeader>
      <Input
        ref={inputRef}
        value={name}
        placeholder={placeholder}
        onChange={(e) => setName(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") submit();
          if (e.key === "Escape") onCancel();
        }}
      />
      <DialogFooter>
        <Button variant="outline" onClick={onCancel}>
          {t("action.cancel")}
        </Button>
        <Button onClick={submit} disabled={!name.trim()}>
          {t("action.ok")}
        </Button>
      </DialogFooter>
    </DialogContent>
  );
}

export function NameDialog({ open, title, placeholder, onCancel, onSubmit }: NameDialogProps) {
  return (
    <Dialog open={open} onOpenChange={(next) => !next && onCancel()}>
      {open && <NameDialogBody title={title} placeholder={placeholder} onCancel={onCancel} onSubmit={onSubmit} />}
    </Dialog>
  );
}

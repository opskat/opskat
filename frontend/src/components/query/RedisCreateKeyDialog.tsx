import { useCallback, useState } from "react";
import { useTranslation } from "react-i18next";
import { Loader2 } from "lucide-react";
import { toast } from "sonner";
import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Input,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@opskat/ui";
import {
  RedisHashSet,
  RedisListPush,
  RedisSetAdd,
  RedisSetStringValue,
  RedisStreamAdd,
  RedisZSetAdd,
} from "../../../wailsjs/go/app/App";

type RedisCreateType = "string" | "hash" | "list" | "set" | "zset" | "stream";

interface RedisCreateKeyDialogProps {
  open: boolean;
  assetId: number;
  db: number;
  onOpenChange: (open: boolean) => void;
  onCreated: (key: string) => void | Promise<void>;
}

const CREATE_TYPES: RedisCreateType[] = ["string", "hash", "list", "set", "zset", "stream"];

export function RedisCreateKeyDialog({
  open,
  assetId,
  db,
  onOpenChange,
  onCreated,
}: RedisCreateKeyDialogProps) {
  const { t } = useTranslation();
  const [type, setType] = useState<RedisCreateType>("string");
  const [keyName, setKeyName] = useState("");
  const [value, setValue] = useState("");
  const [field, setField] = useState("");
  const [member, setMember] = useState("");
  const [score, setScore] = useState("0");
  const [entryId, setEntryId] = useState("*");
  const [submitting, setSubmitting] = useState(false);

  const reset = useCallback(() => {
    setType("string");
    setKeyName("");
    setValue("");
    setField("");
    setMember("");
    setScore("0");
    setEntryId("*");
    setSubmitting(false);
  }, []);

  const close = useCallback(() => {
    reset();
    onOpenChange(false);
  }, [onOpenChange, reset]);

  const submit = useCallback(async () => {
    const key = keyName.trim();
    if (!key) {
      toast.error(t("query.redisKeyNameRequired"));
      return;
    }
    if ((type === "hash" || type === "stream") && !field.trim()) {
      toast.error(t("query.redisFieldRequired"));
      return;
    }
    if ((type === "set" || type === "zset") && !member.trim()) {
      toast.error(t("query.redisMemberRequired"));
      return;
    }
    const parsedScore = Number(score);
    if (type === "zset" && Number.isNaN(parsedScore)) {
      toast.error(t("query.redisScoreInvalid"));
      return;
    }

    setSubmitting(true);
    try {
      if (type === "string") {
        await RedisSetStringValue({ assetId, db, key, value, format: "raw" });
      } else if (type === "hash") {
        await RedisHashSet(assetId, db, key, field.trim(), value);
      } else if (type === "list") {
        await RedisListPush(assetId, db, key, value);
      } else if (type === "set") {
        await RedisSetAdd(assetId, db, key, member.trim());
      } else if (type === "zset") {
        await RedisZSetAdd(assetId, db, key, member.trim(), parsedScore);
      } else {
        await RedisStreamAdd(assetId, db, key, entryId.trim() || "*", [{ field: field.trim(), value }]);
      }
      await onCreated(key);
      close();
    } catch (err) {
      toast.error(String(err));
      setSubmitting(false);
    }
  }, [assetId, close, db, entryId, field, keyName, member, onCreated, score, t, type, value]);

  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (nextOpen) {
          onOpenChange(true);
        } else if (!submitting) {
          close();
        }
      }}
    >
      <DialogContent className="max-w-lg" showCloseButton={!submitting}>
        <DialogHeader>
          <DialogTitle>{t("query.createRedisKey")}</DialogTitle>
          <DialogDescription>{t("query.createRedisKeyDesc")}</DialogDescription>
        </DialogHeader>

        <div className="space-y-3 py-1">
          <div className="grid grid-cols-[96px_1fr] items-center gap-2">
            <label className="text-xs text-muted-foreground">{t("query.redisKeyName")}</label>
            <Input
              className="h-8 font-mono text-xs"
              placeholder={t("query.redisKeyNamePlaceholder")}
              value={keyName}
              onChange={(e) => setKeyName(e.target.value)}
              disabled={submitting}
            />
          </div>
          <div className="grid grid-cols-[96px_1fr] items-center gap-2">
            <label className="text-xs text-muted-foreground">{t("query.redisKeyType")}</label>
            <Select value={type} onValueChange={(val) => setType(val as RedisCreateType)} disabled={submitting}>
              <SelectTrigger className="h-8 text-xs">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {CREATE_TYPES.map((item) => (
                  <SelectItem key={item} value={item}>
                    {item}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          {(type === "hash" || type === "stream") && (
            <div className="grid grid-cols-[96px_1fr] items-center gap-2">
              <label className="text-xs text-muted-foreground">{t("query.field")}</label>
              <Input
                className="h-8 font-mono text-xs"
                placeholder={t("query.newField")}
                value={field}
                onChange={(e) => setField(e.target.value)}
                disabled={submitting}
              />
            </div>
          )}

          {type === "stream" && (
            <div className="grid grid-cols-[96px_1fr] items-center gap-2">
              <label className="text-xs text-muted-foreground">{t("query.streamEntryId")}</label>
              <Input
                className="h-8 font-mono text-xs"
                placeholder={t("query.streamEntryId")}
                value={entryId}
                onChange={(e) => setEntryId(e.target.value)}
                disabled={submitting}
              />
            </div>
          )}

          {(type === "set" || type === "zset") && (
            <div className="grid grid-cols-[96px_1fr] items-center gap-2">
              <label className="text-xs text-muted-foreground">{t("query.member")}</label>
              <Input
                className="h-8 font-mono text-xs"
                placeholder={t("query.newMember")}
                value={member}
                onChange={(e) => setMember(e.target.value)}
                disabled={submitting}
              />
            </div>
          )}

          {type === "zset" && (
            <div className="grid grid-cols-[96px_1fr] items-center gap-2">
              <label className="text-xs text-muted-foreground">{t("query.score")}</label>
              <Input
                className="h-8 font-mono text-xs"
                placeholder={t("query.newScore")}
                value={score}
                onChange={(e) => setScore(e.target.value)}
                disabled={submitting}
              />
            </div>
          )}

          {type !== "set" && type !== "zset" && (
            <div className="grid grid-cols-[96px_1fr] items-center gap-2">
              <label className="text-xs text-muted-foreground">{t("query.value")}</label>
              <Input
                className="h-8 font-mono text-xs"
                placeholder={t("query.newValue")}
                value={value}
                onChange={(e) => setValue(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") submit();
                }}
                disabled={submitting}
              />
            </div>
          )}
        </div>

        <DialogFooter>
          <Button variant="outline" size="sm" onClick={close} disabled={submitting}>
            {t("action.cancel")}
          </Button>
          <Button size="sm" onClick={submit} disabled={submitting}>
            {submitting ? <Loader2 className="mr-1 size-3 animate-spin" /> : null}
            {t("query.createRedisKeySubmit")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

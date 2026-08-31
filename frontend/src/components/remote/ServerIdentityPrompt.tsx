import { AlertTriangle, Check, Fingerprint, X } from "lucide-react";
import { Button } from "@opskat/ui";

export interface ServerIdentity {
  host: string;
  port: number;
  keyType: string;
  fingerprint: string;
  oldFingerprint?: string;
  isChanged: boolean;
}

interface ServerIdentityPromptProps {
  identity: ServerIdentity;
  changedWarning: string;
  oldFingerprintLabel: string;
  rejectLabel: string;
  trustLabel: string;
  onReject: () => void;
  onTrust: () => void;
  trustDestructive?: boolean;
  acceptOnceLabel?: string;
  onAcceptOnce?: () => void;
  testIdPrefix: string;
}

export function ServerIdentityPrompt({
  identity,
  changedWarning,
  oldFingerprintLabel,
  rejectLabel,
  trustLabel,
  onReject,
  onTrust,
  trustDestructive,
  acceptOnceLabel,
  onAcceptOnce,
  testIdPrefix,
}: ServerIdentityPromptProps) {
  return (
    <div className="w-full max-w-sm space-y-3">
      {identity.isChanged && (
        <div className="flex items-start gap-2 rounded-md border border-destructive/50 bg-destructive/10 p-3">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-destructive" />
          <p className="text-xs text-destructive">{changedWarning}</p>
        </div>
      )}
      <div className="select-text space-y-2 rounded-md border bg-muted/30 p-3">
        <div className="select-text flex items-center gap-2 text-xs text-muted-foreground">
          <Fingerprint className="h-3.5 w-3.5" />
          <span>
            {identity.host}:{identity.port}
          </span>
          <span className="text-muted-foreground/60">({identity.keyType})</span>
        </div>
        <div className="select-text break-all font-mono text-xs text-foreground">{identity.fingerprint}</div>
        {identity.isChanged && identity.oldFingerprint && (
          <div className="mt-2 border-t pt-2">
            <div className="mb-1 text-xs text-muted-foreground">{oldFingerprintLabel}</div>
            <div className="select-text break-all font-mono text-xs text-muted-foreground line-through">
              {identity.oldFingerprint}
            </div>
          </div>
        )}
      </div>
      <div className="flex gap-2">
        <Button
          data-testid={`${testIdPrefix}-reject`}
          size="sm"
          variant="outline"
          onClick={onReject}
          className="flex-1"
        >
          <X className="mr-1 h-3.5 w-3.5" />
          {rejectLabel}
        </Button>
        {acceptOnceLabel && onAcceptOnce && (
          <Button
            data-testid={`${testIdPrefix}-accept-once`}
            size="sm"
            variant="secondary"
            onClick={onAcceptOnce}
            className="flex-1"
          >
            {acceptOnceLabel}
          </Button>
        )}
        <Button
          data-testid={`${testIdPrefix}-trust`}
          size="sm"
          variant={trustDestructive ? "destructive" : "default"}
          onClick={onTrust}
          className="flex-1"
        >
          <Check className="mr-1 h-3.5 w-3.5" />
          {trustLabel}
        </Button>
      </div>
    </div>
  );
}

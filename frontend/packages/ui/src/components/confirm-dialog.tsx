import * as React from "react";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "./alert-dialog";

interface ConfirmDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description: React.ReactNode;
  cancelText?: string;
  confirmText?: string;
  variant?: "default" | "destructive";
  onConfirm: () => void;
  /** Optional test hook placed on the confirm button (e2e). */
  confirmTestId?: string;
}

export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  cancelText,
  confirmText,
  variant = "destructive",
  onConfirm,
  confirmTestId,
}: ConfirmDialogProps) {
  const resolvedCancelText = cancelText?.trim() ? cancelText : "Cancel";
  const resolvedConfirmText = confirmText?.trim() ? confirmText : "Confirm";

  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent onOverlayClick={() => onOpenChange(false)}>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          {/* Radix renders Description as a <p>, but `description` is a ReactNode and callers
              legitimately pass lists / multiple paragraphs — block content inside <p> is invalid
              HTML and React warns on every open. Render the description as a <div> instead. */}
          <AlertDialogDescription asChild>
            <div>{description}</div>
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{resolvedCancelText}</AlertDialogCancel>
          <AlertDialogAction variant={variant} data-testid={confirmTestId} onClick={onConfirm}>
            {resolvedConfirmText}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

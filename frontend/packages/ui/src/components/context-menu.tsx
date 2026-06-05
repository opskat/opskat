import * as React from "react";
import { createPortal } from "react-dom";
import { Slot } from "radix-ui";

import { computeContextMenuPosition } from "../lib/context-menu-position";
import { cn } from "../lib/utils";

// --- Custom ContextMenu with smart positioning ---
// Replaces Radix ContextMenu which hardcodes side/sideOffset/align
// and causes menu items to appear under the cursor on flip.

interface ContextMenuContextValue {
  open: boolean;
  position: { x: number; y: number };
  onOpenChange: (open: boolean) => void;
  setPosition: (pos: { x: number; y: number }) => void;
}

const ContextMenuCtx = React.createContext<ContextMenuContextValue | null>(null);

function useCtx() {
  const ctx = React.useContext(ContextMenuCtx);
  if (!ctx) throw new Error("ContextMenu components must be used within ContextMenu");
  return ctx;
}

function ContextMenu({
  children,
  onOpenChange,
  open: controlledOpen,
}: {
  children: React.ReactNode;
  onOpenChange?: (open: boolean) => void;
  open?: boolean;
}) {
  const [uncontrolledOpen, setUncontrolledOpen] = React.useState(false);
  const open = controlledOpen ?? uncontrolledOpen;
  const [position, setPosition] = React.useState({ x: 0, y: 0 });

  const handleOpenChange = React.useCallback(
    (value: boolean) => {
      if (controlledOpen === undefined) setUncontrolledOpen(value);
      onOpenChange?.(value);
    },
    [controlledOpen, onOpenChange]
  );

  const ctx = React.useMemo(
    () => ({ open, position, onOpenChange: handleOpenChange, setPosition }),
    [open, position, handleOpenChange]
  );

  return <ContextMenuCtx.Provider value={ctx}>{children}</ContextMenuCtx.Provider>;
}

function ContextMenuTrigger({
  children,
  onContextMenu,
  asChild = false,
  ...props
}: React.HTMLAttributes<HTMLElement> & { children: React.ReactNode; asChild?: boolean }) {
  const ctx = useCtx();
  const Comp = asChild ? Slot.Root : "span";

  return (
    <Comp
      data-slot="context-menu-trigger"
      {...props}
      className={cn("select-none", props.className)}
      onContextMenu={(e: React.MouseEvent<HTMLElement>) => {
        onContextMenu?.(e);
        if (e.defaultPrevented) return;
        e.preventDefault();
        e.stopPropagation();
        ctx.setPosition({ x: e.clientX, y: e.clientY });
        ctx.onOpenChange(true);
      }}
    >
      {children}
    </Comp>
  );
}

function ContextMenuContent({
  className,
  style: styleProp,
  children,
  ...props
}: React.HTMLAttributes<HTMLDivElement> & { alignToStylePosition?: boolean }) {
  const { alignToStylePosition = false, ...contentProps } = props;
  const ctx = useCtx();
  const ref = React.useRef<HTMLDivElement>(null);
  const [pos, setPos] = React.useState({ top: 0, left: 0 });
  const [visible, setVisible] = React.useState(false);
  const [interactive, setInteractive] = React.useState(false);

  // Calculate position: default bottom-right, flip if not enough space
  React.useLayoutEffect(() => {
    if (!ctx.open || !ref.current) return;
    const rect = ref.current.getBoundingClientRect();
    const vw = window.innerWidth;
    const vh = window.innerHeight;
    const gap = alignToStylePosition ? 0 : 4;

    const style = styleProp as React.CSSProperties | undefined;
    const anchorX = alignToStylePosition && typeof style?.left === "number" ? style.left : ctx.position.x;
    const anchorY = alignToStylePosition && typeof style?.top === "number" ? style.top : ctx.position.y;

    const next = computeContextMenuPosition({
      anchorX,
      anchorY,
      width: rect.width,
      height: rect.height,
      viewportWidth: vw,
      viewportHeight: vh,
      gap,
    });

    setPos({ top: next.top, left: next.left });
    setVisible(true);
  }, [ctx.open, ctx.position, alignToStylePosition, styleProp]);

  // Enable pointer events after delay to prevent right-click release from
  // accidentally triggering menu items
  React.useEffect(() => {
    if (!ctx.open) {
      setVisible(false);
      setInteractive(false);
      return;
    }
    const timer = setTimeout(() => setInteractive(true), 150);
    return () => clearTimeout(timer);
  }, [ctx.open]);

  // Close on outside pointer, escape key
  React.useEffect(() => {
    if (!ctx.open) return;

    const close = () => ctx.onOpenChange(false);
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") close();
    };
    const onPointerDown = (e: PointerEvent) => {
      if (ref.current?.contains(e.target as Node)) return;
      close();
    };

    // Delay pointer listener to avoid right-click release closing immediately
    const timer = setTimeout(() => {
      document.addEventListener("pointerdown", onPointerDown, true);
    }, 50);
    document.addEventListener("keydown", onKey);

    return () => {
      clearTimeout(timer);
      document.removeEventListener("pointerdown", onPointerDown, true);
      document.removeEventListener("keydown", onKey);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ctx.open, ctx.onOpenChange]);

  if (!ctx.open) return null;

  return createPortal(
    <div
      ref={ref}
      data-slot="context-menu-content"
      role="menu"
      className={cn(
        "z-50 min-w-[8rem] overflow-visible rounded-md border bg-popover p-1 text-popover-foreground shadow-md",
        visible && "animate-in fade-in-0 zoom-in-95",
        className
      )}
      style={{
        ...styleProp,
        position: "fixed",
        top: pos.top,
        left: pos.left,
        visibility: visible ? "visible" : "hidden",
        pointerEvents: interactive ? "auto" : "none",
      }}
      {...contentProps}
    >
      {children}
    </div>,
    document.body
  );
}

function ContextMenuItem({
  className,
  inset,
  variant = "default",
  disabled,
  onClick,
  children,
  ...props
}: React.HTMLAttributes<HTMLDivElement> & {
  inset?: boolean;
  variant?: "default" | "destructive";
  disabled?: boolean;
}) {
  const ctx = useCtx();

  return (
    <div
      data-slot="context-menu-item"
      data-inset={inset}
      data-variant={variant}
      data-disabled={disabled || undefined}
      role="menuitem"
      className={cn(
        "relative flex cursor-default items-center gap-2 rounded-sm px-2 py-1.5 text-sm outline-hidden select-none hover:bg-accent hover:text-accent-foreground data-[disabled]:pointer-events-none data-[disabled]:opacity-50 data-[inset]:pl-8 data-[variant=destructive]:text-destructive data-[variant=destructive]:hover:bg-destructive/10 data-[variant=destructive]:hover:text-destructive dark:data-[variant=destructive]:hover:bg-destructive/20 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4 [&_svg:not([class*='text-'])]:text-muted-foreground data-[variant=destructive]:*:[svg]:text-destructive!",
        className
      )}
      onClick={(e) => {
        if (disabled) return;
        onClick?.(e);
        ctx.onOpenChange(false);
      }}
      {...props}
    >
      {children}
    </div>
  );
}

function ContextMenuSeparator({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      data-slot="context-menu-separator"
      role="separator"
      className={cn("-mx-1 my-1 h-px bg-border", className)}
      {...props}
    />
  );
}

function ContextMenuShortcut({ className, ...props }: React.ComponentProps<"span">) {
  return (
    <span
      data-slot="context-menu-shortcut"
      className={cn("ml-auto text-xs tracking-widest text-muted-foreground", className)}
      {...props}
    />
  );
}

// Stub exports for unused components (preserve API compatibility)
function ContextMenuGroup(props: React.HTMLAttributes<HTMLDivElement>) {
  return <div data-slot="context-menu-group" role="group" {...props} />;
}
function ContextMenuPortal({ children }: { children: React.ReactNode }) {
  return <>{children}</>;
}
function ContextMenuSub({ children }: { children: React.ReactNode }) {
  return <>{children}</>;
}
function ContextMenuRadioGroup(props: React.HTMLAttributes<HTMLDivElement>) {
  return <div data-slot="context-menu-radio-group" role="radiogroup" {...props} />;
}
function ContextMenuSubTrigger(_props: Record<string, unknown>) {
  return null;
}
function ContextMenuSubContent(_props: Record<string, unknown>) {
  return null;
}
function ContextMenuCheckboxItem(_props: Record<string, unknown>) {
  return null;
}
function ContextMenuRadioItem(_props: Record<string, unknown>) {
  return null;
}
function ContextMenuLabel(_props: Record<string, unknown>) {
  return null;
}

export {
  ContextMenu,
  ContextMenuTrigger,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuCheckboxItem,
  ContextMenuRadioItem,
  ContextMenuLabel,
  ContextMenuSeparator,
  ContextMenuShortcut,
  ContextMenuGroup,
  ContextMenuPortal,
  ContextMenuSub,
  ContextMenuSubContent,
  ContextMenuSubTrigger,
  ContextMenuRadioGroup,
};

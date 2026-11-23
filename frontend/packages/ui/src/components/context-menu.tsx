import * as React from "react";
import { createPortal } from "react-dom";
import { Slot } from "radix-ui";
import { ChevronRightIcon } from "lucide-react";

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
  const ctx = React.use(ContextMenuCtx);
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

  return <ContextMenuCtx value={ctx}>{children}</ContextMenuCtx>;
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

  // Reset visibility/interactivity when closed — render-phase self-terminating
  // guard (both states are only ever set true while open, so this settles in one pass).
  if (!ctx.open && (visible || interactive)) {
    setVisible(false);
    setInteractive(false);
  }

  // Enable pointer events after delay to prevent right-click release from
  // accidentally triggering menu items
  React.useEffect(() => {
    if (!ctx.open) return;
    const timer = setTimeout(() => setInteractive(true), 150);
    return () => clearTimeout(timer);
  }, [ctx.open]);

  // Close on outside pointer, escape key
  React.useEffect(() => {
    if (!ctx.open) return;

    const close = () => ctx.onOpenChange(false);
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        close();
        return;
      }
      // 方向键进入菜单、在菜单项之间移动。焦点在别处（终端、输入框）时不接管，免得抢走它们的方向键。
      const menu = ref.current;
      if (!menu || (e.key !== "ArrowDown" && e.key !== "ArrowUp")) return;
      const active = document.activeElement;
      if (active && active !== document.body && !menu.contains(active)) return;
      // 焦点在子菜单里时由子菜单自己移动。
      if (active && menu.contains(active) && active.closest("[role=menu]") !== menu) return;
      e.preventDefault();
      moveMenuFocus(menu, active instanceof HTMLElement ? active : null, e.key === "ArrowDown" ? 1 : -1);
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
  onKeyDown,
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
      aria-disabled={disabled || undefined}
      role="menuitem"
      tabIndex={-1}
      className={cn(
        "relative flex cursor-default items-center gap-2 rounded-sm px-2 py-1.5 text-sm outline-hidden select-none hover:bg-accent hover:text-accent-foreground focus:bg-accent focus:text-accent-foreground data-[disabled]:pointer-events-none data-[disabled]:opacity-50 data-[inset]:pl-8 data-[variant=destructive]:text-destructive data-[variant=destructive]:hover:bg-destructive/10 data-[variant=destructive]:hover:text-destructive dark:data-[variant=destructive]:hover:bg-destructive/20 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4 [&_svg:not([class*='text-'])]:text-muted-foreground data-[variant=destructive]:*:[svg]:text-destructive!",
        className
      )}
      onClick={(e) => {
        if (disabled) return;
        onClick?.(e);
        ctx.onOpenChange(false);
      }}
      onKeyDown={(e) => {
        onKeyDown?.(e);
        if (e.defaultPrevented || (e.key !== "Enter" && e.key !== " ")) return;
        e.preventDefault();
        e.currentTarget.click();
      }}
      {...props}
    >
      {children}
    </div>
  );
}

/** 菜单里可以用键盘到达的项：属于这一层菜单（不在嵌套的子菜单里）、没有禁用。 */
function menuItems(menu: HTMLElement): HTMLElement[] {
  return Array.from(menu.querySelectorAll<HTMLElement>("[role=menuitem]")).filter(
    (item) => item.closest("[role=menu]") === menu && !item.hasAttribute("data-disabled")
  );
}

/** 把焦点移到 current 之后（delta 为 1）或之前（-1）的菜单项，首尾相接；current 不在菜单里时到第一项 / 最后一项。 */
function moveMenuFocus(menu: HTMLElement, current: HTMLElement | null, delta: 1 | -1) {
  const items = menuItems(menu);
  if (items.length === 0) return;
  const index = current ? items.indexOf(current) : -1;
  const next = index === -1 ? (delta === 1 ? 0 : items.length - 1) : (index + delta + items.length) % items.length;
  items[next].focus();
}

// --- Submenu ---
// 子菜单挂在触发项旁边（同一个 DOM 子树里，点在子菜单上不算点在菜单外）：指针移入触发项或子菜单时
// 打开、移出两者时关闭；键盘上 →、Enter、空格打开并聚焦第一项，← 关闭并回到触发项，↑ / ↓ 在子菜单项
// 之间移动。

interface ContextMenuSubContextValue {
  open: boolean;
  setOpen: (open: boolean) => void;
  /** 打开后要聚焦第一项（键盘打开时）。 */
  focusFirst: boolean;
  setFocusFirst: (focus: boolean) => void;
}

const ContextMenuSubCtx = React.createContext<ContextMenuSubContextValue | null>(null);

function useSubCtx() {
  const ctx = React.use(ContextMenuSubCtx);
  if (!ctx) throw new Error("ContextMenuSub components must be used within ContextMenuSub");
  return ctx;
}

function ContextMenuSub({ className, children, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  const [open, setOpen] = React.useState(false);
  const [focusFirst, setFocusFirst] = React.useState(false);
  const ctx = React.useMemo(() => ({ open, setOpen, focusFirst, setFocusFirst }), [open, focusFirst]);

  return (
    <ContextMenuSubCtx value={ctx}>
      <div
        data-slot="context-menu-sub"
        className={cn("relative", className)}
        onPointerEnter={() => setOpen(true)}
        onPointerLeave={() => setOpen(false)}
        {...props}
      >
        {children}
      </div>
    </ContextMenuSubCtx>
  );
}

function ContextMenuSubTrigger({
  className,
  inset,
  disabled,
  children,
  ...props
}: React.HTMLAttributes<HTMLDivElement> & { inset?: boolean; disabled?: boolean }) {
  const sub = useSubCtx();
  // 禁用的触发项不展开(指针移入外层时 open 也可能为真)。
  const expanded = sub.open && !disabled;
  const openWithKeyboard = () => {
    sub.setFocusFirst(true);
    sub.setOpen(true);
  };

  return (
    <div
      data-slot="context-menu-sub-trigger"
      data-inset={inset}
      data-state={expanded ? "open" : "closed"}
      data-disabled={disabled || undefined}
      aria-disabled={disabled || undefined}
      role="menuitem"
      aria-haspopup="menu"
      aria-expanded={expanded}
      tabIndex={-1}
      className={cn(
        "flex cursor-default items-center gap-2 rounded-sm px-2 py-1.5 text-sm outline-hidden select-none hover:bg-accent hover:text-accent-foreground focus:bg-accent focus:text-accent-foreground data-[state=open]:bg-accent data-[state=open]:text-accent-foreground data-[disabled]:pointer-events-none data-[disabled]:opacity-50 data-[inset]:pl-8 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4 [&_svg:not([class*='text-'])]:text-muted-foreground",
        className
      )}
      onClick={() => {
        if (!disabled) sub.setOpen(true);
      }}
      onKeyDown={(e) => {
        if (disabled || (e.key !== "ArrowRight" && e.key !== "Enter" && e.key !== " ")) return;
        e.preventDefault();
        openWithKeyboard();
      }}
      {...props}
    >
      {children}
      <ChevronRightIcon className="ml-auto size-4" />
    </div>
  );
}

function ContextMenuSubContent({ className, children, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  const sub = useSubCtx();
  const ref = React.useRef<HTMLDivElement>(null);
  // 右边放不下时翻到触发项左边。
  const [flip, setFlip] = React.useState(false);
  const { open, focusFirst, setFocusFirst } = sub;

  React.useLayoutEffect(() => {
    if (!open || !ref.current) return;
    const rect = ref.current.getBoundingClientRect();
    setFlip(rect.right > window.innerWidth && rect.left - rect.width > 0);
  }, [open]);

  React.useEffect(() => {
    if (!open || !focusFirst || !ref.current) return;
    setFocusFirst(false);
    moveMenuFocus(ref.current, null, 1);
  }, [open, focusFirst, setFocusFirst]);

  if (!sub.open) return null;

  return (
    <div
      ref={ref}
      data-slot="context-menu-sub-content"
      role="menu"
      className={cn(
        "absolute top-0 z-50 -mt-1 min-w-[8rem] rounded-md border bg-popover p-1 text-popover-foreground shadow-lg animate-in fade-in-0 zoom-in-95",
        flip ? "right-full" : "left-full",
        className
      )}
      onKeyDown={(e) => {
        if (e.key === "ArrowDown" || e.key === "ArrowUp") {
          // 子菜单自己处理：不让外层菜单的方向键再移一次焦点。
          e.preventDefault();
          e.stopPropagation();
          moveMenuFocus(e.currentTarget, document.activeElement as HTMLElement | null, e.key === "ArrowDown" ? 1 : -1);
        } else if (e.key === "ArrowLeft") {
          e.preventDefault();
          e.stopPropagation();
          sub.setOpen(false);
          e.currentTarget.parentElement
            ?.querySelector<HTMLElement>(":scope > [data-slot=context-menu-sub-trigger]")
            ?.focus();
        }
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

export {
  ContextMenu,
  ContextMenuTrigger,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSub,
  ContextMenuSubTrigger,
  ContextMenuSubContent,
  ContextMenuSeparator,
  ContextMenuShortcut,
};

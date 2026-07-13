import { useState, useRef, useEffect, useCallback, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { ChevronDown, ChevronRight, Check, Search } from "lucide-react";
import { Button } from "./button";
import { cn } from "../lib/utils";
import { pinyinMatch } from "../lib/pinyin";

export interface TreeNode {
  id: number;
  label: string;
  icon?: ReactNode;
  children?: TreeNode[];
  /** If false, this node is a non-selectable container (default: true) */
  selectable?: boolean;
}

interface TreeSelectProps {
  value: number;
  onValueChange: (value: number) => void;
  nodes: TreeNode[];
  /** Label for the "none" / zero-value option */
  placeholder?: string;
  /** Icon shown next to placeholder */
  placeholderIcon?: ReactNode;
  /** Whether to show search input (default: false) */
  searchable?: boolean;
  /** Search placeholder text */
  searchPlaceholder?: string;
  /** Custom className for the trigger button */
  className?: string;
  /** Stable test id for the trigger button */
  testId?: string;
}

/** Filter tree nodes by search query (with pinyin support), preserving ancestor paths */
function filterTree(nodes: TreeNode[], query: string): TreeNode[] {
  if (!query) return nodes;

  function matches(node: TreeNode): boolean {
    if (pinyinMatch(node.label, query)) return true;
    if (node.children?.some(matches)) return true;
    return false;
  }

  return nodes.filter(matches).map((node) => ({
    ...node,
    children: node.children ? filterTree(node.children, query) : undefined,
  }));
}

function TreeItem({
  node,
  selectedId,
  onSelect,
  depth = 0,
}: {
  node: TreeNode;
  selectedId: number;
  onSelect: (id: number) => void;
  depth?: number;
}) {
  const [expanded, setExpanded] = useState(true);
  const hasChildren = !!(node.children && node.children.length > 0);
  const selectable = node.selectable !== false;

  return (
    <div>
      <div
        className={`flex items-center gap-1 px-2 py-1.5 rounded text-sm ${
          selectable ? "cursor-pointer hover:bg-accent" : "text-muted-foreground"
        }`}
        style={{ paddingLeft: `${depth * 16 + 8}px` }}
        onClick={() => selectable && onSelect(node.id)}
      >
        {hasChildren ? (
          <button
            type="button"
            className="p-0 h-4 w-4 shrink-0 flex items-center justify-center"
            onClick={(e) => {
              e.stopPropagation();
              setExpanded(!expanded);
            }}
          >
            {expanded ? <ChevronDown className="h-3 w-3" /> : <ChevronRight className="h-3 w-3" />}
          </button>
        ) : (
          <span className="w-4 shrink-0" />
        )}
        {node.icon && <span className="shrink-0">{node.icon}</span>}
        <span className="truncate flex-1">{node.label}</span>
        {selectable && selectedId === node.id && <Check className="h-3.5 w-3.5 shrink-0 text-primary" />}
      </div>
      {hasChildren && expanded && (
        <div>
          {node.children!.map((child) => (
            <TreeItem key={child.id} node={child} selectedId={selectedId} onSelect={onSelect} depth={depth + 1} />
          ))}
        </div>
      )}
    </div>
  );
}

/** Find label for a given id in the tree */
function findLabel(nodes: TreeNode[], id: number): string | undefined {
  for (const node of nodes) {
    if (node.id === id) return node.label;
    if (node.children) {
      const found = findLabel(node.children, id);
      if (found) return found;
    }
  }
  return undefined;
}

/** Find icon for a given id in the tree */
function findIcon(nodes: TreeNode[], id: number): ReactNode | undefined {
  for (const node of nodes) {
    if (node.id === id) return node.icon;
    if (node.children) {
      const found = findIcon(node.children, id);
      if (found) return found;
    }
  }
  return undefined;
}

export function TreeSelect({
  value,
  onValueChange,
  nodes,
  placeholder,
  placeholderIcon,
  searchable = false,
  searchPlaceholder,
  className,
  testId,
}: TreeSelectProps) {
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const containerRef = useRef<HTMLDivElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const dropdownRef = useRef<HTMLDivElement>(null);
  const [position, setPosition] = useState<{ top?: number; bottom?: number; left: number; width: number } | null>(null);

  const displayLabel = value === 0 ? placeholder : findLabel(nodes, value) || placeholder;
  const displayIcon = value === 0 ? placeholderIcon : findIcon(nodes, value) || placeholderIcon;

  const filteredNodes = searchable ? filterTree(nodes, search) : nodes;

  // Portaling the dropdown to <body> keeps it out of overflow-clipping / stacking-context
  // ancestors (e.g. a dialog's scroll body + footer) that would otherwise clip or cover it;
  // in exchange we anchor it to the trigger manually and re-anchor on scroll/resize.
  const updatePosition = useCallback(() => {
    const el = containerRef.current;
    if (!el) return;
    const rect = el.getBoundingClientRect();
    const spaceBelow = window.innerHeight - rect.bottom;
    const openUp = spaceBelow < 260 && rect.top > spaceBelow;
    setPosition({
      top: openUp ? undefined : rect.bottom + 4,
      bottom: openUp ? window.innerHeight - rect.top + 4 : undefined,
      left: rect.left,
      width: rect.width,
    });
  }, []);

  const setDropdownOpen = (nextOpen: boolean) => {
    if (nextOpen) {
      setSearch("");
      updatePosition();
      setTimeout(() => searchRef.current?.focus(), 0);
    } else {
      setPosition(null);
    }
    setOpen(nextOpen);
  };

  useEffect(() => {
    if (!open) return;
    window.addEventListener("scroll", updatePosition, true);
    window.addEventListener("resize", updatePosition);
    return () => {
      window.removeEventListener("scroll", updatePosition, true);
      window.removeEventListener("resize", updatePosition);
    };
  }, [open, updatePosition]);

  // Close dropdown when clicking outside the trigger or the portaled dropdown.
  useEffect(() => {
    if (!open) return;
    const handleClick = (e: MouseEvent) => {
      const target = e.target as Node;
      if (containerRef.current?.contains(target)) return;
      if (dropdownRef.current?.contains(target)) return;
      setOpen(false);
    };
    document.addEventListener("mousedown", handleClick);
    return () => document.removeEventListener("mousedown", handleClick);
  }, [open]);

  return (
    <div ref={containerRef} className="relative">
      <Button
        type="button"
        variant="outline"
        data-testid={testId}
        className={cn("w-full justify-between font-normal", className)}
        onClick={() => setDropdownOpen(!open)}
      >
        <div className="flex items-center gap-2 truncate">
          {displayIcon && <span className="shrink-0">{displayIcon}</span>}
          <span className="truncate">{displayLabel}</span>
        </div>
        <ChevronDown className="h-3.5 w-3.5 shrink-0 opacity-50" />
      </Button>
      {open &&
        position &&
        createPortal(
          <div
            ref={dropdownRef}
            className="fixed z-50 rounded-md border bg-popover p-1 text-popover-foreground shadow-md"
            style={{
              top: position.top,
              bottom: position.bottom,
              left: position.left,
              width: position.width,
              minWidth: "200px",
            }}
            onWheel={(e) => e.stopPropagation()}
          >
            {searchable && (
              <div className="flex items-center gap-1.5 px-2 py-1 border-b mb-1">
                <Search className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                <input
                  ref={searchRef}
                  type="text"
                  className="flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground/55"
                  placeholder={searchPlaceholder}
                  value={search}
                  onChange={(e) => setSearch(e.target.value)}
                  onClick={(e) => e.stopPropagation()}
                />
              </div>
            )}
            <div className="max-h-[200px] overflow-y-auto">
              {/* Zero-value / placeholder option */}
              {placeholder && !search && (
                <div
                  className="flex items-center gap-1 px-2 py-1.5 rounded cursor-pointer hover:bg-accent text-sm"
                  onClick={() => {
                    onValueChange(0);
                    setOpen(false);
                  }}
                >
                  <span className="w-4 shrink-0" />
                  {placeholderIcon && <span className="shrink-0">{placeholderIcon}</span>}
                  <span className="truncate flex-1">{placeholder}</span>
                  {value === 0 && <Check className="h-3.5 w-3.5 shrink-0 text-primary" />}
                </div>
              )}
              {filteredNodes.map((node) => (
                <TreeItem
                  key={node.id}
                  node={node}
                  selectedId={value}
                  onSelect={(id) => {
                    onValueChange(id);
                    setOpen(false);
                  }}
                />
              ))}
              {searchable && search && filteredNodes.length === 0 && (
                <div className="px-2 py-3 text-center text-sm text-muted-foreground">--</div>
              )}
            </div>
          </div>,
          document.body
        )}
    </div>
  );
}

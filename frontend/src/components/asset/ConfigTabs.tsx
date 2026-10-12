import { useEffect, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { cn } from "@opskat/ui";
import { STRIP_FADE, revealScrollLeft, stripEdges } from "@/components/asset/configTabStrip";

export interface ConfigGroup {
  /** 稳定标识,用于激活态匹配与 data-testid。 */
  key: string;
  /** i18n key。 */
  label: string;
  /** 数量徽标(如 Connect 集群数);<=0 或 undefined 不显示。 */
  badge?: number;
  render: () => ReactNode;
}

interface ConfigTabsProps {
  groups: ConfigGroup[];
  /** 受控的激活分组 key;不传则由组件自己记住。 */
  active?: string;
  onActiveChange?: (key: string) => void;
}

/** 把一个标签完整滚进标签条的可视区(避开两侧渐隐)。 */
function revealTab(strip: HTMLElement, tab: HTMLElement) {
  strip.scrollLeft = revealScrollLeft(strip, { left: tab.offsetLeft, width: tab.offsetWidth });
}

/**
 * 资产表单类型配置的标签容器:多分组出"下划线坐于发丝线上"的标签,单分组退化为无标签单面板。
 * 标签放不下(标签多、或文案较长的语言)时标签条横向滚动:滚出去的一侧渐隐,激活的标签
 * 自动滚到露全,鼠标滚轮在标签条上横向滚动。
 */
export function ConfigTabs({ groups, active: controlledActive, onActiveChange }: ConfigTabsProps) {
  const { t } = useTranslation();
  const [ownActive, setOwnActive] = useState(groups[0]?.key ?? "");
  const active = controlledActive ?? ownActive;
  const stripRef = useRef<HTMLDivElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const [edges, setEdges] = useState({ left: false, right: false });
  const activeKey = (groups.find((g) => g.key === active) ?? groups[0])?.key;
  const hasTabs = groups.length > 1;

  // 标签条自身变宽窄、或标签文案变长短(切换语言)都会改变"哪一侧还有内容"。
  useEffect(() => {
    const strip = stripRef.current;
    const list = listRef.current;
    if (!strip || !list) return;
    const measure = () => {
      const next = stripEdges(strip);
      setEdges((prev) => (prev.left === next.left && prev.right === next.right ? prev : next));
    };
    // 滚轮默认只纵向滚动:标签条放不下时把它转成横向,并且不再带动表单滚动。
    const onWheel = (e: WheelEvent) => {
      if (strip.scrollWidth <= strip.clientWidth || Math.abs(e.deltaY) <= Math.abs(e.deltaX)) return;
      e.preventDefault();
      strip.scrollLeft += e.deltaY;
    };
    const observer = new ResizeObserver(measure);
    observer.observe(strip);
    observer.observe(list);
    strip.addEventListener("scroll", measure, { passive: true });
    strip.addEventListener("wheel", onWheel, { passive: false });
    return () => {
      observer.disconnect();
      strip.removeEventListener("scroll", measure);
      strip.removeEventListener("wheel", onWheel);
    };
  }, [hasTabs]);

  // 激活的标签换了(点击,或保存被拒时由外部切回某个标签):把它滚到露全。
  useEffect(() => {
    const strip = stripRef.current;
    const tab = strip?.querySelector<HTMLElement>('[role="tab"][aria-selected="true"]');
    if (strip && tab) revealTab(strip, tab);
  }, [activeKey, hasTabs]);

  // 单分组:无标签,直接出内容。
  if (!hasTabs) {
    return <>{groups[0]?.render()}</>;
  }

  const activeGroup = groups.find((g) => g.key === active) ?? groups[0];

  return (
    <div className="w-full">
      <div className="relative border-b border-border">
        {edges.left && <StripArrow side="left" onClick={() => pageStrip(stripRef.current, -1)} />}
        {edges.right && <StripArrow side="right" onClick={() => pageStrip(stripRef.current, 1)} />}
        <div
          ref={stripRef}
          data-testid="config-tab-strip"
          className="relative -mb-px overflow-x-auto overflow-y-hidden scroll-smooth [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
          style={{ maskImage: edges.left || edges.right ? fadeMask(edges) : undefined }}
        >
          <div ref={listRef} role="tablist" className="flex w-max items-end gap-6">
            {groups.map((g) => {
              const isActive = g.key === activeGroup.key;
              return (
                <button
                  key={g.key}
                  type="button"
                  role="tab"
                  aria-selected={isActive}
                  data-testid={`config-tab-${g.key}`}
                  onClick={() => {
                    setOwnActive(g.key);
                    onActiveChange?.(g.key);
                  }}
                  className={cn(
                    "relative flex items-center gap-1.5 pb-[10px] text-[13.5px] whitespace-nowrap transition-colors outline-none focus-visible:text-foreground",
                    isActive ? "font-semibold text-primary" : "font-medium text-muted-foreground hover:text-foreground"
                  )}
                >
                  {t(g.label)}
                  {g.badge !== undefined && g.badge > 0 && (
                    <span className="rounded-full bg-primary/10 px-1.5 text-[10px] font-semibold text-primary">
                      {g.badge}
                    </span>
                  )}
                  <span
                    className={cn(
                      "absolute inset-x-0 bottom-0 h-0.5 rounded-full",
                      isActive ? "bg-primary" : "bg-transparent"
                    )}
                  />
                </button>
              );
            })}
          </div>
        </div>
      </div>
      <div className="mt-5">{activeGroup.render()}</div>
    </div>
  );
}

/** 滚出去的一侧渐隐成透明:不依赖表单底色,亮暗主题都成立。最外侧 ARROW_ZONE 完全透明,留给翻页箭头。 */
function fadeMask(edges: { left: boolean; right: boolean }): string {
  const left = edges.left ? `transparent ${ARROW_ZONE}px, black ${STRIP_FADE}px` : "black 0";
  const right = edges.right
    ? `black calc(100% - ${STRIP_FADE}px), transparent calc(100% - ${ARROW_ZONE}px)`
    : "black 100%";
  return `linear-gradient(to right, ${left}, ${right})`;
}

const ARROW_ZONE = 18;

/** 往一侧翻大半屏标签。 */
function pageStrip(strip: HTMLElement | null, direction: 1 | -1) {
  if (strip) strip.scrollLeft += direction * strip.clientWidth * 0.6;
}

/** 标签条一侧还有标签时出现的翻页箭头。键盘用户直接 Tab 到标签即可(浏览器会把它滚出来),所以箭头不进焦点序。 */
function StripArrow({ side, onClick }: { side: "left" | "right"; onClick: () => void }) {
  const Icon = side === "left" ? ChevronLeft : ChevronRight;
  return (
    <button
      type="button"
      tabIndex={-1}
      aria-hidden
      data-testid={`config-tab-strip-${side}`}
      onClick={onClick}
      className={cn(
        "absolute top-0 z-10 flex h-5 w-[18px] items-center text-muted-foreground transition-colors hover:text-foreground",
        side === "left" ? "left-0 justify-start" : "right-0 justify-end"
      )}
    >
      <Icon className="h-4 w-4" />
    </button>
  );
}

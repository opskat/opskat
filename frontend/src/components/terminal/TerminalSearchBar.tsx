import { useState, useRef, useEffect, useCallback } from "react";
import { useTranslation } from "react-i18next";
import { X, ChevronUp, ChevronDown, CaseSensitive, WholeWord, Regex } from "lucide-react";
import { cn, Input, Button } from "@opskat/ui";
import type { SearchAddon } from "@xterm/addon-search";

const SEARCH_DECORATIONS = {
  matchBackground: "#FFD33D44",
  matchBorder: "#FFD33D",
  matchOverviewRuler: "#FFD33D",
  activeMatchBackground: "#FF6A0088",
  activeMatchBorder: "#FF6A00",
  activeMatchColorOverviewRuler: "#FF6A00",
};

interface TerminalSearchBarProps {
  visible: boolean;
  onClose: () => void;
  searchAddon: SearchAddon | null;
  initialQuery?: string | null;
  initialQueryToken?: number;
}

export function TerminalSearchBar({
  visible,
  onClose,
  searchAddon,
  initialQuery,
  initialQueryToken,
}: TerminalSearchBarProps) {
  const { t } = useTranslation();
  const inputRef = useRef<HTMLInputElement>(null);
  const [query, setQuery] = useState("");
  const [caseSensitive, setCaseSensitive] = useState(false);
  const [wholeWord, setWholeWord] = useState(false);
  const [regex, setRegex] = useState(false);

  useEffect(() => {
    if (visible) {
      requestAnimationFrame(() => inputRef.current?.focus());
    } else {
      searchAddon?.clearDecorations();
    }
  }, [visible, searchAddon]);

  // 新搜索请求时回填输入框:渲染期对比上次值(键覆盖下方 effect 的全部依赖),
  // 替代 effect 里的同步 setState;SearchAddon 副作用仍留在下面的 effect。
  const [prevSeed, setPrevSeed] = useState<{
    visible: boolean;
    initialQuery: string | null | undefined;
    initialQueryToken: number | undefined;
    searchAddon: SearchAddon | null;
  } | null>(null);
  if (
    prevSeed === null ||
    prevSeed.visible !== visible ||
    prevSeed.initialQuery !== initialQuery ||
    prevSeed.initialQueryToken !== initialQueryToken ||
    prevSeed.searchAddon !== searchAddon
  ) {
    setPrevSeed({ visible, initialQuery, initialQueryToken, searchAddon });
    if (visible && initialQuery != null) {
      setQuery(initialQuery);
    }
  }

  useEffect(() => {
    if (!visible || initialQuery == null) return;
    if (!searchAddon) return;
    if (!initialQuery) {
      searchAddon.clearDecorations();
      return;
    }
    searchAddon.findNext(initialQuery, { caseSensitive, wholeWord, regex, decorations: SEARCH_DECORATIONS });
    // Only a new search request should seed the input. Option changes are handled
    // by the existing option effect below, without resetting user-edited text.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [visible, initialQuery, initialQueryToken, searchAddon]);

  const doSearch = useCallback(
    (direction: "next" | "previous", term?: string) => {
      if (!searchAddon) return;
      const searchTerm = term ?? query;
      if (!searchTerm) {
        searchAddon.clearDecorations();
        return;
      }
      const opts = { caseSensitive, wholeWord, regex, decorations: SEARCH_DECORATIONS };
      if (direction === "next") {
        searchAddon.findNext(searchTerm, opts);
      } else {
        searchAddon.findPrevious(searchTerm, opts);
      }
    },
    [searchAddon, query, caseSensitive, wholeWord, regex]
  );

  const handleQueryChange = useCallback(
    (value: string) => {
      setQuery(value);
      if (!searchAddon) return;
      if (!value) {
        searchAddon.clearDecorations();
        return;
      }
      searchAddon.findNext(value, { caseSensitive, wholeWord, regex, decorations: SEARCH_DECORATIONS });
    },
    [searchAddon, caseSensitive, wholeWord, regex]
  );

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === "Enter") {
        e.preventDefault();
        doSearch(e.shiftKey ? "previous" : "next");
      } else if (e.key === "Escape") {
        e.preventDefault();
        onClose();
      }
    },
    [doSearch, onClose]
  );

  // 切换搜索选项时重新搜索。挂载时跳过:原实现靠"query 在挂载 commit 时还是空串"来跳过
  // 首次执行,现在 query 在渲染期回填,须显式对比上次选项,仅在选项真正变化后重搜。
  const prevOptionsRef = useRef<{ caseSensitive: boolean; wholeWord: boolean; regex: boolean } | null>(null);
  useEffect(() => {
    const prev = prevOptionsRef.current;
    prevOptionsRef.current = { caseSensitive, wholeWord, regex };
    if (prev === null) return;
    if (prev.caseSensitive === caseSensitive && prev.wholeWord === wholeWord && prev.regex === regex) return;
    if (visible && query) {
      doSearch("next");
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [caseSensitive, wholeWord, regex]);

  if (!visible) return null;

  return (
    <div className="flex items-center gap-1 px-2 py-1.5 border-b bg-background/95 backdrop-blur-sm">
      <Input
        ref={inputRef}
        value={query}
        onChange={(e) => handleQueryChange(e.target.value)}
        onKeyDown={handleKeyDown}
        placeholder={t("ssh.search.placeholder")}
        className="h-7 flex-1 text-sm"
      />
      <Button
        variant="ghost"
        size="icon"
        className={cn("h-7 w-7", caseSensitive && "bg-accent text-accent-foreground")}
        onClick={() => setCaseSensitive(!caseSensitive)}
        title={t("ssh.search.caseSensitive")}
      >
        <CaseSensitive className="h-3.5 w-3.5" />
      </Button>
      <Button
        variant="ghost"
        size="icon"
        className={cn("h-7 w-7", wholeWord && "bg-accent text-accent-foreground")}
        onClick={() => setWholeWord(!wholeWord)}
        title={t("ssh.search.wholeWord")}
      >
        <WholeWord className="h-3.5 w-3.5" />
      </Button>
      <Button
        variant="ghost"
        size="icon"
        className={cn("h-7 w-7", regex && "bg-accent text-accent-foreground")}
        onClick={() => setRegex(!regex)}
        title={t("ssh.search.regex")}
      >
        <Regex className="h-3.5 w-3.5" />
      </Button>
      <div className={cn("flex items-center border-l ml-0.5 pl-1")}>
        <Button
          variant="ghost"
          size="icon"
          className="h-7 w-7"
          onClick={() => doSearch("previous")}
          title={t("ssh.search.previous")}
        >
          <ChevronUp className="h-3.5 w-3.5" />
        </Button>
        <Button
          variant="ghost"
          size="icon"
          className="h-7 w-7"
          onClick={() => doSearch("next")}
          title={t("ssh.search.next")}
        >
          <ChevronDown className="h-3.5 w-3.5" />
        </Button>
      </div>
      <Button variant="ghost" size="icon" className="h-7 w-7" onClick={onClose} title={t("ssh.search.close")}>
        <X className="h-3.5 w-3.5" />
      </Button>
    </div>
  );
}

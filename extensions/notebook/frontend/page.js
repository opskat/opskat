// extensions/notebook/frontend/page.js
//
// The notebook extension's own page: the "asset.connect" page a double-click on a
// notebook asset opens (see manifest registration in main.go's init()). It is a
// plain ES module with no build step — window.__OPSKAT_EXT__ is what the host
// injects before loading it (frontend/src/extension/inject.ts), and every page an
// extension ships is loaded the same way (frontend/src/extension/loader.ts).
//
// Its purpose beyond "show the notes" is to exercise the seam task 8 adds: every
// call below goes through window.__OPSKAT_EXT__.api.callTool, which the host now
// runs through the exact same policy check / in-app approval / grant / audit gate
// opsctl's delegated exec uses (internal/app/opsctl's handleExtToolExec) instead of
// dialing the plugin directly. note_put asks for confirmation the first time (the
// "write" policy group is not granted by default); note_delete is refused outright
// (the "no-delete" group denies it by default) — so this one page's buttons are
// what e2e drives to cover the approval dialog and the deny-as-error path.

export function NotebookPage({ assetId }) {
  const { React, api } = window.__OPSKAT_EXT__;
  const { useState, useCallback, useEffect, createElement: h } = React;

  const [notes, setNotes] = useState([]);
  const [key, setKey] = useState("");
  const [content, setContent] = useState("");
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState(null); // { kind: "success" | "error", message }

  const refresh = useCallback(async () => {
    try {
      const result = await api.callTool("notebook", "note_list", {}, assetId);
      setNotes((result && result.notes) || []);
    } catch (err) {
      setStatus({ kind: "error", message: String(err) });
    }
  }, [assetId]);

  useEffect(() => {
    refresh();
    // Deliberately not polling: this page exists to exercise one call at a time
    // under operator control, not to demonstrate a live-updating view.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [assetId]);

  const runTool = useCallback(
    async (tool, args, successMessage) => {
      setBusy(true);
      setStatus(null);
      try {
        await api.callTool("notebook", tool, args, assetId);
        setStatus({ kind: "success", message: successMessage });
        await refresh();
      } catch (err) {
        setStatus({ kind: "error", message: String(err) });
      } finally {
        setBusy(false);
      }
    },
    [assetId, refresh]
  );

  const put = useCallback(() => {
    const trimmedKey = key.trim();
    if (!trimmedKey) return;
    runTool("note_put", { key: trimmedKey, content }, `saved ${trimmedKey}`);
  }, [key, content, runTool]);

  const del = useCallback((k) => runTool("note_delete", { key: k }, `deleted ${k}`), [runTool]);

  return h(
    "div",
    { "data-testid": "notebook-page", style: { padding: 16, display: "flex", flexDirection: "column", gap: 12 } },
    h("h2", null, "Notes"),
    h(
      "div",
      { "data-testid": "notebook-status", "data-kind": status ? status.kind : "", style: { minHeight: 20 } },
      status ? status.message : ""
    ),
    h(
      "div",
      { style: { display: "flex", gap: 8 } },
      h("input", {
        "data-testid": "notebook-key-input",
        placeholder: "key",
        value: key,
        onChange: (e) => setKey(e.target.value),
      }),
      h("input", {
        "data-testid": "notebook-content-input",
        placeholder: "content",
        value: content,
        onChange: (e) => setContent(e.target.value),
      }),
      h(
        "button",
        { "data-testid": "notebook-put-button", disabled: busy || !key.trim(), onClick: put },
        "Save"
      )
    ),
    h(
      "ul",
      { "data-testid": "notebook-list" },
      notes.map((note) =>
        h(
          "li",
          { key: note.key, "data-testid": `notebook-note-${note.key}` },
          note.key,
          h(
            "button",
            { "data-testid": `notebook-delete-${note.key}`, disabled: busy, onClick: () => del(note.key) },
            "Delete"
          )
        )
      )
    )
  );
}

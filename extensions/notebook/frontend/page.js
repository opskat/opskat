// extensions/notebook/frontend/page.js
//
// The notebook extension's own page: the "asset.connect" page a double-click on a
// notebook asset opens (see manifest registration in main.go's init()). It is a
// plain ES module with no build step — window.__OPSKAT_EXT__ is what the host
// injects before loading it (frontend/src/extension/inject.ts), and every page an
// extension ships is loaded the same way (frontend/src/extension/loader.ts).
//
// Every call below goes through window.__OPSKAT_EXT__.api.callTool, which the host
// runs directly against the page's asset: the page is the user's own action, so no
// policy check, approval or audit applies (AI exec and opsctl on the same asset stay
// gated). A tool error, note_delete's included, reaches runTool as a rejected promise.
//
// It also renders the note list a second time through window.__OPSKAT_EXT__.hostUI
// (task 11's @opskat/host-ui — this is why manifest.json declares hostABI 2.1
// instead of 2.0): note_list already returns key/size/updatedAt per note, which is
// exactly a result-table shape, so this is the "result table for the note list"
// case the spec calls out rather than a contrived one.

export function NotebookPage({ assetId }) {
  const { React, api, hostUI } = window.__OPSKAT_EXT__;
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
    ),
    h(
      "div",
      { "data-testid": "notebook-table", style: { height: 180 } },
      h(hostUI.QueryResultTable, {
        columns: ["key", "size", "updatedAt"],
        rows: notes,
      })
    )
  );
}

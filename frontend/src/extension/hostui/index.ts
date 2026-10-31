// The @opskat/host-ui contract (window.__OPSKAT_EXT__.hostUI). Each component is a
// thin wrapper whose props type is owned here, so the host's internal components can
// change without breaking extension pages.
import { CodeEditor, type HostCodeEditorProps } from "./CodeEditor";
import { DataTable, type HostDataTableProps } from "./DataTable";
import { JsonTreeView, type HostJsonTreeViewProps } from "./JsonTreeView";

export { CodeEditor, DataTable, JsonTreeView };
export type { HostCodeEditorProps, HostDataTableProps, HostJsonTreeViewProps };
export type { HostMonaco, HostMonacoEditor } from "./CodeEditor";
export type { HostCopyFormat, HostRowDensity, HostSortDir } from "./DataTable";

export interface HostUI {
  version: string;
  CodeEditor: typeof CodeEditor;
  JsonTreeView: typeof JsonTreeView;
  DataTable: typeof DataTable;
}

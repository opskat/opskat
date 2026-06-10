import { OnFileDrop, OnFileDropOff } from "../../../wailsjs/runtime/runtime";

type RectProvider = () => DOMRect | null | undefined;

interface TerminalDropTarget {
  getRect: RectProvider;
  uploadFiles: (paths: string[]) => void;
}

interface FileManagerDropTarget {
  getRect: RectProvider;
  getRemoteDir: () => string;
  startUploadFile: (localPath: string, remotePath: string) => void;
}

const terminalTargets = new Map<symbol, TerminalDropTarget>();
const fileManagerTargets = new Map<symbol, FileManagerDropTarget>();

let listening = false;

function contains(rect: DOMRect | null | undefined, x: number, y: number): boolean {
  return !!rect && x >= rect.left && x <= rect.right && y >= rect.top && y <= rect.bottom;
}

function targetCount(): number {
  return terminalTargets.size + fileManagerTargets.size;
}

function firstHit<T extends { getRect: RectProvider }>(targets: Iterable<T>, x: number, y: number): T | undefined {
  return Array.from(targets)
    .reverse()
    .find((target) => contains(target.getRect(), x, y));
}

function handleFileDrop(x: number, y: number, paths: string[]) {
  const fileManagerTarget = firstHit(fileManagerTargets.values(), x, y);
  if (fileManagerTarget) {
    const remoteDir = fileManagerTarget.getRemoteDir();
    for (const path of paths) {
      fileManagerTarget.startUploadFile(path, remoteDir);
    }
    return;
  }

  const terminalTarget = firstHit(terminalTargets.values(), x, y);
  terminalTarget?.uploadFiles(paths);
}

function syncWailsFileDropListener() {
  if (!listening && targetCount() > 0) {
    OnFileDrop(handleFileDrop, true);
    listening = true;
    return;
  }
  if (listening && targetCount() === 0) {
    OnFileDropOff();
    listening = false;
  }
}

export function registerTerminalFileDropTarget(target: TerminalDropTarget): () => void {
  const id = Symbol("terminal-file-drop-target");
  terminalTargets.set(id, target);
  syncWailsFileDropListener();
  return () => {
    terminalTargets.delete(id);
    syncWailsFileDropListener();
  };
}

export function registerFileManagerDropTarget(target: FileManagerDropTarget): () => void {
  const id = Symbol("file-manager-drop-target");
  fileManagerTargets.set(id, target);
  syncWailsFileDropListener();
  return () => {
    fileManagerTargets.delete(id);
    syncWailsFileDropListener();
  };
}

export function resetTerminalFileDropCoordinatorForTest() {
  terminalTargets.clear();
  fileManagerTargets.clear();
  if (listening) {
    OnFileDropOff();
    listening = false;
  }
}

import {
  Database,
  FileArchive,
  FileAudio,
  FileCode,
  FileCog,
  FileImage,
  FileJson,
  FileKey,
  FileSpreadsheet,
  FileText,
  FileType,
  FileVideo,
  Presentation,
  File as FileIcon,
} from "lucide-react";

export type LucideIcon = typeof FileIcon;

type ObjectKind =
  | "image"
  | "video"
  | "audio"
  | "pdf"
  | "spreadsheet"
  | "presentation"
  | "archive"
  | "json"
  | "config"
  | "code"
  | "database"
  | "font"
  | "credential"
  | "text"
  | "file";

const IMAGE_EXT = new Set(["png", "jpg", "jpeg", "gif", "webp", "svg", "bmp", "avif", "ico", "tif", "tiff", "heic"]);
const VIDEO_EXT = new Set(["mp4", "mov", "webm", "mkv", "avi", "m4v", "flv", "wmv", "mpeg", "mpg"]);
const AUDIO_EXT = new Set(["mp3", "wav", "flac", "ogg", "m4a", "aac", "wma", "opus", "aiff"]);
const ARCHIVE_EXT = new Set(["zip", "gz", "gzip", "tar", "tgz", "bz2", "xz", "rar", "7z", "zst", "jar", "war"]);
const SPREADSHEET_EXT = new Set(["csv", "tsv", "xls", "xlsx", "xlsm", "xlsb", "ods", "numbers"]);
const PRESENTATION_EXT = new Set(["ppt", "pptx", "pps", "ppsx", "odp", "key"]);
const DATABASE_EXT = new Set(["db", "sqlite", "sqlite3", "mdb", "accdb", "dump", "rdb"]);
const FONT_EXT = new Set(["ttf", "otf", "woff", "woff2", "eot"]);
const CREDENTIAL_EXT = new Set(["pem", "crt", "cer", "cert", "key", "p12", "pfx", "pub", "asc", "gpg"]);
const CONFIG_EXT = new Set(["yaml", "yml", "toml", "ini", "conf", "config", "properties", "env", "lock"]);
const CODE_EXT = new Set([
  "js",
  "jsx",
  "mjs",
  "cjs",
  "ts",
  "tsx",
  "mts",
  "cts",
  "go",
  "py",
  "pyw",
  "java",
  "kt",
  "kts",
  "rs",
  "rb",
  "php",
  "swift",
  "dart",
  "scala",
  "lua",
  "pl",
  "r",
  "cs",
  "fs",
  "fsx",
  "c",
  "h",
  "cc",
  "cpp",
  "cxx",
  "hpp",
  "sh",
  "bash",
  "zsh",
  "fish",
  "ps1",
  "bat",
  "cmd",
  "sql",
  "graphql",
  "gql",
  "html",
  "htm",
  "css",
  "scss",
  "sass",
  "less",
  "vue",
  "svelte",
  "astro",
  "wasm",
  "proto",
  "sol",
]);
const TEXT_EXT = new Set(["txt", "md", "mdx", "log", "xml", "rtf", "tex", "adoc"]);

function ext(key: string): string {
  const fileName = key.slice(key.lastIndexOf("/") + 1);
  const i = fileName.lastIndexOf(".");
  return i >= 0 ? fileName.slice(i + 1).toLowerCase() : "";
}

function isConfigName(key: string): boolean {
  const fileName = key.slice(key.lastIndexOf("/") + 1).toLowerCase();
  return (
    fileName === "dockerfile" ||
    fileName === "makefile" ||
    fileName === "jenkinsfile" ||
    fileName === "vagrantfile" ||
    fileName.startsWith(".env") ||
    [".gitignore", ".gitattributes", ".editorconfig", ".npmrc", ".yarnrc", ".dockerignore"].includes(fileName)
  );
}

function objectKind(contentType: string, key: string): ObjectKind {
  const mime = contentType.toLowerCase();
  const extension = ext(key);

  if (mime.startsWith("image/") || IMAGE_EXT.has(extension)) return "image";
  if (mime.startsWith("video/") || VIDEO_EXT.has(extension)) return "video";
  if (mime.startsWith("audio/") || AUDIO_EXT.has(extension)) return "audio";
  if (mime === "application/pdf" || extension === "pdf") return "pdf";
  if (mime.includes("spreadsheet") || mime.includes("excel") || mime === "text/csv" || SPREADSHEET_EXT.has(extension))
    return "spreadsheet";
  if (mime.includes("presentation") || mime.includes("powerpoint") || PRESENTATION_EXT.has(extension))
    return "presentation";
  if (mime.includes("zip") || mime.includes("compressed") || mime.includes("archive") || ARCHIVE_EXT.has(extension))
    return "archive";
  if (mime.includes("json") || extension === "json" || extension === "jsonl" || extension === "ndjson") return "json";
  if (isConfigName(key) || CONFIG_EXT.has(extension)) return "config";
  if (mime.includes("sqlite") || mime.includes("database") || DATABASE_EXT.has(extension)) return "database";
  if (
    mime.includes("javascript") ||
    mime.includes("typescript") ||
    mime.includes("x-python") ||
    mime.includes("x-httpd-php") ||
    mime.includes("x-shellscript") ||
    mime.includes("x-sh") ||
    mime.includes("sql") ||
    mime.includes("wasm") ||
    CODE_EXT.has(extension)
  )
    return "code";
  if (mime.startsWith("font/") || mime.includes("font-") || FONT_EXT.has(extension)) return "font";
  if (mime.includes("pem") || mime.includes("certificate") || mime.includes("pkcs") || CREDENTIAL_EXT.has(extension))
    return "credential";
  if (mime.startsWith("text/") || TEXT_EXT.has(extension)) return "text";
  return "file";
}

/** image/* by content-type, or an image extension when object metadata is blank/generic. */
export function isImage(contentType: string, key = ""): boolean {
  return objectKind(contentType, key) === "image";
}

const KIND_ICONS: Record<ObjectKind, LucideIcon> = {
  image: FileImage,
  video: FileVideo,
  audio: FileAudio,
  pdf: FileText,
  spreadsheet: FileSpreadsheet,
  presentation: Presentation,
  archive: FileArchive,
  json: FileJson,
  config: FileCog,
  code: FileCode,
  database: Database,
  font: FileType,
  credential: FileKey,
  text: FileText,
  file: FileIcon,
};

/** Content-type first with a rich extension fallback for commonly generic OSS metadata. */
export function typeIcon(contentType: string, key: string): LucideIcon {
  return KIND_ICONS[objectKind(contentType, key)];
}

const KIND_COLORS: Record<ObjectKind, string> = {
  image: "text-chart-2",
  video: "text-chart-5",
  audio: "text-chart-3",
  pdf: "text-destructive",
  spreadsheet: "text-success",
  presentation: "text-warning",
  archive: "text-chart-4",
  json: "text-syntax-boolean",
  config: "text-muted-foreground",
  code: "text-info",
  database: "text-chart-1",
  font: "text-chart-5",
  credential: "text-warning",
  text: "text-syntax-string",
  file: "text-muted-foreground",
};

/** File-family color using the existing semantic, syntax, and categorical design tokens. */
export function typeIconColor(contentType: string, key: string): string {
  return KIND_COLORS[objectKind(contentType, key)];
}

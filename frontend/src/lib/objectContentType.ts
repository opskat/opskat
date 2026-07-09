import { FileImage, FileVideo, FileAudio, FileJson, FileText, FileArchive, File as FileIcon } from "lucide-react";

export type LucideIcon = typeof FileIcon;

const IMAGE_EXT = ["png", "jpg", "jpeg", "gif", "webp", "svg", "bmp", "avif"];

function ext(key: string): string {
  const i = key.lastIndexOf(".");
  return i >= 0 ? key.slice(i + 1).toLowerCase() : "";
}

/** image/* by content-type, or an image extension when content-type is blank/generic. */
export function isImage(contentType: string, key = ""): boolean {
  if (contentType.startsWith("image/")) return true;
  return IMAGE_EXT.includes(ext(key));
}

/** Family → lucide icon; content-type first, extension fallback, generic File default. */
export function typeIcon(contentType: string, key: string): LucideIcon {
  if (isImage(contentType, key)) return FileImage;
  if (contentType.startsWith("video/") || ["mp4", "mov", "webm", "mkv", "avi"].includes(ext(key))) return FileVideo;
  if (contentType.startsWith("audio/") || ["mp3", "wav", "flac", "ogg", "m4a"].includes(ext(key))) return FileAudio;
  if (contentType.includes("json") || ext(key) === "json") return FileJson;
  if (["zip", "gz", "tar", "rar", "7z"].includes(ext(key))) return FileArchive;
  if (contentType.startsWith("text/") || ["txt", "md", "log", "csv", "yaml", "yml", "xml"].includes(ext(key)))
    return FileText;
  return FileIcon;
}

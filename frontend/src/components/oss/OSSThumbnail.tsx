import { createElement, useEffect, useRef, useState } from "react";
import { isImage, typeIcon } from "@/lib/objectContentType";
import { useLazyThumbnail } from "./useLazyThumbnail";

export interface OSSThumbnailProps {
  objectKey: string;
  contentType: string;
  url?: string;
  onEnsure: () => void;
  className?: string;
}

export function OSSThumbnail({ objectKey, contentType, url, onEnsure, className }: OSSThumbnailProps) {
  const ref = useRef<HTMLDivElement>(null);
  const image = isImage(contentType, objectKey);
  const [errored, setErrored] = useState(false);
  useEffect(() => setErrored(false), [url]);
  useLazyThumbnail(ref, image && !errored, onEnsure);

  const showImg = image && !errored && !!url;

  return (
    <div ref={ref} className={className} data-testid="oss-thumb">
      {showImg ? (
        <img
          src={url}
          alt=""
          className="size-full object-cover"
          data-testid="oss-thumb-img"
          onError={() => setErrored(true)}
        />
      ) : (
        <div className="flex size-full items-center justify-center text-muted-foreground" data-testid="oss-thumb-icon">
          {createElement(typeIcon(contentType, objectKey), { className: "size-6" })}
        </div>
      )}
    </div>
  );
}

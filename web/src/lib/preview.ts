// Decides how a doc can be previewed in the browser and fetches its bytes.
import { api, type DocItem } from "../api";

export type PreviewKind = "pdf" | "image" | "docx" | "sheet" | "text" | "unsupported";

const KIND_BY_FORMAT: Record<string, PreviewKind> = {
  pdf: "pdf",
  png: "image",
  jpg: "image",
  jpeg: "image",
  gif: "image",
  svg: "image",
  webp: "image",
  bmp: "image",
  docx: "docx",
  doc: "docx",
  xlsx: "sheet",
  xls: "sheet",
  csv: "sheet",
  txt: "text",
  md: "text",
  json: "text",
  js: "text",
  ts: "text",
  log: "text",
  // pptx / ppt and anything else fall through to "unsupported".
};

export function previewKind(doc: Pick<DocItem, "format">): PreviewKind {
  const fmt = (doc.format || "").toLowerCase();
  return KIND_BY_FORMAT[fmt] || "unsupported";
}

// Previewable only when the file is actually archived (has bytes in storage)
// and we have a renderer for its format.
export function canPreview(doc: Pick<DocItem, "format" | "object_key">): boolean {
  return doc.object_key !== "" && previewKind(doc) !== "unsupported";
}

type FetchAs = "blob" | "arraybuffer" | "text";
type FetchResult = { blob: Blob; arraybuffer: ArrayBuffer; text: string };

// Fetches the archived bytes through the download endpoint (cookie-authed).
// The endpoint sends Content-Disposition: attachment, which does not affect
// fetch — we read the body directly and render it client-side.
export async function fetchDoc<T extends FetchAs>(
  id: string,
  as: T,
): Promise<FetchResult[T]> {
  const res = await fetch(api.downloadUrl(id), { credentials: "include" });
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
  if (as === "blob") return (await res.blob()) as FetchResult[T];
  if (as === "arraybuffer") return (await res.arrayBuffer()) as FetchResult[T];
  return (await res.text()) as FetchResult[T];
}

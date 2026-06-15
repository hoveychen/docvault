import { normalizePath } from "./tree";

// The file browser lives at /browse/<path>, where each path segment is
// percent-encoded so spaces / unicode / reserved chars survive the URL.

export function browseUrl(path: string): string {
  const norm = normalizePath(path);
  if (!norm) return "/browse";
  return "/browse/" + norm.split("/").map(encodeURIComponent).join("/");
}

/** Decode the react-router splat ("*" param) back into a clean source path. */
export function pathFromSplat(splat: string | undefined): string {
  if (!splat) return "";
  return splat
    .split("/")
    .filter(Boolean)
    .map((s) => {
      try {
        return decodeURIComponent(s);
      } catch {
        return s;
      }
    })
    .join("/");
}

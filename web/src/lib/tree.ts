import type { DocItem, FolderItem } from "../api";

// docvault stores a "source_path" on every item:
//   - a document's source_path is the path of the folder that CONTAINS it
//   - a folder's source_path is the folder's OWN full path
// Paths have no leading slash and use "/" separators (e.g. "Marketing/2026").
// This module rebuilds a navigable directory tree from those flat paths.

export function normalizePath(p: string | null | undefined): string {
  if (!p) return "";
  return p
    .split("/")
    .map((s) => s.trim())
    .filter(Boolean)
    .join("/");
}

export function parentPath(p: string): string {
  const norm = normalizePath(p);
  const i = norm.lastIndexOf("/");
  return i === -1 ? "" : norm.slice(0, i);
}

export function lastSegment(p: string): string {
  const norm = normalizePath(p);
  const i = norm.lastIndexOf("/");
  return i === -1 ? norm : norm.slice(i + 1);
}

/** Cumulative breadcrumb crumbs for a path, e.g. "a/b" -> [{name:"a",path:"a"},{name:"b",path:"a/b"}]. */
export function crumbs(p: string): { name: string; path: string }[] {
  const norm = normalizePath(p);
  if (!norm) return [];
  const parts = norm.split("/");
  const out: { name: string; path: string }[] = [];
  let acc = "";
  for (const part of parts) {
    acc = acc ? `${acc}/${part}` : part;
    out.push({ name: part, path: acc });
  }
  return out;
}

export interface TreeFolder {
  path: string; // full path
  name: string; // last segment
  folder?: FolderItem; // backing entity when the folder was enumerated by sync
}

export interface FileTree {
  /** Immediate sub-folders directly under `path`. */
  childFolders(path: string): TreeFolder[];
  /** Documents that live directly in `path`. */
  childDocs(path: string): DocItem[];
  /** All folder paths (for the sidebar tree). */
  allFolderPaths(): string[];
  /** Whether a folder path exists in the index. */
  hasFolder(path: string): boolean;
}

export function buildTree(docs: DocItem[], folders: FolderItem[]): FileTree {
  const dirSet = new Set<string>();
  const folderByPath = new Map<string, FolderItem>();

  const addWithAncestors = (raw: string) => {
    const norm = normalizePath(raw);
    if (!norm) return;
    let acc = "";
    for (const part of norm.split("/")) {
      acc = acc ? `${acc}/${part}` : part;
      dirSet.add(acc);
    }
  };

  for (const f of folders) {
    const norm = normalizePath(f.source_path);
    if (!norm) continue;
    addWithAncestors(norm);
    // Prefer the entity richest in data if the same path appears twice.
    if (!folderByPath.has(norm)) folderByPath.set(norm, f);
  }
  // Documents' parent folders may be deeper than any enumerated folder; seed them
  // so navigation can always drill down to where the files actually are.
  for (const d of docs) addWithAncestors(d.source_path);

  const docsByDir = new Map<string, DocItem[]>();
  for (const d of docs) {
    const dir = normalizePath(d.source_path);
    const arr = docsByDir.get(dir);
    if (arr) arr.push(d);
    else docsByDir.set(dir, [d]);
  }

  const childFolderCache = new Map<string, TreeFolder[]>();

  return {
    childFolders(path) {
      const p = normalizePath(path);
      const cached = childFolderCache.get(p);
      if (cached) return cached;
      const out: TreeFolder[] = [];
      for (const dir of dirSet) {
        if (parentPath(dir) === p && dir !== p) {
          out.push({ path: dir, name: lastSegment(dir), folder: folderByPath.get(dir) });
        }
      }
      out.sort((a, b) => a.name.localeCompare(b.name));
      childFolderCache.set(p, out);
      return out;
    },
    childDocs(path) {
      return docsByDir.get(normalizePath(path)) ?? [];
    },
    allFolderPaths() {
      return [...dirSet].sort();
    },
    hasFolder(path) {
      return dirSet.has(normalizePath(path));
    },
  };
}

import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  ArrowDownToLine,
  ArrowUpDown,
  ChevronRight,
  Cloud,
  Clock,
  Files,
  Folder,
  FolderOpen,
  Home,
  LayoutGrid,
  List as ListIcon,
  Paperclip,
  RefreshCw,
  Search,
  SearchX,
  Trash2,
  X,
} from "lucide-react";
import { api, type DocItem } from "../api";
import { useVault } from "../lib/vault";
import { crumbs, normalizePath } from "../lib/tree";
import { browseUrl } from "../lib/routes";
import { fileVisual } from "../lib/fileType";
import { formatRelative, formatSize } from "../lib/format";
import type { TreeFolder } from "../lib/tree";
import { Badge, Button, IconButton, Input, Skeleton, Spinner, Tooltip } from "./ui";
import { PreviewModal } from "./PreviewModal";

const docKey = (d: Pick<DocItem, "format" | "doc_type">) =>
  (d.format || d.doc_type || "").toLowerCase();

type Mode = "folder" | "recent" | "source" | "trash";
type SortKey = "name" | "size" | "date";
type SortDir = "asc" | "desc";
type View = "list" | "grid";

interface Props {
  mode: Mode;
  path?: string;
  provider?: string;
}

const VIEW_KEY = "docvault-view";

export function Browser({ mode, path = "", provider = "" }: Props) {
  const vault = useVault();
  const { tree, docs, loading } = vault;
  const navigate = useNavigate();

  const [view, setView] = useState<View>(() =>
    (localStorage.getItem(VIEW_KEY) as View) === "grid" ? "grid" : "list",
  );
  const [sort, setSort] = useState<{ key: SortKey; dir: SortDir }>({ key: "name", dir: "asc" });
  const [selDocs, setSelDocs] = useState<Set<string>>(new Set());
  const [selFolders, setSelFolders] = useState<Set<string>>(new Set());
  const [working, setWorking] = useState(false);
  const [query, setQuery] = useState("");
  const [typeFilter, setTypeFilter] = useState("");
  const [preview, setPreview] = useState<DocItem | null>(null);

  const setViewPersist = (v: View) => {
    setView(v);
    localStorage.setItem(VIEW_KEY, v);
  };

  const q = query.trim().toLowerCase();
  const searching = q !== "" || typeFilter !== "";

  // The set of docs in scope for this mode, before search/type filtering.
  const scopeDocs: DocItem[] = useMemo(() => {
    switch (mode) {
      case "folder":
        return searching ? docs : tree.childDocs(path);
      case "recent":
        return [...docs].sort((a, b) => (a.synced_at < b.synced_at ? 1 : -1)).slice(0, 200);
      case "source":
        return docs.filter((d) => d.provider === provider);
      case "trash":
        return docs.filter((d) => d.source_deleted_at);
      default:
        return [];
    }
  }, [mode, path, provider, docs, tree, searching]);

  // Type-filter options drawn from the docs in scope.
  const typeOptions = useMemo(() => {
    const set = new Set<string>();
    for (const d of scopeDocs) {
      const k = docKey(d);
      if (k) set.add(k);
    }
    return [...set].sort();
  }, [scopeDocs]);

  const rawDocs: DocItem[] = useMemo(() => {
    let arr = scopeDocs;
    if (q) arr = arr.filter((d) => d.title.toLowerCase().includes(q));
    if (typeFilter) arr = arr.filter((d) => docKey(d) === typeFilter);
    return arr;
  }, [scopeDocs, q, typeFilter]);

  // Folders: normal tree children when browsing a folder; name matches when searching.
  const folders: TreeFolder[] = useMemo(() => {
    if (mode === "folder" && !searching) return tree.childFolders(path);
    if (mode === "folder" && q && !typeFilter) {
      return vault.folders
        .filter((f) => f.title.toLowerCase().includes(q))
        .map((f) => ({ path: normalizePath(f.source_path), name: f.title, folder: f }));
    }
    return [];
  }, [mode, path, searching, q, typeFilter, tree, vault.folders]);

  const showFolderColForced = mode !== "folder" || searching;

  const sortedDocs = useMemo(() => {
    const arr = [...rawDocs];
    const dir = sort.dir === "asc" ? 1 : -1;
    arr.sort((a, b) => {
      let r = 0;
      if (sort.key === "name") r = a.title.localeCompare(b.title);
      else if (sort.key === "size") r = (a.size_bytes || 0) - (b.size_bytes || 0);
      else r = a.synced_at < b.synced_at ? -1 : a.synced_at > b.synced_at ? 1 : 0;
      return r * dir;
    });
    return arr;
  }, [rawDocs, sort]);

  const sortedFolders = useMemo(
    () => [...folders].sort((a, b) => a.name.localeCompare(b.name) * (sort.dir === "asc" ? 1 : -1)),
    [folders, sort.dir],
  );

  const toggleSort = (key: SortKey) =>
    setSort((s) => (s.key === key ? { key, dir: s.dir === "asc" ? "desc" : "asc" } : { key, dir: "asc" }));

  const toggleDoc = (id: string) =>
    setSelDocs((prev) => {
      const next = new Set(prev);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
  const toggleFolder = (id: string) =>
    setSelFolders((prev) => {
      const next = new Set(prev);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
  const clearSel = () => {
    setSelDocs(new Set());
    setSelFolders(new Set());
  };

  const deletableDocs = sortedDocs.filter((d) => d.deletable);
  const allDocsSelected = deletableDocs.length > 0 && deletableDocs.every((d) => selDocs.has(d.id));
  const someSelected = selDocs.size > 0;
  const toggleAll = () =>
    setSelDocs(allDocsSelected ? new Set() : new Set(deletableDocs.map((d) => d.id)));

  const selCount = selDocs.size + selFolders.size;

  const runDelete = async () => {
    const docIds = [...selDocs];
    const folderIds = [...selFolders];
    const ok = window.confirm(
      `确定删除所选 ${selCount} 项的云端原件吗？\n\n` +
        `它们会被移入飞书 / Lark 回收站（可恢复），归档副本仍保留在 docvault。`,
    );
    if (!ok) return;
    setWorking(true);
    try {
      const errs: string[] = [];
      if (docIds.length) {
        const r = await vault.deleteDocs(docIds);
        errs.push(...r.failed);
      }
      if (folderIds.length) {
        const r = await vault.deleteFolders(folderIds);
        errs.push(...r.failed);
      }
      clearSel();
      if (errs.length) alert(`部分未删除：${errs.join("，")}`);
    } finally {
      setWorking(false);
    }
  };

  const header = headerMeta(mode, path, provider);

  return (
    <div className="browser">
      <div className="page-header">
        {mode === "folder" ? (
          <Breadcrumb path={path} onNav={(p) => navigate(browseUrl(p))} />
        ) : (
          <span className="page-header__title" style={{ display: "flex", alignItems: "center", gap: 8 }}>
            <header.icon size={16} />
            {header.title}
          </span>
        )}
        <span className="page-header__spacer" />
        <div className="page-header__actions">
          <div className="seg">
            <button
              className={view === "list" ? "is-active" : ""}
              onClick={() => setViewPersist("list")}
              aria-label="列表视图"
            >
              <ListIcon />
            </button>
            <button
              className={view === "grid" ? "is-active" : ""}
              onClick={() => setViewPersist("grid")}
              aria-label="网格视图"
            >
              <LayoutGrid />
            </button>
          </div>
          <Button
            variant="primary"
            icon={RefreshCw}
            onClick={vault.startSync}
            disabled={vault.syncing}
          >
            {vault.syncing ? "同步中…" : "同步"}
          </Button>
        </div>
      </div>

      <div className="toolbar">
        <Input
          icon={Search}
          placeholder={searching ? "在全部归档中搜索…" : "搜索文件…"}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          style={{ width: 240 }}
        />
        {typeOptions.length > 1 && (
          <select
            className="input-wrap"
            style={{ height: 32 }}
            value={typeFilter}
            onChange={(e) => setTypeFilter(e.target.value)}
          >
            <option value="">全部类型</option>
            {typeOptions.map((t) => (
              <option key={t} value={t}>
                {t.toUpperCase()}
              </option>
            ))}
          </select>
        )}
        <span className="toolbar__spacer" />
        {searching && (
          <span className="text-tertiary" style={{ fontSize: 12.5 }}>
            {sortedFolders.length + sortedDocs.length} 个结果
            <button
              className="btn btn--ghost btn--sm"
              style={{ marginLeft: 8 }}
              onClick={() => {
                setQuery("");
                setTypeFilter("");
              }}
            >
              清除
            </button>
          </span>
        )}
      </div>

      <SyncBanner />

      <div className="page-body">
        <div className="page-pad">
          {loading ? (
            <ListSkeleton />
          ) : sortedFolders.length === 0 && sortedDocs.length === 0 ? (
            searching ? (
              <SearchEmpty />
            ) : (
              <EmptyState mode={mode} isRoot={mode === "folder" && !path} totalDocs={docs.length} onSync={vault.startSync} syncing={vault.syncing} />
            )
          ) : view === "list" ? (
            <ListView
              folders={sortedFolders}
              docs={sortedDocs}
              sort={sort}
              onSort={toggleSort}
              selDocs={selDocs}
              selFolders={selFolders}
              onToggleDoc={toggleDoc}
              onToggleFolder={toggleFolder}
              onToggleAll={toggleAll}
              allSelected={allDocsSelected}
              someSelected={someSelected}
              hasDeletable={deletableDocs.length > 0}
              onOpenFolder={(p) => navigate(browseUrl(p))}
              showFolderCol={showFolderColForced}
              onPreview={setPreview}
            />
          ) : (
            <GridView
              folders={sortedFolders}
              docs={sortedDocs}
              selDocs={selDocs}
              selFolders={selFolders}
              onToggleDoc={toggleDoc}
              onToggleFolder={toggleFolder}
              onOpenFolder={(p) => navigate(browseUrl(p))}
              onPreview={setPreview}
            />
          )}

          {selCount > 0 && (
            <div className="bulk-bar">
              <span className="bulk-bar__count">
                已选 <b>{selCount}</b> 项
              </span>
              <Button variant="danger" size="sm" icon={Trash2} onClick={runDelete} disabled={working}>
                {working ? "删除中…" : "删除云端原件"}
              </Button>
              <IconButton icon={X} size="sm" onClick={clearSel} label="取消选择" />
            </div>
          )}
        </div>
      </div>

      {preview && <PreviewModal doc={preview} onClose={() => setPreview(null)} />}
    </div>
  );
}

/* ---------------- Breadcrumb ---------------- */
function Breadcrumb({ path, onNav }: { path: string; onNav: (p: string) => void }) {
  const items = crumbs(path);
  return (
    <nav className="crumbs">
      <span
        className={"crumbs__item" + (items.length === 0 ? " crumbs__item--current" : "")}
        onClick={() => onNav("")}
      >
        <Home />
        全部文件
      </span>
      {items.map((c, i) => (
        <span key={c.path} style={{ display: "inline-flex", alignItems: "center" }}>
          <span className="crumbs__sep">
            <ChevronRight />
          </span>
          <span
            className={"crumbs__item" + (i === items.length - 1 ? " crumbs__item--current" : "")}
            onClick={() => onNav(c.path)}
          >
            {c.name}
          </span>
        </span>
      ))}
    </nav>
  );
}

/* ---------------- List view ---------------- */
interface ListProps {
  folders: TreeFolder[];
  docs: DocItem[];
  sort: { key: SortKey; dir: SortDir };
  onSort: (k: SortKey) => void;
  selDocs: Set<string>;
  selFolders: Set<string>;
  onToggleDoc: (id: string) => void;
  onToggleFolder: (id: string) => void;
  onToggleAll: () => void;
  allSelected: boolean;
  someSelected: boolean;
  hasDeletable: boolean;
  onOpenFolder: (p: string) => void;
  showFolderCol: boolean;
  onPreview: (d: DocItem) => void;
}

function ListView(p: ListProps) {
  return (
    <table className="file-table">
      <thead>
        <tr>
          <th className="col-chk">
            <input
              className="check"
              type="checkbox"
              checked={p.allSelected}
              ref={(el) => {
                if (el) el.indeterminate = !p.allSelected && p.someSelected;
              }}
              onChange={p.onToggleAll}
              disabled={!p.hasDeletable}
            />
          </th>
          <SortableTh label="名称" col="name" sort={p.sort} onSort={p.onSort} />
          <th>类型</th>
          {p.showFolderCol && <th className="col-loc">位置</th>}
          <SortableTh label="大小" col="size" sort={p.sort} onSort={p.onSort} />
          <SortableTh label="同步时间" col="date" sort={p.sort} onSort={p.onSort} />
          <th className="col-actions" />
        </tr>
      </thead>
      <tbody>
        {p.folders.map((f) => (
          <FolderRow
            key={f.path}
            folder={f}
            selected={!!f.folder && p.selFolders.has(f.folder.id)}
            onToggle={() => f.folder && p.onToggleFolder(f.folder.id)}
            onOpen={() => p.onOpenFolder(f.path)}
            showFolderCol={p.showFolderCol}
          />
        ))}
        {p.docs.map((d) => (
          <DocRow
            key={d.id}
            doc={d}
            selected={p.selDocs.has(d.id)}
            onToggle={() => p.onToggleDoc(d.id)}
            showFolderCol={p.showFolderCol}
            onPreview={p.onPreview}
          />
        ))}
      </tbody>
    </table>
  );
}

function SortableTh({
  label,
  col,
  sort,
  onSort,
}: {
  label: string;
  col: SortKey;
  sort: { key: SortKey; dir: SortDir };
  onSort: (k: SortKey) => void;
}) {
  const active = sort.key === col;
  return (
    <th className="sortable" onClick={() => onSort(col)}>
      <span className="th-sort">
        {label}
        {active && <ArrowUpDown style={{ opacity: 0.8 }} />}
      </span>
    </th>
  );
}

function FolderRow({
  folder,
  selected,
  onToggle,
  onOpen,
  showFolderCol,
}: {
  folder: TreeFolder;
  selected: boolean;
  onToggle: () => void;
  onOpen: () => void;
  showFolderCol: boolean;
}) {
  const deletable = folder.folder?.deletable;
  const deleted = folder.folder?.source_deleted_at;
  return (
    <tr className={"file-row" + (selected ? " is-selected" : "")} onClick={onOpen}>
      <td className="col-chk" onClick={(e) => e.stopPropagation()}>
        <input
          className="check"
          type="checkbox"
          checked={selected}
          disabled={!deletable}
          onChange={onToggle}
          title={folder.folder?.not_deletable_reason || (deletable ? "" : "无法删除")}
        />
      </td>
      <td>
        <div className="file-row__name">
          <Folder color="var(--accent-subtle-fg)" />
          <span>{folder.name}</span>
        </div>
      </td>
      <td>
        <Badge tone="neutral">文件夹</Badge>
      </td>
      {showFolderCol && <td className="col-loc text-tertiary">—</td>}
      <td className="num">—</td>
      <td className="num">
        {deleted ? <Badge tone="danger">原件已删</Badge> : "—"}
      </td>
      <td className="col-actions" />
    </tr>
  );
}

function DocRow({
  doc,
  selected,
  onToggle,
  showFolderCol,
  onPreview,
}: {
  doc: DocItem;
  selected: boolean;
  onToggle: () => void;
  showFolderCol: boolean;
  onPreview: (d: DocItem) => void;
}) {
  const v = fileVisual(doc);
  const deleted = !!doc.source_deleted_at;
  const archived = doc.object_key !== "";
  return (
    <tr
      className={
        "file-row" +
        (selected ? " is-selected" : "") +
        (!archived ? " is-unarchived" : "") +
        (archived ? " is-openable" : "")
      }
      onClick={archived ? () => onPreview(doc) : undefined}
    >
      <td className="col-chk" onClick={(e) => e.stopPropagation()}>
        <input
          className="check"
          type="checkbox"
          checked={selected}
          disabled={!doc.deletable}
          onChange={onToggle}
          title={doc.deletable ? "" : "非本人拥有或未归档，无法删除"}
        />
      </td>
      <td>
        <div className="file-row__name">
          <v.Icon color={v.color} />
          <span>{doc.title}</span>
          {doc.attachments && doc.attachments.length > 0 && (
            <Tooltip label={`${doc.attachments.length} 个内嵌附件`}>
              <span className="file-row__attach">
                <Paperclip size={13} />
                {doc.attachments.length}
              </span>
            </Tooltip>
          )}
        </div>
      </td>
      <td>
        <Badge tone="neutral">{v.label}</Badge>
      </td>
      {showFolderCol && (
        <td className="col-loc text-tertiary">
          <span className="cell-ellipsis" title={doc.source_path || "/"}>
            {doc.source_path || "/"}
          </span>
        </td>
      )}
      <td className="num">{archived ? formatSize(doc.size_bytes) : "—"}</td>
      <td className="num">
        {deleted ? (
          <Badge tone="danger">原件已删</Badge>
        ) : archived ? (
          formatRelative(doc.synced_at)
        ) : (
          <Tooltip label="该类型未导出副本（缺导出权限或类型不支持），暂不可下载">
            <Badge tone="warning">未归档</Badge>
          </Tooltip>
        )}
      </td>
      <td className="col-actions" onClick={(e) => e.stopPropagation()}>
        <div className="row-actions">
          {archived && (
            <Tooltip label="下载">
              <a href={api.downloadUrl(doc.id)} className="icon-btn icon-btn--sm">
                <ArrowDownToLine />
              </a>
            </Tooltip>
          )}
        </div>
      </td>
    </tr>
  );
}

/* ---------------- Grid view ---------------- */
function GridView({
  folders,
  docs,
  selDocs,
  selFolders,
  onToggleDoc,
  onToggleFolder,
  onOpenFolder,
  onPreview,
}: {
  folders: TreeFolder[];
  docs: DocItem[];
  selDocs: Set<string>;
  selFolders: Set<string>;
  onToggleDoc: (id: string) => void;
  onToggleFolder: (id: string) => void;
  onOpenFolder: (p: string) => void;
  onPreview: (d: DocItem) => void;
}) {
  return (
    <div className="file-grid">
      {folders.map((f) => {
        const selected = !!f.folder && selFolders.has(f.folder.id);
        return (
          <div
            key={f.path}
            className={"grid-card" + (selected ? " is-selected" : "")}
            onClick={() => onOpenFolder(f.path)}
          >
            {f.folder?.deletable && (
              <input
                className="check grid-card__chk"
                type="checkbox"
                checked={selected}
                onChange={() => f.folder && onToggleFolder(f.folder.id)}
                onClick={(e) => e.stopPropagation()}
              />
            )}
            <div className="grid-card__icon">
              <Folder color="var(--accent-subtle-fg)" />
            </div>
            <div className="grid-card__name">{f.name}</div>
            <div className="grid-card__meta">文件夹</div>
          </div>
        );
      })}
      {docs.map((d) => {
        const v = fileVisual(d);
        const selected = selDocs.has(d.id);
        const openable = d.object_key !== "";
        return (
          <div
            key={d.id}
            className={
              "grid-card" +
              (selected ? " is-selected" : "") +
              (d.object_key === "" ? " is-unarchived" : "") +
              (openable ? " is-openable" : "")
            }
            onClick={openable ? () => onPreview(d) : undefined}
          >
            {d.deletable && (
              <input
                className="check grid-card__chk"
                type="checkbox"
                checked={selected}
                onChange={() => onToggleDoc(d.id)}
                onClick={(e) => e.stopPropagation()}
              />
            )}
            <div className="grid-card__icon">
              <v.Icon color={v.color} />
            </div>
            <div className="grid-card__name">{d.title}</div>
            <div className="grid-card__meta">
              {d.source_deleted_at
                ? "原件已删"
                : d.object_key === ""
                  ? "未归档"
                  : `${v.label} · ${formatSize(d.size_bytes)}`}
            </div>
          </div>
        );
      })}
    </div>
  );
}

/* ---------------- Sync banner ---------------- */
function SyncBanner() {
  const { status, error } = useVault();
  if (error) {
    return <div className="sync-banner sync-banner--error">{error}</div>;
  }
  if (!status || status.status === "none" || status.status === "succeeded") return null;
  const total = status.total_items || 0;
  const done = status.done_items || 0;
  const pct = total ? Math.round((done / total) * 100) : 0;
  if (status.status === "failed") {
    return (
      <div className="sync-banner sync-banner--error">
        同步失败{status.error ? `：${status.error}` : ""}
      </div>
    );
  }
  return (
    <div className="sync-banner">
      <Spinner size={15} />
      <span style={{ whiteSpace: "nowrap" }}>
        {status.status === "queued" ? "排队中" : "同步中"} {done}/{total}
      </span>
      <div className="sync-progress">
        <div className="sync-progress__bar" style={{ width: `${pct}%` }} />
      </div>
    </div>
  );
}

/* ---------------- Empty + loading ---------------- */
function EmptyState({
  mode,
  isRoot,
  totalDocs,
  onSync,
  syncing,
}: {
  mode: Mode;
  isRoot: boolean;
  totalDocs: number;
  onSync: () => void;
  syncing: boolean;
}) {
  if (mode === "trash") {
    return (
      <div className="empty">
        <span className="empty__icon">
          <Trash2 />
        </span>
        <div className="empty__title">回收站为空</div>
        <div className="empty__desc">删除了云端原件的文档会出现在这里——归档副本仍可下载。</div>
      </div>
    );
  }
  if (isRoot && totalDocs === 0) {
    return (
      <div className="empty">
        <span className="empty__icon">
          <FolderOpen />
        </span>
        <div className="empty__title">还没有归档</div>
        <div className="empty__desc">
          点击「同步」，docvault 会扫描你授权账号能访问的云文档，导出后存入归档。
        </div>
        <Button variant="primary" icon={RefreshCw} onClick={onSync} disabled={syncing}>
          {syncing ? "同步中…" : "立即同步"}
        </Button>
      </div>
    );
  }
  return (
    <div className="empty">
      <span className="empty__icon">
        <FolderOpen />
      </span>
      <div className="empty__title">这里什么都没有</div>
      <div className="empty__desc">
        {mode === "source" ? "该来源下还没有归档文档。" : "此文件夹为空。"}
      </div>
    </div>
  );
}

function SearchEmpty() {
  return (
    <div className="empty">
      <span className="empty__icon">
        <SearchX />
      </span>
      <div className="empty__title">没有匹配的文件</div>
      <div className="empty__desc">换个关键词，或清除筛选条件再试。</div>
    </div>
  );
}

function ListSkeleton() {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 14, padding: "6px 10px" }}>
      {Array.from({ length: 8 }).map((_, i) => (
        <div key={i} style={{ display: "flex", alignItems: "center", gap: 12 }}>
          <Skeleton width={17} height={17} radius={5} />
          <Skeleton width={`${30 + ((i * 13) % 40)}%`} />
          <span style={{ flex: 1 }} />
          <Skeleton width={48} />
          <Skeleton width={70} />
        </div>
      ))}
    </div>
  );
}

function headerMeta(mode: Mode, _path: string, provider: string) {
  switch (mode) {
    case "recent":
      return { title: "最近同步", icon: Clock };
    case "source":
      return { title: provider, icon: Cloud };
    case "trash":
      return { title: "回收站", icon: Trash2 };
    default:
      return { title: "全部文件", icon: Files };
  }
}

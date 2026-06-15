import { useCallback, useEffect, useState } from "react";
import {
  api,
  type DocItem,
  type FolderItem,
  type ProviderInfo,
  type SyncStatus,
  type User,
} from "./api";

export function App() {
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);
  const [providers, setProviders] = useState<ProviderInfo[]>([]);

  useEffect(() => {
    api.providers().then((p) => setProviders(p.providers || [])).catch(() => {});
    api
      .me()
      .then(setUser)
      .catch(() => setUser(null))
      .finally(() => setLoading(false));
  }, []);

  if (loading) return <div className="center muted">加载中…</div>;
  return user ? (
    <Dashboard user={user} onLogout={() => setUser(null)} />
  ) : (
    <Login providers={providers} />
  );
}

function Login({ providers }: { providers: ProviderInfo[] }) {
  return (
    <div className="center">
      <div className="card login">
        <h1>docvault</h1>
        <p className="muted">把你的云文档一键归档到本地可下载的副本。</p>
        {providers.length === 0 && (
          <p className="error">尚未配置任何云文档来源（检查服务端 Feishu/Lark 凭据）。</p>
        )}
        {providers.map((p) => (
          <a key={p.key} className="btn primary block" href={api.loginUrl(p.key)}>
            使用 {p.label} 授权登录
          </a>
        ))}
      </div>
    </div>
  );
}

function Dashboard({ user, onLogout }: { user: User; onLogout: () => void }) {
  const [docs, setDocs] = useState<DocItem[]>([]);
  const [status, setStatus] = useState<SyncStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [deleting, setDeleting] = useState(false);
  const [folders, setFolders] = useState<FolderItem[]>([]);
  const [selectedFolders, setSelectedFolders] = useState<Set<string>>(new Set());
  const [deletingFolders, setDeletingFolders] = useState(false);

  const refresh = useCallback(async () => {
    const [d, f, s] = await Promise.all([api.documents(), api.folders(), api.syncStatus()]);
    setDocs(d.documents || []);
    setFolders(f.folders || []);
    setStatus(s);
    setSelected(new Set());
    setSelectedFolders(new Set());
  }, []);

  useEffect(() => {
    refresh().catch((e) => setErr(String(e)));
  }, [refresh]);

  // Poll while a sync is queued/running.
  useEffect(() => {
    if (status?.status !== "queued" && status?.status !== "running") return;
    const t = setInterval(() => refresh().catch(() => {}), 2000);
    return () => clearInterval(t);
  }, [status?.status, refresh]);

  const startSync = async () => {
    setBusy(true);
    setErr("");
    try {
      await api.startSync();
      await refresh();
    } catch (e) {
      setErr(String(e));
    } finally {
      setBusy(false);
    }
  };

  const logout = async () => {
    await api.logout().catch(() => {});
    onLogout();
  };

  const toggle = (id: string) =>
    setSelected((prev) => {
      const next = new Set(prev);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });

  const deletableDocs = docs.filter((d) => d.deletable);
  const allSelected = deletableDocs.length > 0 && deletableDocs.every((d) => selected.has(d.id));
  const toggleAll = () =>
    setSelected(allSelected ? new Set() : new Set(deletableDocs.map((d) => d.id)));

  const deleteSelected = async () => {
    const ids = [...selected];
    if (ids.length === 0) return;
    const ok = window.confirm(
      `确定要删除这 ${ids.length} 个文档的云端原件吗？\n\n` +
        `它们会被移入飞书/Lark 回收站（可在回收站恢复），归档副本仍保留在 docvault。`
    );
    if (!ok) return;
    setDeleting(true);
    setErr("");
    try {
      const { results } = await api.deleteSource(ids);
      const failed = results.filter((r) => r.status !== "deleted");
      if (failed.length > 0) {
        setErr(`部分未删除：${failed.map((f) => `${f.id.slice(0, 8)}(${f.error || f.status})`).join("，")}`);
      }
      await refresh();
    } catch (e) {
      setErr(String(e));
    } finally {
      setDeleting(false);
    }
  };

  const toggleFolder = (id: string) =>
    setSelectedFolders((prev) => {
      const next = new Set(prev);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });

  const deleteSelectedFolders = async () => {
    const ids = [...selectedFolders];
    if (ids.length === 0) return;
    const ok = window.confirm(
      `确定要删除这 ${ids.length} 个文件夹的云端原件吗？\n\n` +
        `整个文件夹及其内容会被移入飞书/Lark 回收站（可恢复）。仅当其内容已全部归档且归你本人拥有时才会执行。`
    );
    if (!ok) return;
    setDeletingFolders(true);
    setErr("");
    try {
      const { results } = await api.deleteFolderSource(ids);
      const failed = results.filter((r) => r.status !== "deleted");
      if (failed.length > 0) {
        setErr(`部分文件夹未删除：${failed.map((f) => `${f.id.slice(0, 8)}(${f.error || f.status})`).join("，")}`);
      }
      await refresh();
    } catch (e) {
      setErr(String(e));
    } finally {
      setDeletingFolders(false);
    }
  };

  const running = status?.status === "queued" || status?.status === "running";

  return (
    <div className="app">
      <header className="topbar">
        <div className="brand">docvault</div>
        <div className="user">
          {user.avatar_url && <img src={user.avatar_url} alt="" className="avatar" />}
          <span>{user.display_name || user.email || "我"}</span>
          <button className="btn ghost" onClick={logout}>
            退出
          </button>
        </div>
      </header>

      <main className="content">
        <section className="card">
          <div className="row spread">
            <div>
              <h2>同步</h2>
              <p className="muted">
                扫描你授权账号能访问的云文档，导出后存入归档。
              </p>
            </div>
            <button className="btn primary" onClick={startSync} disabled={busy || running}>
              {running ? "同步中…" : "立即同步"}
            </button>
          </div>
          {status && <StatusLine status={status} />}
          {err && <p className="error">{err}</p>}
        </section>

        {folders.length > 0 && (
          <section className="card">
            <div className="row spread">
              <h2>源文件夹（{folders.length}）</h2>
              <button
                className="btn danger"
                onClick={deleteSelectedFolders}
                disabled={deletingFolders || selectedFolders.size === 0}
                title="删除所选文件夹在云端的原件（整夹移入回收站）"
              >
                {deletingFolders ? "删除中…" : `删除文件夹原件（${selectedFolders.size}）`}
              </button>
            </div>
            <p className="muted small-note">
              仅当文件夹内所有文档都已归档且归你本人拥有时才可删除；否则会标注不可删原因。
            </p>
            <table className="docs">
              <thead>
                <tr>
                  <th className="chk"></th>
                  <th>文件夹</th>
                  <th>路径</th>
                  <th>状态</th>
                </tr>
              </thead>
              <tbody>
                {folders.map((f) => (
                  <tr key={f.id}>
                    <td className="chk">
                      <input
                        type="checkbox"
                        checked={selectedFolders.has(f.id)}
                        onChange={() => toggleFolder(f.id)}
                        disabled={!f.deletable}
                        title={f.deletable ? "" : f.not_deletable_reason || "无法删除"}
                      />
                    </td>
                    <td>{f.title}</td>
                    <td className="muted">{f.source_path || "/"}</td>
                    <td>
                      {f.source_deleted_at ? (
                        <span className="tag deleted">原件已删</span>
                      ) : f.deletable ? (
                        <span className="muted">可删除</span>
                      ) : (
                        <span className="muted">{f.not_deletable_reason}</span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </section>
        )}

        <section className="card">
          <div className="row spread">
            <h2>已归档文档（{docs.length}）</h2>
            <button
              className="btn danger"
              onClick={deleteSelected}
              disabled={deleting || selected.size === 0}
              title="删除所选文档在云端的原件（移入回收站），完成数据私有化"
            >
              {deleting ? "删除中…" : `删除云端原件（${selected.size}）`}
            </button>
          </div>
          <p className="muted small-note">
            勾选你拥有且已归档的文档，可删除其云端原件——副本仍保留在 docvault。仅文件所有者可删；非本人拥有的不可勾选。
          </p>
          {docs.length === 0 ? (
            <p className="muted">还没有归档。点击「立即同步」开始。</p>
          ) : (
            <table className="docs">
              <thead>
                <tr>
                  <th className="chk">
                    <input
                      type="checkbox"
                      checked={allSelected}
                      onChange={toggleAll}
                      disabled={deletableDocs.length === 0}
                    />
                  </th>
                  <th>标题</th>
                  <th>类型</th>
                  <th>路径</th>
                  <th>大小</th>
                  <th>状态</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {docs.map((d) => (
                  <tr key={d.id}>
                    <td className="chk">
                      <input
                        type="checkbox"
                        checked={selected.has(d.id)}
                        onChange={() => toggle(d.id)}
                        disabled={!d.deletable}
                        title={d.deletable ? "" : "非本人拥有或未归档，无法删除"}
                      />
                    </td>
                    <td>{d.title}</td>
                    <td>
                      <span className="tag">{d.format || d.doc_type}</span>
                    </td>
                    <td className="muted">{d.source_path || "/"}</td>
                    <td>{formatSize(d.size_bytes)}</td>
                    <td>
                      {d.source_deleted_at ? (
                        <span className="tag deleted">原件已删</span>
                      ) : (
                        <span className="muted">{formatTime(d.synced_at)}</span>
                      )}
                    </td>
                    <td>
                      <a className="btn small" href={api.downloadUrl(d.id)}>
                        下载
                      </a>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </section>
      </main>
    </div>
  );
}

function StatusLine({ status }: { status: SyncStatus }) {
  if (status.status === "none") return <p className="muted">尚未同步过。</p>;
  const { status: s, total_items = 0, done_items = 0, failed_items = 0 } = status;
  const label: Record<string, string> = {
    queued: "排队中",
    running: "同步中",
    succeeded: "已完成",
    failed: "失败",
  };
  return (
    <p className={s === "failed" ? "error" : "muted"}>
      状态：{label[s] ?? s}　已处理 {done_items}/{total_items}
      {failed_items > 0 && `　跳过/失败 ${failed_items}`}
      {status.error && `　— ${status.error}`}
    </p>
  );
}

function formatSize(bytes: number): string {
  if (!bytes) return "—";
  const units = ["B", "KB", "MB", "GB"];
  let n = bytes;
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  return `${n.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

function formatTime(iso: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}

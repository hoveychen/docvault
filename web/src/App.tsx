import { useCallback, useEffect, useState } from "react";
import { api, type DocItem, type SyncStatus, type User } from "./api";

const PROVIDER_LABELS: Record<string, string> = {
  feishu: "飞书 / Lark",
  google: "Google Workspace",
  o365: "Office 365",
};

export function App() {
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);
  const [providers, setProviders] = useState<string[]>([]);

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

function Login({ providers }: { providers: string[] }) {
  return (
    <div className="center">
      <div className="card login">
        <h1>docvault</h1>
        <p className="muted">把你的云文档一键归档到本地可下载的副本。</p>
        {providers.length === 0 && (
          <p className="error">尚未配置任何云文档来源（检查服务端 Feishu 凭据）。</p>
        )}
        {providers.map((p) => (
          <a key={p} className="btn primary block" href={api.loginUrl(p)}>
            使用 {PROVIDER_LABELS[p] ?? p} 授权登录
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

  const refresh = useCallback(async () => {
    const [d, s] = await Promise.all([api.documents(), api.syncStatus()]);
    setDocs(d.documents || []);
    setStatus(s);
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

        <section className="card">
          <h2>已归档文档（{docs.length}）</h2>
          {docs.length === 0 ? (
            <p className="muted">还没有归档。点击「立即同步」开始。</p>
          ) : (
            <table className="docs">
              <thead>
                <tr>
                  <th>标题</th>
                  <th>类型</th>
                  <th>路径</th>
                  <th>大小</th>
                  <th>同步时间</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {docs.map((d) => (
                  <tr key={d.id}>
                    <td>{d.title}</td>
                    <td>
                      <span className="tag">{d.format || d.doc_type}</span>
                    </td>
                    <td className="muted">{d.source_path || "/"}</td>
                    <td>{formatSize(d.size_bytes)}</td>
                    <td className="muted">{formatTime(d.synced_at)}</td>
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

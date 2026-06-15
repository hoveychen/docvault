import { useCallback, useEffect, useState } from "react";
import { api, type Connection, type ConnectionInput, type User } from "./api";

// AdminPanel is rendered only for admin users. It manages members (promote /
// demote / ban / unban) and provider connections (add / edit / delete).
export function AdminPanel({ meId }: { meId: string }) {
  const [open, setOpen] = useState(false);
  return (
    <section className="card admin">
      <div className="row spread">
        <h2>管理后台</h2>
        <button className="btn ghost" onClick={() => setOpen((v) => !v)}>
          {open ? "收起" : "展开"}
        </button>
      </div>
      {open && (
        <>
          <Members meId={meId} />
          <Connections />
        </>
      )}
    </section>
  );
}

function Members({ meId }: { meId: string }) {
  const [users, setUsers] = useState<User[]>([]);
  const [err, setErr] = useState("");

  const load = useCallback(() => {
    api
      .adminUsers()
      .then((r) => setUsers(r.users || []))
      .catch((e) => setErr(String(e)));
  }, []);
  useEffect(load, [load]);

  const act = async (id: string, action: "promote" | "demote" | "ban" | "unban") => {
    setErr("");
    try {
      await api.adminUserAction(id, action);
      load();
    } catch (e) {
      setErr(String(e));
    }
  };

  return (
    <div className="admin-block">
      <h3>成员（{users.length}）</h3>
      {err && <p className="error">{err}</p>}
      <table className="docs">
        <thead>
          <tr>
            <th>用户</th>
            <th>邮箱</th>
            <th>角色</th>
            <th>状态</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          {users.map((u) => (
            <tr key={u.id}>
              <td>
                {u.display_name || "—"}
                {u.id === meId && <span className="muted"> (你)</span>}
              </td>
              <td className="muted">{u.email || "—"}</td>
              <td>
                <span className={`tag ${u.role === "admin" ? "" : "muted-tag"}`}>{u.role}</span>
              </td>
              <td>{u.banned ? <span className="tag deleted">已封禁</span> : <span className="muted">正常</span>}</td>
              <td className="admin-actions">
                {u.id !== meId && (
                  <>
                    {u.role === "admin" ? (
                      <button className="btn small" onClick={() => act(u.id, "demote")}>
                        降为成员
                      </button>
                    ) : (
                      <button className="btn small" onClick={() => act(u.id, "promote")}>
                        设为管理员
                      </button>
                    )}
                    {u.banned ? (
                      <button className="btn small" onClick={() => act(u.id, "unban")}>
                        解封
                      </button>
                    ) : (
                      <button className="btn small danger" onClick={() => act(u.id, "ban")}>
                        封禁
                      </button>
                    )}
                  </>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

const EMPTY: ConnectionInput = { key: "", label: "", app_id: "", app_secret: "", domain: "feishu" };

function Connections() {
  const [conns, setConns] = useState<Connection[]>([]);
  const [editing, setEditing] = useState<string | null>(null); // connection id, or "new", or null
  const [form, setForm] = useState<ConnectionInput>(EMPTY);
  const [err, setErr] = useState("");

  const load = useCallback(() => {
    api
      .adminConnections()
      .then((r) => setConns(r.connections || []))
      .catch((e) => setErr(String(e)));
  }, []);
  useEffect(load, [load]);

  const startNew = () => {
    setForm(EMPTY);
    setEditing("new");
  };
  const startEdit = (c: Connection) => {
    setForm({ key: c.key, label: c.label, app_id: c.app_id, app_secret: "", domain: c.domain });
    setEditing(c.id);
  };

  const save = async () => {
    setErr("");
    try {
      if (editing === "new") {
        await api.adminCreateConnection(form);
      } else if (editing) {
        await api.adminUpdateConnection(editing, form);
      }
      setEditing(null);
      load();
    } catch (e) {
      setErr(String(e));
    }
  };

  const remove = async (id: string) => {
    if (!window.confirm("删除该连接？已用它登录的用户将无法再同步（已归档数据保留）。")) return;
    setErr("");
    try {
      await api.adminDeleteConnection(id);
      load();
    } catch (e) {
      setErr(String(e));
    }
  };

  return (
    <div className="admin-block">
      <div className="row spread">
        <h3>连接 / 组织（{conns.length}）</h3>
        <button className="btn small primary" onClick={startNew}>
          新增连接
        </button>
      </div>
      {err && <p className="error">{err}</p>}
      <table className="docs">
        <thead>
          <tr>
            <th>Key</th>
            <th>名称</th>
            <th>App ID</th>
            <th>域</th>
            <th>密钥</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          {conns.map((c) => (
            <tr key={c.id}>
              <td>{c.key}</td>
              <td>{c.label}</td>
              <td className="muted">{c.app_id}</td>
              <td>{c.domain}</td>
              <td>{c.has_secret ? "已设置" : <span className="error">缺失</span>}</td>
              <td className="admin-actions">
                <button className="btn small" onClick={() => startEdit(c)}>
                  编辑
                </button>
                <button className="btn small danger" onClick={() => remove(c.id)}>
                  删除
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      {editing && (
        <div className="conn-form">
          <h4>{editing === "new" ? "新增连接" : "编辑连接"}</h4>
          {editing === "new" && (
            <label>
              Key（唯一，用于 OAuth 路由 /api/auth/&lt;key&gt;/callback）
              <input value={form.key} onChange={(e) => setForm({ ...form, key: e.target.value })} placeholder="org-acme" />
            </label>
          )}
          <label>
            显示名称
            <input value={form.label} onChange={(e) => setForm({ ...form, label: e.target.value })} placeholder="Acme (Lark)" />
          </label>
          <label>
            App ID
            <input value={form.app_id} onChange={(e) => setForm({ ...form, app_id: e.target.value })} placeholder="cli_xxx" />
          </label>
          <label>
            App Secret{editing !== "new" && "（留空则保持不变）"}
            <input type="password" value={form.app_secret} onChange={(e) => setForm({ ...form, app_secret: e.target.value })} />
          </label>
          <label>
            域
            <select value={form.domain} onChange={(e) => setForm({ ...form, domain: e.target.value })}>
              <option value="feishu">飞书 (open.feishu.cn)</option>
              <option value="lark">Lark (open.larksuite.com)</option>
            </select>
          </label>
          <div className="row">
            <button className="btn primary small" onClick={save}>
              保存
            </button>
            <button className="btn ghost small" onClick={() => setEditing(null)}>
              取消
            </button>
          </div>
          <p className="muted small-note">
            记得在该组织应用后台把重定向 URL 设为 &lt;PUBLIC_URL&gt;/api/auth/{form.key || "<key>"}/callback
          </p>
        </div>
      )}
    </div>
  );
}

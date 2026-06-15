import { useCallback, useEffect, useState } from "react";
import { Navigate } from "react-router-dom";
import { Plus, Shield } from "lucide-react";
import { api, type Connection, type ConnectionInput, type User } from "../api";
import { usePageUser } from "../App";
import { Avatar, Badge, Button, Field, Input } from "../components/ui";

export function Admin() {
  const me = usePageUser();
  if (me.role !== "admin") return <Navigate to="/browse" replace />;

  return (
    <div className="browser">
      <div className="page-header">
        <span className="page-header__title" style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <Shield size={16} />
          管理后台
        </span>
      </div>
      <div className="page-body">
        <div className="page-pad panel">
          <Members meId={me.id} />
          <Connections />
        </div>
      </div>
    </div>
  );
}

function Members({ meId }: { meId: string }) {
  const [users, setUsers] = useState<User[]>([]);
  const [err, setErr] = useState("");

  const load = useCallback(() => {
    api.adminUsers().then((r) => setUsers(r.users || [])).catch((e) => setErr(String(e)));
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
    <section className="panel-section">
      <div className="panel-section__head">
        <h3>成员（{users.length}）</h3>
      </div>
      {err && <p className="error-text" style={{ fontSize: 13, marginBottom: 10 }}>{err}</p>}
      <div className="data-card">
        {users.map((u) => (
          <div className="data-row" key={u.id}>
            <Avatar src={u.avatar_url} name={u.display_name || u.email} size={34} />
            <div className="data-row__main">
              <div className="data-row__title">
                {u.display_name || "—"}
                {u.id === meId && <span className="text-tertiary"> （你）</span>}
              </div>
              <div className="data-row__sub">{u.email || "—"}</div>
            </div>
            <Badge tone={u.role === "admin" ? "accent" : "neutral"}>
              {u.role === "admin" ? "管理员" : "成员"}
            </Badge>
            {u.banned && <Badge tone="danger">已封禁</Badge>}
            {u.id !== meId && (
              <div className="data-row__actions">
                {u.role === "admin" ? (
                  <Button size="sm" onClick={() => act(u.id, "demote")}>降为成员</Button>
                ) : (
                  <Button size="sm" onClick={() => act(u.id, "promote")}>设为管理员</Button>
                )}
                {u.banned ? (
                  <Button size="sm" onClick={() => act(u.id, "unban")}>解封</Button>
                ) : (
                  <Button size="sm" variant="danger" onClick={() => act(u.id, "ban")}>封禁</Button>
                )}
              </div>
            )}
          </div>
        ))}
      </div>
    </section>
  );
}

const EMPTY: ConnectionInput = { key: "", label: "", app_id: "", app_secret: "", domain: "feishu" };

function Connections() {
  const [conns, setConns] = useState<Connection[]>([]);
  const [editing, setEditing] = useState<string | null>(null); // connection id, "new", or null
  const [form, setForm] = useState<ConnectionInput>(EMPTY);
  const [err, setErr] = useState("");

  const load = useCallback(() => {
    api.adminConnections().then((r) => setConns(r.connections || [])).catch((e) => setErr(String(e)));
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
      if (editing === "new") await api.adminCreateConnection(form);
      else if (editing) await api.adminUpdateConnection(editing, form);
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
    <section className="panel-section">
      <div className="panel-section__head">
        <h3>连接 / 组织（{conns.length}）</h3>
        <Button size="sm" variant="primary" icon={Plus} onClick={startNew}>
          新增连接
        </Button>
      </div>
      <p className="panel-section__desc">
        每个连接对应一个飞书 / Lark 自建应用。记得在该组织应用后台注册重定向 URL
        &lt;PUBLIC_URL&gt;/api/auth/&lt;key&gt;/callback。
      </p>
      {err && <p className="error-text" style={{ fontSize: 13, marginBottom: 10 }}>{err}</p>}
      <div className="data-card">
        {conns.length === 0 && (
          <div className="data-row text-tertiary" style={{ fontSize: 13 }}>暂无连接。</div>
        )}
        {conns.map((c) => (
          <div className="data-row" key={c.id}>
            <div className="data-row__main">
              <div className="data-row__title">
                {c.label || c.key}
                <span className="text-tertiary mono" style={{ fontSize: 11, marginLeft: 8 }}>
                  {c.key}
                </span>
              </div>
              <div className="data-row__sub mono">
                {c.app_id} · {c.domain} · {c.has_secret ? "密钥已设置" : "密钥缺失"}
              </div>
            </div>
            {!c.has_secret && <Badge tone="danger">密钥缺失</Badge>}
            <div className="data-row__actions">
              <Button size="sm" onClick={() => startEdit(c)}>编辑</Button>
              <Button size="sm" variant="danger" onClick={() => remove(c.id)}>删除</Button>
            </div>
          </div>
        ))}
      </div>

      {editing && (
        <div className="form-card">
          <h4>{editing === "new" ? "新增连接" : "编辑连接"}</h4>
          {editing === "new" && (
            <Field label="Key（唯一，用于 OAuth 路由 /api/auth/<key>/callback）">
              <Input
                block
                value={form.key}
                onChange={(e) => setForm({ ...form, key: e.target.value })}
                placeholder="org-acme"
              />
            </Field>
          )}
          <Field label="显示名称">
            <Input
              block
              value={form.label}
              onChange={(e) => setForm({ ...form, label: e.target.value })}
              placeholder="Acme (Lark)"
            />
          </Field>
          <Field label="App ID">
            <Input
              block
              value={form.app_id}
              onChange={(e) => setForm({ ...form, app_id: e.target.value })}
              placeholder="cli_xxx"
            />
          </Field>
          <Field label={`App Secret${editing !== "new" ? "（留空则保持不变）" : ""}`}>
            <Input
              block
              type="password"
              value={form.app_secret}
              onChange={(e) => setForm({ ...form, app_secret: e.target.value })}
            />
          </Field>
          <Field label="域">
            <select
              className="input-wrap"
              style={{ height: 32 }}
              value={form.domain}
              onChange={(e) => setForm({ ...form, domain: e.target.value })}
            >
              <option value="feishu">飞书 (open.feishu.cn)</option>
              <option value="lark">Lark (open.larksuite.com)</option>
            </select>
          </Field>
          <div className="form-actions">
            <Button variant="primary" size="sm" onClick={save}>保存</Button>
            <Button variant="ghost" size="sm" onClick={() => setEditing(null)}>取消</Button>
          </div>
          <p className="form-card__hint">
            重定向 URL：&lt;PUBLIC_URL&gt;/api/auth/{form.key || "<key>"}/callback
          </p>
        </div>
      )}
    </section>
  );
}

import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Navigate } from "react-router-dom";
import { Plus, Shield } from "lucide-react";
import { api, type Connection, type ConnectionInput, type User } from "../api";
import { usePageUser } from "../App";
import i18n from "../lib/i18n";
import { Avatar, Badge, Button, Field, Input } from "../components/ui";

export function Admin() {
  const { t } = useTranslation();
  const me = usePageUser();
  if (me.role !== "admin") return <Navigate to="/browse" replace />;

  return (
    <div className="browser">
      <div className="page-header">
        <span className="page-header__title" style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <Shield size={16} />
          {t("admin.title")}
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
  const { t } = useTranslation();
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
        <h3>{t("admin.membersTitle", { count: users.length })}</h3>
      </div>
      {err && <p className="error-text" style={{ fontSize: 13, marginBottom: 10 }}>{err}</p>}
      <div className="data-card">
        {users.map((u) => (
          <div className="data-row" key={u.id}>
            <Avatar src={u.avatar_url} name={u.display_name || u.email} size={34} />
            <div className="data-row__main">
              <div className="data-row__title">
                {u.display_name || "—"}
                {u.id === meId && <span className="text-tertiary">{t("admin.you")}</span>}
              </div>
              <div className="data-row__sub">{u.email || "—"}</div>
            </div>
            <Badge tone={u.role === "admin" ? "accent" : "neutral"}>
              {u.role === "admin" ? t("role.admin") : t("role.member")}
            </Badge>
            {u.banned && <Badge tone="danger">{t("admin.banned")}</Badge>}
            {u.id !== meId && (
              <div className="data-row__actions">
                {u.role === "admin" ? (
                  <Button size="sm" onClick={() => act(u.id, "demote")}>{t("admin.demote")}</Button>
                ) : (
                  <Button size="sm" onClick={() => act(u.id, "promote")}>{t("admin.promote")}</Button>
                )}
                {u.banned ? (
                  <Button size="sm" onClick={() => act(u.id, "unban")}>{t("admin.unban")}</Button>
                ) : (
                  <Button size="sm" variant="danger" onClick={() => act(u.id, "ban")}>{t("admin.ban")}</Button>
                )}
              </div>
            )}
          </div>
        ))}
      </div>
    </section>
  );
}

const EMPTY: ConnectionInput = { provider_type: "feishu", key: "", label: "", app_id: "", app_secret: "", domain: "feishu" };

// Human-readable names for the provider implementation types the backend exposes;
// falls back to the raw type when no translation exists.
const typeLabel = (type: string) =>
  i18n.exists(`admin.providerLabels.${type}`)
    ? i18n.t(`admin.providerLabels.${type}`)
    : type;

function Connections() {
  const { t } = useTranslation();
  const [conns, setConns] = useState<Connection[]>([]);
  const [types, setTypes] = useState<string[]>(["feishu"]);
  const [editing, setEditing] = useState<string | null>(null); // connection id, "new", or null
  const [form, setForm] = useState<ConnectionInput>(EMPTY);
  const [err, setErr] = useState("");

  const load = useCallback(() => {
    api.adminConnections().then((r) => setConns(r.connections || [])).catch((e) => setErr(String(e)));
  }, []);
  useEffect(load, [load]);
  useEffect(() => {
    api.adminProviderTypes().then((r) => { if (r.types?.length) setTypes(r.types); }).catch(() => {});
  }, []);

  const startNew = () => {
    setForm(EMPTY);
    setEditing("new");
  };
  const startEdit = (c: Connection) => {
    setForm({ provider_type: c.provider_type, key: c.key, label: c.label, app_id: c.app_id, app_secret: "", domain: c.domain });
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
    if (!window.confirm(t("admin.confirmDeleteConnection"))) return;
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
        <h3>{t("admin.connectionsTitle", { count: conns.length })}</h3>
        <Button size="sm" variant="primary" icon={Plus} onClick={startNew}>
          {t("admin.addConnection")}
        </Button>
      </div>
      <p className="panel-section__desc">{t("admin.connectionsDesc")}</p>
      {err && <p className="error-text" style={{ fontSize: 13, marginBottom: 10 }}>{err}</p>}
      <div className="data-card">
        {conns.length === 0 && (
          <div className="data-row text-tertiary" style={{ fontSize: 13 }}>{t("admin.noConnections")}</div>
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
                {typeLabel(c.provider_type)} · {c.app_id}
                {c.provider_type === "feishu" ? ` · ${c.domain}` : ""} ·{" "}
                {c.has_secret ? t("admin.secretSet") : t("admin.secretMissing")}
              </div>
            </div>
            {!c.has_secret && <Badge tone="danger">{t("admin.secretMissing")}</Badge>}
            <div className="data-row__actions">
              <Button size="sm" onClick={() => startEdit(c)}>{t("common.edit")}</Button>
              <Button size="sm" variant="danger" onClick={() => remove(c.id)}>{t("common.delete")}</Button>
            </div>
          </div>
        ))}
      </div>

      {editing && (
        <div className="form-card">
          <h4>{editing === "new" ? t("admin.addConnection") : t("admin.editConnection")}</h4>
          {editing === "new" && (
            <Field label={t("admin.providerType")}>
              <select
                className="input-wrap"
                style={{ height: 32 }}
                value={form.provider_type}
                onChange={(e) => {
                  const pt = e.target.value;
                  // domain only applies to feishu; reset it when switching type.
                  setForm({ ...form, provider_type: pt, domain: pt === "feishu" ? "feishu" : "" });
                }}
              >
                {types.map((pt) => (
                  <option key={pt} value={pt}>{typeLabel(pt)}</option>
                ))}
              </select>
            </Field>
          )}
          {editing === "new" && (
            <Field label={t("admin.fieldKey")}>
              <Input
                block
                value={form.key}
                onChange={(e) => setForm({ ...form, key: e.target.value })}
                placeholder="org-acme"
              />
            </Field>
          )}
          <Field label={t("admin.fieldLabel")}>
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
          <Field label={editing !== "new" ? t("admin.fieldSecretEdit") : t("admin.fieldSecret")}>
            <Input
              block
              type="password"
              value={form.app_secret}
              onChange={(e) => setForm({ ...form, app_secret: e.target.value })}
            />
          </Field>
          {form.provider_type === "feishu" && (
            <Field label={t("admin.fieldDomain")}>
              <select
                className="input-wrap"
                style={{ height: 32 }}
                value={form.domain}
                onChange={(e) => setForm({ ...form, domain: e.target.value })}
              >
                <option value="feishu">{t("admin.domainFeishu")}</option>
                <option value="lark">{t("admin.domainLark")}</option>
              </select>
            </Field>
          )}
          {form.provider_type === "microsoft" && (
            <Field label={t("admin.fieldTenant")}>
              <Input
                block
                value={form.domain}
                onChange={(e) => setForm({ ...form, domain: e.target.value })}
                placeholder="common"
              />
            </Field>
          )}
          <div className="form-actions">
            <Button variant="primary" size="sm" onClick={save}>{t("common.save")}</Button>
            <Button variant="ghost" size="sm" onClick={() => setEditing(null)}>{t("common.cancel")}</Button>
          </div>
          <p className="form-card__hint">
            {t("admin.redirectHint", { key: form.key || "<key>" })}
          </p>
        </div>
      )}
    </section>
  );
}

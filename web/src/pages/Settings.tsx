import { useEffect, useState } from "react";
import { Monitor, Moon, Settings as SettingsIcon, Sun } from "lucide-react";
import { api, type ArchiveStats } from "../api";
import { usePageUser } from "../App";
import { useTheme } from "../lib/theme";
import { fileVisual } from "../lib/fileType";
import { Avatar, Badge, Spinner } from "../components/ui";

export function Settings() {
  const user = usePageUser();
  const { theme, set } = useTheme();

  return (
    <div className="browser">
      <div className="page-header">
        <span className="page-header__title" style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <SettingsIcon size={16} />
          设置
        </span>
      </div>
      <div className="page-body">
        <div className="page-pad panel">
          <section className="panel-section">
            <div className="panel-section__head">
              <h3>账号</h3>
            </div>
            <div className="data-card">
              <div className="data-row">
                <Avatar src={user.avatar_url} name={user.display_name || user.email} size={40} />
                <div className="data-row__main">
                  <div className="data-row__title">{user.display_name || "—"}</div>
                  <div className="data-row__sub">{user.email || "未提供邮箱"}</div>
                </div>
                <Badge tone={user.role === "admin" ? "accent" : "neutral"}>
                  {user.role === "admin" ? "管理员" : "成员"}
                </Badge>
              </div>
            </div>
          </section>

          <ArchiveStatsSection />

          <section className="panel-section">
            <div className="panel-section__head">
              <h3>外观</h3>
            </div>
            <p className="panel-section__desc">选择 docvault 的主题。</p>
            <div className="seg" style={{ height: 36 }}>
              <ThemeOption icon={Moon} label="深色" active={theme === "dark"} onClick={() => set("dark")} />
              <ThemeOption icon={Sun} label="浅色" active={theme === "light"} onClick={() => set("light")} />
            </div>
          </section>
        </div>
      </div>
    </div>
  );
}

function ArchiveStatsSection() {
  const [stats, setStats] = useState<ArchiveStats | null>(null);
  const [err, setErr] = useState("");

  useEffect(() => {
    api.stats().then(setStats).catch((e) => setErr(String(e)));
  }, []);

  const pct = stats && stats.total > 0 ? Math.round((stats.archived / stats.total) * 100) : 0;

  return (
    <section className="panel-section">
      <div className="panel-section__head">
        <h3>归档统计</h3>
        {stats && (
          <span className="text-tertiary num" style={{ fontSize: 12.5 }}>
            {stats.archived} / {stats.total} 已归档
          </span>
        )}
      </div>
      <p className="panel-section__desc">
        「已归档」表示有可下载副本；「未归档」是同步时未能导出的文档（缺导出权限或类型不支持）。
      </p>
      {err && <p className="error-text" style={{ fontSize: 13 }}>{err}</p>}
      {!stats && !err && (
        <div className="text-secondary" style={{ display: "flex", gap: 8, alignItems: "center", fontSize: 13 }}>
          <Spinner size={14} /> 统计中…
        </div>
      )}
      {stats && (
        <>
          <div className="stat-bar">
            <div className="stat-bar__fill" style={{ width: `${pct}%` }} />
          </div>
          <div className="stat-legend">
            <span>
              <b className="num">{stats.archived}</b> 已归档
            </span>
            <span className="stat-legend__warn">
              <b className="num">{stats.unarchived}</b> 未归档
            </span>
            <span className="text-tertiary">
              <b className="num">{stats.source_deleted}</b> 原件已删
            </span>
            <span className="text-tertiary">
              <b className="num">{stats.folders}</b> 文件夹
            </span>
          </div>
          {stats.by_type.length > 0 && (
            <div className="data-card" style={{ marginTop: 14 }}>
              {stats.by_type.map((t) => {
                const v = fileVisual({ format: "", doc_type: t.doc_type });
                return (
                  <div className="data-row" key={t.doc_type}>
                    <v.Icon color={v.color} style={{ width: 17, height: 17 }} />
                    <div className="data-row__main">
                      <div className="data-row__title">{v.label}</div>
                      <div className="data-row__sub num">
                        {t.archived} 已归档 · {t.unarchived} 未归档
                      </div>
                    </div>
                    {t.unarchived > 0 && <Badge tone="warning">{t.unarchived} 未归档</Badge>}
                  </div>
                );
              })}
            </div>
          )}
        </>
      )}
    </section>
  );
}

function ThemeOption({
  icon: Icon,
  label,
  active,
  onClick,
}: {
  icon: typeof Monitor;
  label: string;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      className={active ? "is-active" : ""}
      onClick={onClick}
      style={{ width: "auto", padding: "0 14px", gap: 6, fontSize: 13 }}
    >
      <Icon style={{ width: 15, height: 15 }} />
      {label}
    </button>
  );
}

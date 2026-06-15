import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Monitor, Moon, Settings as SettingsIcon, Sun } from "lucide-react";
import { api, type ArchiveStats } from "../api";
import { usePageUser } from "../App";
import { useTheme } from "../lib/theme";
import { fileVisual } from "../lib/fileType";
import { Avatar, Badge, Spinner } from "../components/ui";

export function Settings() {
  const { t } = useTranslation();
  const user = usePageUser();
  const { theme, set } = useTheme();

  return (
    <div className="browser">
      <div className="page-header">
        <span className="page-header__title" style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <SettingsIcon size={16} />
          {t("settings.title")}
        </span>
      </div>
      <div className="page-body">
        <div className="page-pad panel">
          <section className="panel-section">
            <div className="panel-section__head">
              <h3>{t("settings.account")}</h3>
            </div>
            <div className="data-card">
              <div className="data-row">
                <Avatar src={user.avatar_url} name={user.display_name || user.email} size={40} />
                <div className="data-row__main">
                  <div className="data-row__title">{user.display_name || "—"}</div>
                  <div className="data-row__sub">{user.email || t("settings.noEmail")}</div>
                </div>
                <Badge tone={user.role === "admin" ? "accent" : "neutral"}>
                  {user.role === "admin" ? t("role.admin") : t("role.member")}
                </Badge>
              </div>
            </div>
          </section>

          <ArchiveStatsSection />

          <section className="panel-section">
            <div className="panel-section__head">
              <h3>{t("settings.appearance")}</h3>
            </div>
            <p className="panel-section__desc">{t("settings.themeDesc")}</p>
            <div className="seg" style={{ height: 36 }}>
              <ThemeOption icon={Moon} label={t("settings.dark")} active={theme === "dark"} onClick={() => set("dark")} />
              <ThemeOption icon={Sun} label={t("settings.light")} active={theme === "light"} onClick={() => set("light")} />
            </div>
          </section>
        </div>
      </div>
    </div>
  );
}

function ArchiveStatsSection() {
  const { t } = useTranslation();
  const [stats, setStats] = useState<ArchiveStats | null>(null);
  const [err, setErr] = useState("");

  useEffect(() => {
    api.stats().then(setStats).catch((e) => setErr(String(e)));
  }, []);

  const pct = stats && stats.total > 0 ? Math.round((stats.archived / stats.total) * 100) : 0;

  return (
    <section className="panel-section">
      <div className="panel-section__head">
        <h3>{t("settings.statsTitle")}</h3>
        {stats && (
          <span className="text-tertiary num" style={{ fontSize: 12.5 }}>
            {t("settings.archivedRatio", { archived: stats.archived, total: stats.total })}
          </span>
        )}
      </div>
      <p className="panel-section__desc">{t("settings.statsDesc")}</p>
      {err && <p className="error-text" style={{ fontSize: 13 }}>{err}</p>}
      {!stats && !err && (
        <div className="text-secondary" style={{ display: "flex", gap: 8, alignItems: "center", fontSize: 13 }}>
          <Spinner size={14} /> {t("settings.statsLoading")}
        </div>
      )}
      {stats && (
        <>
          <div className="stat-bar">
            <div className="stat-bar__fill" style={{ width: `${pct}%` }} />
          </div>
          <div className="stat-legend">
            <span>
              <b className="num">{stats.archived}</b> {t("settings.archived")}
            </span>
            <span className="stat-legend__warn">
              <b className="num">{stats.unarchived}</b> {t("settings.unarchived")}
            </span>
            <span className="text-tertiary">
              <b className="num">{stats.source_deleted}</b> {t("settings.sourceDeleted")}
            </span>
            <span className="text-tertiary">
              <b className="num">{stats.folders}</b> {t("settings.folders")}
            </span>
          </div>
          {stats.by_type.length > 0 && (
            <div className="data-card" style={{ marginTop: 14 }}>
              {stats.by_type.map((bt) => {
                const v = fileVisual({ format: "", doc_type: bt.doc_type });
                return (
                  <div className="data-row" key={bt.doc_type}>
                    <v.Icon color={v.color} style={{ width: 17, height: 17 }} />
                    <div className="data-row__main">
                      <div className="data-row__title">{v.label}</div>
                      <div className="data-row__sub num">
                        {t("settings.typeBreakdown", { archived: bt.archived, unarchived: bt.unarchived })}
                      </div>
                    </div>
                    {bt.unarchived > 0 && (
                      <Badge tone="warning">{t("settings.unarchivedBadge", { count: bt.unarchived })}</Badge>
                    )}
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

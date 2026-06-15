import { useState } from "react";
import { useTranslation } from "react-i18next";
import { NavLink, useLocation, useNavigate } from "react-router-dom";
import {
  ChevronRight,
  Clock,
  Cloud,
  Files,
  Folder,
  Languages,
  LogOut,
  Moon,
  Settings,
  Shield,
  Sun,
  Trash2,
  Vault as VaultIcon,
} from "lucide-react";
import type { User } from "../api";
import { useVault } from "../lib/vault";
import { useTheme } from "../lib/theme";
import { SUPPORTED_LANGS, setLang, type Lang } from "../lib/i18n";
import { browseUrl, pathFromSplat } from "../lib/routes";
import { Avatar } from "./ui";

export function Sidebar({ user, onLogout }: { user: User; onLogout: () => void }) {
  const { t } = useTranslation();
  const { docs, tree } = useVault();
  const roots = tree.childFolders("");

  // Distinct providers present in the archive, for the SOURCES group.
  const sources = [...new Set(docs.map((d) => d.provider))].sort();

  return (
    <aside className="sidebar">
      <div className="sidebar__brand">
        <span className="brand-mark">
          <VaultIcon />
        </span>
        docvault
      </div>

      <nav className="sidebar__scroll">
        <NavItem to="/browse" icon={Files} label={t("nav.allFiles")} count={docs.length} end={false} />
        <NavItem to="/recent" icon={Clock} label={t("nav.recent")} />
        <NavItem to="/trash" icon={Trash2} label={t("nav.trash")} />

        {sources.length > 0 && (
          <div className="nav-group">
            <div className="nav-group__label">{t("nav.sources")}</div>
            {sources.map((s) => (
              <NavLink
                key={s}
                to={`/source/${encodeURIComponent(s)}`}
                className={({ isActive }) => "nav-item" + (isActive ? " nav-item--active" : "")}
              >
                <Cloud />
                <span className="nav-item__label">{s}</span>
              </NavLink>
            ))}
          </div>
        )}

        {roots.length > 0 && (
          <div className="nav-group">
            <div className="nav-group__label">{t("nav.folders")}</div>
            {roots.map((f) => (
              <TreeNode key={f.path} path={f.path} name={f.name} depth={0} />
            ))}
          </div>
        )}
      </nav>

      <UserFooter user={user} onLogout={onLogout} />
    </aside>
  );
}

function NavItem({
  to,
  icon: Icon,
  label,
  count,
  end = true,
}: {
  to: string;
  icon: typeof Files;
  label: string;
  count?: number;
  end?: boolean;
}) {
  return (
    <NavLink
      to={to}
      end={end}
      className={({ isActive }) => "nav-item" + (isActive ? " nav-item--active" : "")}
    >
      <Icon />
      <span className="nav-item__label">{label}</span>
      {count != null && count > 0 && <span className="nav-item__count">{count}</span>}
    </NavLink>
  );
}

function TreeNode({ path, name, depth }: { path: string; name: string; depth: number }) {
  const { tree } = useVault();
  const navigate = useNavigate();
  const location = useLocation();
  const [open, setOpen] = useState(false);

  const children = tree.childFolders(path);
  const hasChildren = children.length > 0;

  const currentPath = location.pathname.startsWith("/browse/")
    ? pathFromSplat(location.pathname.slice("/browse/".length))
    : location.pathname === "/browse"
      ? ""
      : null;
  const active = currentPath === path;

  return (
    <>
      <div
        className={"tree-item" + (active ? " tree-item--active" : "")}
        style={{ paddingLeft: 6 + depth * 14 }}
        onClick={() => navigate(browseUrl(path))}
      >
        <span
          className={"tree-item__caret" + (open ? " tree-item__caret--open" : "")}
          onClick={(e) => {
            e.stopPropagation();
            if (hasChildren) setOpen((v) => !v);
          }}
          style={{ visibility: hasChildren ? "visible" : "hidden" }}
        >
          <ChevronRight />
        </span>
        <Folder className="tree-folder-ic" />
        <span className="tree-item__label">{name}</span>
      </div>
      {open &&
        children.map((c) => (
          <TreeNode key={c.path} path={c.path} name={c.name} depth={depth + 1} />
        ))}
    </>
  );
}

function UserFooter({ user, onLogout }: { user: User; onLogout: () => void }) {
  const { t, i18n } = useTranslation();
  const [open, setOpen] = useState(false);
  const { theme, toggle } = useTheme();
  const navigate = useNavigate();

  // Cycle through the supported languages in order; the menu shows the current one.
  const currentLang = (i18n.resolvedLanguage || i18n.language) as Lang;
  const cycleLang = () => {
    const i = SUPPORTED_LANGS.indexOf(currentLang);
    setLang(SUPPORTED_LANGS[(i + 1) % SUPPORTED_LANGS.length]);
  };

  return (
    <div className="sidebar__footer">
      {open && (
        <>
          <div
            style={{ position: "fixed", inset: 0, zIndex: 55 }}
            onClick={() => setOpen(false)}
          />
          <div className="menu" role="menu">
            <button className="menu__item" onClick={() => { setOpen(false); navigate("/settings"); }}>
              <Settings />
              {t("menu.settings")}
            </button>
            {user.role === "admin" && (
              <button className="menu__item" onClick={() => { setOpen(false); navigate("/admin"); }}>
                <Shield />
                {t("menu.admin")}
              </button>
            )}
            <button className="menu__item" onClick={toggle}>
              {theme === "dark" ? <Sun /> : <Moon />}
              {theme === "dark" ? t("menu.lightTheme") : t("menu.darkTheme")}
              <span className="menu__switch">{theme === "dark" ? "Dark" : "Light"}</span>
            </button>
            <button className="menu__item" onClick={cycleLang}>
              <Languages />
              {t("settings.language")}
              <span className="menu__switch">{t(`language.${currentLang}`)}</span>
            </button>
            <div className="menu__sep" />
            <button className="menu__item" onClick={() => { setOpen(false); onLogout(); }}>
              <LogOut />
              {t("menu.logout")}
            </button>
          </div>
        </>
      )}
      <div className="user-chip" onClick={() => setOpen((v) => !v)}>
        <Avatar src={user.avatar_url} name={user.display_name || user.email} size={28} />
        <div className="user-chip__meta">
          <div className="user-chip__name">{user.display_name || t("user.me")}</div>
          <div className="user-chip__sub">{user.email || user.role}</div>
        </div>
      </div>
    </div>
  );
}

import { Monitor, Moon, Settings as SettingsIcon, Sun } from "lucide-react";
import { usePageUser } from "../App";
import { useTheme } from "../lib/theme";
import { Avatar, Badge } from "../components/ui";

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

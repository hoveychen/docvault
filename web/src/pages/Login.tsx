import { ArrowRight, Vault as VaultIcon } from "lucide-react";
import { api, type ProviderInfo } from "../api";
import { Button } from "../components/ui";

export function Login({ providers }: { providers: ProviderInfo[] }) {
  return (
    <div className="login-screen">
      <div className="login-card">
        <span className="login-card__mark">
          <VaultIcon />
        </span>
        <h1>docvault</h1>
        <p className="login-card__sub">
          你的私有云文档归档。授权一次，docvault 把云端文档同步成本地可下载的副本，
          再交由你统一浏览、管理与清理。
        </p>

        {providers.length === 0 ? (
          <p className="error-text" style={{ fontSize: 13 }}>
            尚未配置任何云文档来源——请在服务端设置 Feishu / Lark 凭据后再登录。
          </p>
        ) : (
          <div className="login-providers">
            {providers.map((p) => (
              <a key={p.key} href={api.loginUrl(p.key)} style={{ display: "block" }}>
                <Button variant="primary" size="lg" block>
                  使用 {p.label} 授权登录
                  <ArrowRight />
                </Button>
              </a>
            ))}
          </div>
        )}

        <p className="login-foot">
          docvault 仅读取你本人授权账号可访问的文档，凭据加密存储。删除云端原件等写操作需你显式确认。
        </p>
      </div>
    </div>
  );
}

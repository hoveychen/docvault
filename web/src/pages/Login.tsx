import { useTranslation } from "react-i18next";
import { ArrowRight, Vault as VaultIcon } from "lucide-react";
import { api, type ProviderInfo } from "../api";
import { Button } from "../components/ui";

export function Login({ providers }: { providers: ProviderInfo[] }) {
  const { t } = useTranslation();
  return (
    <div className="login-screen">
      <div className="login-card">
        <span className="login-card__mark">
          <VaultIcon />
        </span>
        <h1>docvault</h1>
        <p className="login-card__sub">{t("login.subtitle")}</p>

        {providers.length === 0 ? (
          <p className="error-text" style={{ fontSize: 13 }}>
            {t("login.noProviders")}
          </p>
        ) : (
          <div className="login-providers">
            {providers.map((p) => (
              <a key={p.key} href={api.loginUrl(p.key)} style={{ display: "block" }}>
                <Button variant="primary" size="lg" block>
                  {t("login.loginWith", { provider: p.label })}
                  <ArrowRight />
                </Button>
              </a>
            ))}
          </div>
        )}

        <p className="login-foot">{t("login.footer")}</p>
      </div>
    </div>
  );
}

import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  BrowserRouter,
  Navigate,
  Route,
  Routes,
  useOutletContext,
} from "react-router-dom";
import { api, type ProviderInfo, type User } from "./api";
import { AppShell } from "./components/AppShell";
import { Spinner } from "./components/ui";
import { Login } from "./pages/Login";
import { Browse } from "./pages/Browse";
import { Trash } from "./pages/Trash";
import { Admin } from "./pages/Admin";
import { Settings } from "./pages/Settings";

export interface OutletCtx {
  user: User;
}
export const usePageUser = () => useOutletContext<OutletCtx>().user;

export function App() {
  const { t } = useTranslation();
  const [user, setUser] = useState<User | null>(null);
  const [providers, setProviders] = useState<ProviderInfo[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    api.providers().then((p) => setProviders(p.providers || [])).catch(() => {});
    api
      .me()
      .then(setUser)
      .catch(() => setUser(null))
      .finally(() => setLoading(false));
  }, []);

  if (loading) {
    return (
      <div className="center-screen">
        <Spinner size={20} />
        {t("common.loading")}
      </div>
    );
  }

  if (!user) return <Login providers={providers} />;

  return (
    <BrowserRouter>
      <Routes>
        <Route element={<AppShell user={user} onLogout={() => setUser(null)} />}>
          <Route index element={<Navigate to="/browse" replace />} />
          <Route path="browse/*" element={<Browse mode="folder" />} />
          <Route path="recent" element={<Browse mode="recent" />} />
          <Route path="source/:provider" element={<Browse mode="source" />} />
          <Route path="trash" element={<Trash />} />
          <Route path="admin" element={<Admin />} />
          <Route path="settings" element={<Settings />} />
          <Route path="*" element={<Navigate to="/browse" replace />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}

import { Outlet } from "react-router-dom";
import type { User } from "../api";
import { VaultProvider } from "../lib/vault";
import { Sidebar } from "./Sidebar";

export function AppShell({ user, onLogout }: { user: User; onLogout: () => void }) {
  return (
    <VaultProvider>
      <div className="shell">
        <Sidebar user={user} onLogout={onLogout} />
        <main className="main">
          <Outlet context={{ user }} />
        </main>
      </div>
    </VaultProvider>
  );
}

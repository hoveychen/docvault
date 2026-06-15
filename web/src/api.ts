// Thin typed client for the docvault REST API. All calls send cookies.

export interface User {
  id: string;
  display_name: string;
  email: string;
  avatar_url: string;
  role: string;
  banned: boolean;
  created_at?: string;
}

export interface Connection {
  id: string;
  key: string;
  label: string;
  app_id: string;
  domain: string;
  has_secret: boolean;
}

export interface ConnectionInput {
  key?: string;
  label: string;
  app_id: string;
  app_secret: string;
  domain: string;
}

export interface DocItem {
  id: string;
  provider: string;
  title: string;
  doc_type: string;
  format: string;
  source_path: string;
  object_key: string; // empty when the item wasn't archived (unsupported type / export failed)
  size_bytes: number;
  synced_at: string;
  deletable: boolean;
  source_deleted_at?: string | null;
}

export interface FolderItem {
  id: string;
  provider: string;
  title: string;
  source_path: string;
  deletable: boolean;
  not_deletable_reason?: string;
  source_deleted_at?: string | null;
}

export interface ProviderInfo {
  key: string;
  label: string;
}

export interface DeleteResult {
  id: string;
  status: string;
  error?: string;
}

export interface SyncStatus {
  status: string;
  total_items?: number;
  done_items?: number;
  failed_items?: number;
  error?: string;
  finished_at?: string | null;
}

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, { credentials: "include", ...init });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.error || `${res.status} ${res.statusText}`);
  }
  return res.json() as Promise<T>;
}

export const api = {
  me: () => req<User>("/api/me"),
  providers: () => req<{ providers: ProviderInfo[] }>("/api/providers"),
  documents: () => req<{ documents: DocItem[] }>("/api/documents"),
  folders: () => req<{ folders: FolderItem[] }>("/api/folders"),
  syncStatus: () => req<SyncStatus>("/api/sync/status"),
  startSync: () => req<{ job_id: string; status: string }>("/api/sync", { method: "POST" }),
  logout: () => req<{ status: string }>("/api/auth/logout", { method: "POST" }),
  deleteFolderSource: (ids: string[]) =>
    req<{ results: DeleteResult[] }>("/api/folders/delete-source", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ folder_ids: ids }),
    }),
  deleteSource: (ids: string[]) =>
    req<{ results: DeleteResult[] }>("/api/documents/delete-source", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ document_ids: ids }),
    }),
  loginUrl: (provider: string) => `/api/auth/${provider}/login`,
  downloadUrl: (id: string) => `/api/documents/${id}/download`,

  // --- admin ---
  adminUsers: () => req<{ users: User[] }>("/api/admin/users"),
  adminUserAction: (id: string, action: "promote" | "demote" | "ban" | "unban") =>
    req<{ status: string }>(`/api/admin/users/${id}/${action}`, { method: "POST" }),
  adminConnections: () => req<{ connections: Connection[] }>("/api/admin/connections"),
  adminCreateConnection: (c: ConnectionInput) =>
    req<{ status: string }>("/api/admin/connections", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(c),
    }),
  adminUpdateConnection: (id: string, c: ConnectionInput) =>
    req<{ status: string }>(`/api/admin/connections/${id}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(c),
    }),
  adminDeleteConnection: (id: string) =>
    req<{ status: string }>(`/api/admin/connections/${id}`, { method: "DELETE" }),
};

// Thin typed client for the docvault REST API. All calls send cookies.

export interface User {
  id: string;
  display_name: string;
  email: string;
  avatar_url: string;
}

export interface DocItem {
  id: string;
  provider: string;
  title: string;
  doc_type: string;
  format: string;
  source_path: string;
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
  providers: () => req<{ providers: string[] }>("/api/providers"),
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
};

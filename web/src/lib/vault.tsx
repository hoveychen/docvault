import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { api, type DocItem, type FolderItem, type SyncStatus } from "../api";
import { buildTree, type FileTree } from "./tree";

interface VaultState {
  docs: DocItem[];
  folders: FolderItem[];
  status: SyncStatus | null;
  tree: FileTree;
  loading: boolean;
  error: string;
  syncing: boolean;
  refresh: () => Promise<void>;
  startSync: () => Promise<void>;
  deleteDocs: (ids: string[]) => Promise<{ ok: number; failed: string[] }>;
  deleteFolders: (ids: string[]) => Promise<{ ok: number; failed: string[] }>;
}

const Ctx = createContext<VaultState | null>(null);

export function VaultProvider({ children }: { children: ReactNode }) {
  const [docs, setDocs] = useState<DocItem[]>([]);
  const [folders, setFolders] = useState<FolderItem[]>([]);
  const [status, setStatus] = useState<SyncStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [syncing, setSyncing] = useState(false);
  const firstLoad = useRef(true);

  const refresh = useCallback(async () => {
    try {
      const [d, f, s] = await Promise.all([api.documents(), api.folders(), api.syncStatus()]);
      setDocs(d.documents || []);
      setFolders(f.folders || []);
      setStatus(s);
      setError("");
    } catch (e) {
      setError(String(e));
    } finally {
      if (firstLoad.current) {
        firstLoad.current = false;
        setLoading(false);
      }
    }
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  // Poll while a sync is queued/running so progress + new files appear live.
  const active = status?.status === "queued" || status?.status === "running";
  useEffect(() => {
    if (!active) return;
    const t = setInterval(() => refresh(), 2000);
    return () => clearInterval(t);
  }, [active, refresh]);

  const startSync = useCallback(async () => {
    setSyncing(true);
    setError("");
    try {
      await api.startSync();
      await refresh();
    } catch (e) {
      setError(String(e));
    } finally {
      setSyncing(false);
    }
  }, [refresh]);

  const deleteDocs = useCallback(
    async (ids: string[]) => {
      const { results } = await api.deleteSource(ids);
      const failed = results.filter((r) => r.status !== "deleted").map((r) => r.error || r.status);
      await refresh();
      return { ok: results.length - failed.length, failed };
    },
    [refresh],
  );

  const deleteFolders = useCallback(
    async (ids: string[]) => {
      const { results } = await api.deleteFolderSource(ids);
      const failed = results.filter((r) => r.status !== "deleted").map((r) => r.error || r.status);
      await refresh();
      return { ok: results.length - failed.length, failed };
    },
    [refresh],
  );

  const tree = useMemo(() => buildTree(docs, folders), [docs, folders]);

  const value: VaultState = {
    docs,
    folders,
    status,
    tree,
    loading,
    error,
    syncing: syncing || active,
    refresh,
    startSync,
    deleteDocs,
    deleteFolders,
  };

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useVault(): VaultState {
  const v = useContext(Ctx);
  if (!v) throw new Error("useVault must be used within VaultProvider");
  return v;
}

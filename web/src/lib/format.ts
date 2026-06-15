import i18n from "./i18n";

export function formatSize(bytes: number): string {
  if (!bytes) return "—";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let n = bytes;
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  return `${n.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

/** Compact, localized relative time with an absolute-date fallback. */
export function formatRelative(iso?: string | null): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const diff = Date.now() - d.getTime();
  const sec = Math.round(diff / 1000);
  if (sec < 60) return i18n.t("format.justNow");
  const min = Math.round(sec / 60);
  if (min < 60) return i18n.t("format.minutesAgo", { count: min });
  const hr = Math.round(min / 60);
  if (hr < 24) return i18n.t("format.hoursAgo", { count: hr });
  const day = Math.round(hr / 24);
  if (day < 30) return i18n.t("format.daysAgo", { count: day });
  return d.toLocaleDateString(i18n.language);
}

export function formatDate(iso?: string | null): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString(i18n.language);
}

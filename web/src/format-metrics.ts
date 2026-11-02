export function formatNumber(value?: number, digits = 1): string {
  if (value == null || !Number.isFinite(value)) return "—";
  return value.toLocaleString(undefined, { maximumFractionDigits: digits });
}

export function formatPercent(value?: number): string {
  return value == null ? "—" : `${formatNumber(value, 1)}%`;
}

export function formatBytes(value?: number): string {
  if (value == null || !Number.isFinite(value)) return "—";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let amount = value;
  let unit = 0;
  while (amount >= 1024 && unit < units.length - 1) {
    amount /= 1024;
    unit += 1;
  }
  return `${formatNumber(amount, amount < 10 ? 1 : 0)} ${units[unit]}`;
}

export function relativeTime(value?: string | Date): string {
  if (!value) return "—";
  const seconds = Math.max(0, Math.round((Date.now() - new Date(value).getTime()) / 1000));
  if (seconds < 5) return "just now";
  if (seconds < 60) return `${seconds}s ago`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  return `${Math.floor(seconds / 86400)}d ago`;
}

export function durationBetween(start: string, end: string): string {
  const seconds = Math.max(0, Math.round((new Date(end).getTime() - new Date(start).getTime()) / 1000));
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  return `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`;
}

export function titleCase(value?: string): string {
  return (value || "unknown").replaceAll("_", " ").replace(/\b\w/g, (letter) => letter.toUpperCase());
}

export function kindLabel(kind: string): string {
  const labels: Record<string, string> = {
    high_cpu: "High CPU",
    high_memory: "High memory",
    container_unavailable: "Container unavailable",
    agent_stale: "Agent stale",
  };
  return labels[kind] ?? titleCase(kind);
}

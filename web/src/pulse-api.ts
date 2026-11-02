import type {
  ContainerOverview,
  IncidentResponse,
  IncidentView,
  MetricsResponse,
  TimeRange,
} from "./pulse-types";

const rangeQuery: Record<TimeRange, { milliseconds: number; resolution: string }> = {
  "15m": { milliseconds: 15 * 60 * 1000, resolution: "5s" },
  "1h": { milliseconds: 60 * 60 * 1000, resolution: "10s" },
  "6h": { milliseconds: 6 * 60 * 60 * 1000, resolution: "1m" },
  "24h": { milliseconds: 24 * 60 * 60 * 1000, resolution: "5m" },
  "7d": { milliseconds: 7 * 24 * 60 * 60 * 1000, resolution: "30m" },
};

async function requestJSON<T>(url: string): Promise<T> {
  const response = await fetch(url, {
    headers: { Accept: "application/json" },
    cache: "no-store",
  });
  if (!response.ok) {
    const payload = (await response.json().catch(() => null)) as { error?: string } | null;
    throw new Error(payload?.error ?? `${response.status} ${response.statusText}`);
  }
  return response.json() as Promise<T>;
}

export const pulseAPI = {
  overview: () => requestJSON<ContainerOverview>("/api/v1/containers"),
  incidents: (view: IncidentView) =>
    requestJSON<IncidentResponse>(`/api/v1/incidents?view=${view}&limit=200`),
  metrics: (containerID: string, range: TimeRange) => {
    const selectedRange = rangeQuery[range];
    const to = new Date();
    const from = new Date(to.getTime() - selectedRange.milliseconds);
    const query = new URLSearchParams({
      from: from.toISOString(),
      to: to.toISOString(),
      resolution: selectedRange.resolution,
      limit: "1000",
    });
    return requestJSON<MetricsResponse>(
      `/api/v1/containers/${encodeURIComponent(containerID)}/metrics?${query}`,
    );
  },
};

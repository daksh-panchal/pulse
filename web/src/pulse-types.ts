export type Freshness = "current" | "delayed" | "stale";
export type IncidentState = "pending" | "firing" | "recovering" | "resolved";
export type IncidentView = "active" | "history" | "all";
export type TimeRange = "15m" | "1h" | "6h" | "24h" | "7d";
export type StatusLevel = "healthy" | "warning" | "critical";

export interface TelemetrySample {
  agent_id: string;
  container_id: string;
  container_name: string;
  image: string;
  sampled_at: string;
  state: string;
  health: string;
  cpu_limit_cores?: number;
  cpu_utilization_percent?: number;
  memory_working_set_bytes?: number;
  memory_limit_bytes?: number;
  memory_utilization_percent?: number;
  network_rx_bytes?: number;
  network_tx_bytes?: number;
}

export interface CurrentContainer extends TelemetrySample {
  freshness: Freshness;
}

export interface Incident {
  id: number;
  kind: string;
  agent_id: string;
  container_id?: string;
  state: IncidentState;
  title: string;
  threshold?: number;
  observed_value?: number;
  opened_at: string;
  last_evaluated_at: string;
  resolved_at?: string;
  details?: { container_name?: string };
}

export interface ContainerOverview {
  generated_at: string;
  containers: CurrentContainer[];
}

export interface IncidentResponse {
  incidents: Incident[];
}

export interface MetricsResponse {
  samples: TelemetrySample[];
}

export interface ContainerStatus {
  level: StatusLevel;
  label: string;
}

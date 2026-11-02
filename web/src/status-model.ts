import type { ContainerStatus, CurrentContainer, Incident } from "./pulse-types";

export function containerStatus(
  container: CurrentContainer,
  activeIncidents: Incident[],
): ContainerStatus {
  const incidents = activeIncidents.filter(
    (incident) => incident.container_id === container.container_id,
  );
  if (container.freshness === "stale") return { level: "critical", label: "Stale" };
  if (container.state !== "running" || container.health === "unhealthy") {
    return { level: "critical", label: "Unavailable" };
  }
  if (incidents.some((incident) => incident.state === "firing")) {
    return { level: "critical", label: "Alerting" };
  }
  if (container.freshness === "delayed" || incidents.length > 0) {
    return {
      level: "warning",
      label: container.freshness === "delayed" ? "Delayed" : "Pending",
    };
  }
  return { level: "healthy", label: "Healthy" };
}

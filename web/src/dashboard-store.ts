import { computed, DestroyRef, inject, Injectable, signal } from "@angular/core";
import { pulseAPI } from "./pulse-api";
import type {
  CurrentContainer,
  Incident,
  IncidentView,
  TelemetrySample,
  TimeRange,
} from "./pulse-types";

const pollInterval = 5_000;

@Injectable({ providedIn: "root" })
export class DashboardStore {
  readonly containers = signal<CurrentContainer[]>([]);
  readonly activeIncidents = signal<Incident[]>([]);
  readonly incidents = signal<Incident[]>([]);
  readonly samples = signal<TelemetrySample[]>([]);
  readonly selectedID = signal("");
  readonly range = signal<TimeRange>("1h");
  readonly incidentView = signal<IncidentView>("active");
  readonly generatedAt = signal<Date | undefined>(undefined);
  readonly online = signal(false);
  readonly loading = signal(true);
  readonly error = signal("");

  readonly selectedContainer = computed(() =>
    this.containers().find((container) => container.container_id === this.selectedID()),
  );

  private busy = false;
  private readonly destroyRef = inject(DestroyRef);

  constructor() {
    void this.refresh();
    const timer = window.setInterval(() => void this.refresh(), pollInterval);
    const handleVisibility = () => {
      if (!document.hidden) void this.refresh();
    };
    document.addEventListener("visibilitychange", handleVisibility);
    this.destroyRef.onDestroy(() => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", handleVisibility);
    });
  }

  async refresh(): Promise<void> {
    if (this.busy || document.hidden) return;
    this.busy = true;
    try {
      const view = this.incidentView();
      const [overview, incidentResponse, activeResponse] = await Promise.all([
        pulseAPI.overview(),
        pulseAPI.incidents(view),
        view === "active" ? Promise.resolve(null) : pulseAPI.incidents("active"),
      ]);

      const selectedID = overview.containers.some(
        (container) => container.container_id === this.selectedID(),
      )
        ? this.selectedID()
        : (overview.containers[0]?.container_id ?? "");
      const metrics = selectedID
        ? await pulseAPI.metrics(selectedID, this.range())
        : { samples: [] };

      this.selectedID.set(selectedID);
      this.containers.set(overview.containers);
      this.incidents.set(incidentResponse.incidents);
      this.activeIncidents.set(activeResponse?.incidents ?? incidentResponse.incidents);
      this.samples.set(metrics.samples);
      this.generatedAt.set(new Date(overview.generated_at));
      this.online.set(true);
      this.error.set("");
    } catch (cause) {
      this.online.set(false);
      this.error.set(
        `Dashboard update failed: ${cause instanceof Error ? cause.message : "unknown error"}. Pulse will retry automatically.`,
      );
    } finally {
      this.busy = false;
      this.loading.set(false);
    }
  }

  selectContainer(containerID: string): void {
    this.selectedID.set(containerID);
    void this.refresh();
  }

  selectRange(range: TimeRange): void {
    this.range.set(range);
    void this.refresh();
  }

  selectIncidentView(view: IncidentView): void {
    this.incidentView.set(view);
    void this.refresh();
  }
}

import { ChangeDetectionStrategy, Component, computed, input } from "@angular/core";
import { containerStatus } from "../status-model";
import type { CurrentContainer, Incident } from "../pulse-types";

@Component({
  selector: "pulse-summary-cards",
  templateUrl: "./summary-cards.component.html",
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class SummaryCardsComponent {
  readonly containers = input.required<CurrentContainer[]>();
  readonly activeIncidents = input.required<Incident[]>();

  readonly healthy = computed(() =>
    this.containers().filter(
      (container) => containerStatus(container, this.activeIncidents()).level === "healthy",
    ).length,
  );
  readonly current = computed(() =>
    this.containers().filter((container) => container.freshness === "current").length,
  );
}

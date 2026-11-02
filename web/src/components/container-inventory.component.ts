import { ChangeDetectionStrategy, Component, computed, input, output, signal } from "@angular/core";
import { formatPercent } from "../format-metrics";
import { containerStatus } from "../status-model";
import type { CurrentContainer, Incident } from "../pulse-types";

@Component({
  selector: "pulse-container-inventory",
  templateUrl: "./container-inventory.component.html",
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class ContainerInventoryComponent {
  readonly containers = input.required<CurrentContainer[]>();
  readonly activeIncidents = input.required<Incident[]>();
  readonly selectedID = input.required<string>();
  readonly containerSelected = output<string>();
  readonly filter = signal("");

  readonly filteredContainers = computed(() => {
    const query = this.filter().trim().toLowerCase();
    return this.containers().filter(
      (container) =>
        container.container_name.toLowerCase().includes(query) ||
        container.image.toLowerCase().includes(query),
    );
  });

  protected readonly formatPercent = formatPercent;
  protected readonly status = (container: CurrentContainer) =>
    containerStatus(container, this.activeIncidents());
  protected readonly progress = (value?: number) => `${Math.max(0, Math.min(100, value ?? 0))}%`;

  protected updateFilter(event: Event): void {
    this.filter.set((event.target as HTMLInputElement).value);
  }
}

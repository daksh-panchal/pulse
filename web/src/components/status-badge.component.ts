import { ChangeDetectionStrategy, Component, computed, input } from "@angular/core";
import type { StatusLevel } from "../pulse-types";

@Component({
  selector: "pulse-status-badge",
  templateUrl: "./status-badge.component.html",
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class StatusBadgeComponent {
  readonly level = input.required<StatusLevel>();
  readonly label = input.required<string>();
  readonly colorClass = computed(() => {
    const classes: Record<StatusLevel, string> = {
      healthy: "bg-accent/10 text-accent",
      warning: "bg-warning/10 text-warning",
      critical: "bg-danger/10 text-danger",
    };
    return classes[this.level()];
  });
}

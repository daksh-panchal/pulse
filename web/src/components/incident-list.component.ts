import { ChangeDetectionStrategy, Component, input, output } from "@angular/core";
import { durationBetween, kindLabel, relativeTime } from "../format-metrics";
import type { Incident, IncidentView, StatusLevel } from "../pulse-types";
import { StatusBadgeComponent } from "./status-badge.component";

@Component({
  selector: "pulse-incident-list",
  imports: [StatusBadgeComponent],
  templateUrl: "./incident-list.component.html",
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class IncidentListComponent {
  readonly incidents = input.required<Incident[]>();
  readonly view = input.required<IncidentView>();
  readonly viewChanged = output<IncidentView>();
  protected readonly viewOptions: IncidentView[] = ["active", "history", "all"];
  protected readonly durationBetween = durationBetween;
  protected readonly kindLabel = kindLabel;
  protected readonly relativeTime = relativeTime;

  protected level(incident: Incident): StatusLevel {
    if (incident.state === "resolved") return "healthy";
    if (incident.state === "firing") return "critical";
    return "warning";
  }
}

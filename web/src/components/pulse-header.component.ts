import { ChangeDetectionStrategy, Component, input } from "@angular/core";
import { relativeTime } from "../format-metrics";

@Component({
  selector: "pulse-header",
  templateUrl: "./pulse-header.component.html",
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class PulseHeaderComponent {
  readonly online = input.required<boolean>();
  readonly generatedAt = input<Date>();
  protected readonly relativeTime = relativeTime;
}

import { ChangeDetectionStrategy, Component, input } from "@angular/core";

@Component({
  selector: "pulse-resource-metric",
  templateUrl: "./resource-metric.component.html",
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class ResourceMetricComponent {
  readonly label = input.required<string>();
  readonly value = input.required<string>();
  readonly note = input.required<string>();
  readonly progress = input<number>();

  protected progressWidth(): string {
    return `${Math.max(0, Math.min(100, this.progress() ?? 0))}%`;
  }
}

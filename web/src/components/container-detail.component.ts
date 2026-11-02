import { ChangeDetectionStrategy, Component, computed, input, output } from "@angular/core";
import { formatBytes, formatNumber, formatPercent, relativeTime, titleCase } from "../format-metrics";
import { containerStatus } from "../status-model";
import type { CurrentContainer, Incident, TelemetrySample, TimeRange } from "../pulse-types";
import { MetricChartComponent, type ChartPoint } from "./metric-chart.component";
import { ResourceMetricComponent } from "./resource-metric.component";
import { StatusBadgeComponent } from "./status-badge.component";

@Component({
  selector: "pulse-container-detail",
  imports: [MetricChartComponent, ResourceMetricComponent, StatusBadgeComponent],
  templateUrl: "./container-detail.component.html",
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class ContainerDetailComponent {
  readonly container = input<CurrentContainer>();
  readonly activeIncidents = input.required<Incident[]>();
  readonly samples = input.required<TelemetrySample[]>();
  readonly range = input.required<TimeRange>();
  readonly rangeChanged = output<TimeRange>();

  protected readonly rangeOptions: TimeRange[] = ["15m", "1h", "6h", "24h", "7d"];
  protected readonly formatBytes = formatBytes;
  protected readonly formatNumber = formatNumber;
  protected readonly formatPercent = formatPercent;
  protected readonly relativeTime = relativeTime;
  protected readonly titleCase = titleCase;

  protected readonly status = computed(() => {
    const container = this.container();
    return container ? containerStatus(container, this.activeIncidents()) : undefined;
  });
  protected readonly cpuPoints = computed(() =>
    this.valuePoints("cpu_utilization_percent"),
  );
  protected readonly memoryPoints = computed(() =>
    this.valuePoints("memory_utilization_percent"),
  );
  protected readonly network = computed(() => this.networkRates());

  private valuePoints(
    key: "cpu_utilization_percent" | "memory_utilization_percent",
  ): ChartPoint[] {
    return this.samples().map((sample) => ({
      time: new Date(sample.sampled_at).getTime(),
      value: sample[key],
    }));
  }

  private networkRates(): { received: ChartPoint[]; sent: ChartPoint[] } {
    const received: ChartPoint[] = [];
    const sent: ChartPoint[] = [];
    const samples = this.samples();
    for (let index = 1; index < samples.length; index += 1) {
      const previous = samples[index - 1];
      const current = samples[index];
      const elapsed =
        (new Date(current.sampled_at).getTime() - new Date(previous.sampled_at).getTime()) /
        1000;
      const rate = (key: "network_rx_bytes" | "network_tx_bytes") => {
        const before = previous[key];
        const after = current[key];
        return elapsed > 0 && before != null && after != null && after >= before
          ? (after - before) / elapsed
          : undefined;
      };
      const time = new Date(current.sampled_at).getTime();
      received.push({ time, value: rate("network_rx_bytes") });
      sent.push({ time, value: rate("network_tx_bytes") });
    }
    return { received, sent };
  }
}

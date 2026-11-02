import { ChangeDetectionStrategy, Component, computed, input } from "@angular/core";
import { formatBytes, formatNumber } from "../format-metrics";
import type { TimeRange } from "../pulse-types";

export interface ChartPoint {
  time: number;
  value?: number;
}

export interface ChartSeries {
  points: ChartPoint[];
  color: string;
}

interface ChartLine {
  path: string;
  color: string;
}

@Component({
  selector: "pulse-metric-chart",
  templateUrl: "./metric-chart.component.html",
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class MetricChartComponent {
  readonly series = input.required<ChartSeries[]>();
  readonly range = input.required<TimeRange>();
  readonly percentage = input(false);
  readonly emptyMessage = input.required<string>();
  readonly chartLabel = input.required<string>();

  protected readonly model = computed(() => this.buildModel());

  private buildModel() {
    const width = 700;
    const height = 170;
    const bounds = { left: 42, right: 8, top: 8, bottom: 24 };
    const points = this.series()
      .flatMap((series) => series.points)
      .filter((point): point is Required<ChartPoint> => point.value != null && Number.isFinite(point.value));
    if (points.length < 2) return { hasData: false, width, height, grid: [], times: [], lines: [] as ChartLine[] };

    const minTime = Math.min(...points.map((point) => point.time));
    const maxTime = Math.max(...points.map((point) => point.time));
    const observedMax = Math.max(...points.map((point) => point.value));
    const maxValue = this.percentage()
      ? Math.max(100, Math.ceil(observedMax / 20) * 20)
      : this.niceMaximum(observedMax);
    const x = (time: number) =>
      bounds.left + ((time - minTime) / Math.max(1, maxTime - minTime)) * (width - bounds.left - bounds.right);
    const y = (value: number) =>
      bounds.top + (1 - value / Math.max(1, maxValue)) * (height - bounds.top - bounds.bottom);

    const grid = [0, 0.5, 1].map((fraction) => ({
      y: bounds.top + fraction * (height - bounds.top - bounds.bottom),
      label: this.formatAxis(maxValue * (1 - fraction)),
    }));
    const times = [0, 0.5, 1].map((fraction) => {
      const time = minTime + (maxTime - minTime) * fraction;
      return {
        x: x(time),
        label: this.shortTime(time),
        anchor: fraction === 0 ? "start" : fraction === 1 ? "end" : "middle",
      };
    });
    const lines = this.series().map((series) => {
      let path = "";
      let continuing = false;
      series.points.forEach((point) => {
        if (point.value == null || !Number.isFinite(point.value)) {
          continuing = false;
          return;
        }
        path += `${continuing ? "L" : "M"}${x(point.time).toFixed(1)},${y(point.value).toFixed(1)} `;
        continuing = true;
      });
      return { path: path.trim(), color: series.color };
    });
    return { hasData: true, width, height, grid, times, lines };
  }

  private formatAxis(value: number): string {
    return this.percentage() ? `${formatNumber(value, 0)}%` : `${formatBytes(value)}/s`;
  }

  private niceMaximum(value: number): number {
    if (!Number.isFinite(value) || value <= 0) return 1;
    const magnitude = 10 ** Math.floor(Math.log10(value));
    return Math.ceil(value / magnitude) * magnitude;
  }

  private shortTime(milliseconds: number): string {
    const date = new Date(milliseconds);
    if (this.range() === "24h" || this.range() === "7d") {
      return date.toLocaleDateString(undefined, {
        month: "short",
        day: "numeric",
        hour: this.range() === "24h" ? "numeric" : undefined,
      });
    }
    return date.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
  }
}

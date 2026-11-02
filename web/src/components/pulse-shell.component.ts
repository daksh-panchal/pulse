import { ChangeDetectionStrategy, Component, inject } from "@angular/core";
import { DashboardStore } from "../dashboard-store";
import { ContainerDetailComponent } from "./container-detail.component";
import { ContainerInventoryComponent } from "./container-inventory.component";
import { IncidentListComponent } from "./incident-list.component";
import { PulseHeaderComponent } from "./pulse-header.component";
import { SummaryCardsComponent } from "./summary-cards.component";

@Component({
  selector: "pulse-shell",
  imports: [
    ContainerDetailComponent,
    ContainerInventoryComponent,
    IncidentListComponent,
    PulseHeaderComponent,
    SummaryCardsComponent,
  ],
  templateUrl: "./pulse-shell.component.html",
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class PulseShellComponent {
  protected readonly dashboard = inject(DashboardStore);
}

import { provideZonelessChangeDetection } from "@angular/core";
import { bootstrapApplication } from "@angular/platform-browser";
import { PulseShellComponent } from "./components/pulse-shell.component";

bootstrapApplication(PulseShellComponent, {
  providers: [provideZonelessChangeDetection()],
}).catch((error: unknown) => console.error(error));

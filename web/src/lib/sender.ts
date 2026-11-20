import type { SenderInfo } from "./types";

// Names the caller for display: the invoker's process name, or else its
// systemd unit. Mirrors sender.Info.DisplayName on the server.
export function senderDisplayName(info: SenderInfo): string {
  return info.invoker_name || info.systemd_unit || "";
}

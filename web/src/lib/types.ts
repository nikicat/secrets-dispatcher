export interface ItemInfo {
  path: string;
  label: string;
  attributes: Record<string, string>;
}

export interface PendingRequest {
  id: string;
  client: string;
  items: ItemInfo[];
  session: string;
  created_at: string;
  expires_at: string;
  type: "get_secret" | "search";
  search_attributes?: Record<string, string>;
}

export interface ClientInfo {
  name: string;
  socket_path: string;
}

export interface StatusResponse {
  running: boolean;
  clients: ClientInfo[];
  pending_count: number;
  // Deprecated fields for backward compatibility
  client?: string;
  remote_socket?: string;
}

export interface PendingListResponse {
  requests: PendingRequest[];
}

export interface ActionResponse {
  status: string;
}

export interface ErrorResponse {
  error: string;
}

export type AuthState = "checking" | "authenticated" | "unauthenticated";

export type Resolution = "approved" | "denied" | "expired" | "cancelled";

export interface HistoryEntry {
  request: PendingRequest;
  resolution: Resolution;
  resolved_at: string;
}

// WebSocket message types
export type WSMessage =
  | WSSnapshotMessage
  | WSRequestCreatedMessage
  | WSRequestResolvedMessage
  | WSRequestExpiredMessage
  | WSRequestCancelledMessage
  | WSClientConnectedMessage
  | WSClientDisconnectedMessage
  | WSHistoryEntryMessage
  | WSPingMessage;

export interface WSSnapshotMessage {
  type: "snapshot";
  requests: PendingRequest[];
  clients: ClientInfo[];
  history: HistoryEntry[];
}

export interface WSRequestCreatedMessage {
  type: "request_created";
  request: PendingRequest;
}

export interface WSRequestResolvedMessage {
  type: "request_resolved";
  id: string;
  result: "approved" | "denied";
}

export interface WSRequestExpiredMessage {
  type: "request_expired";
  id: string;
}

export interface WSRequestCancelledMessage {
  type: "request_cancelled";
  id: string;
}

export interface WSClientConnectedMessage {
  type: "client_connected";
  client: ClientInfo;
}

export interface WSClientDisconnectedMessage {
  type: "client_disconnected";
  client: ClientInfo;
}

export interface WSHistoryEntryMessage {
  type: "history_entry";
  history_entry: HistoryEntry;
}

export interface WSPingMessage {
  type: "ping";
}

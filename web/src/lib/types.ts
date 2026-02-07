export interface PendingRequest {
  id: string;
  client: string;
  items: string[];
  session: string;
  created_at: string;
  expires_at: string;
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

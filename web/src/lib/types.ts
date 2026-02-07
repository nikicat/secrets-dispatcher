export interface PendingRequest {
  id: string;
  client: string;
  items: string[];
  session: string;
  created_at: string;
  expires_at: string;
}

export interface StatusResponse {
  running: boolean;
  client: string;
  pending_count: number;
  remote_socket: string;
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

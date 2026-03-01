import type {
  WSMessage,
  PendingRequest,
  ClientInfo,
  HistoryEntry,
  AutoApproveRule,
  TrustedSigner,
} from "./types";

export interface ApprovalWebSocketCallbacks {
  onSnapshot?: (requests: PendingRequest[], clients: ClientInfo[], history: HistoryEntry[], version: string, autoApproveRules: AutoApproveRule[], trustedSigners: TrustedSigner[], autoApproveDurationSeconds: number) => void;
  onRequestCreated?: (request: PendingRequest) => void;
  onRequestResolved?: (id: string, result: "approved" | "denied") => void;
  onRequestExpired?: (id: string) => void;
  onRequestCancelled?: (id: string) => void;
  onClientConnected?: (client: ClientInfo) => void;
  onClientDisconnected?: (client: ClientInfo) => void;
  onHistoryEntry?: (entry: HistoryEntry) => void;
  onAutoApproveRuleAdded?: (rule: AutoApproveRule) => void;
  onAutoApproveRuleRemoved?: (id: string) => void;
  onConnectionChange?: (isConnected: boolean) => void;
  onAuthError?: () => void;
  onVersionMismatch?: () => void;
}

export class ApprovalWebSocket {
  private ws: WebSocket | null = null;
  private callbacks: ApprovalWebSocketCallbacks;
  private reconnectDelay = 1000;
  private maxReconnectDelay = 30000;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private shouldReconnect = true;
  private isConnected = false;
  private clientVersion: string | null = null;

  constructor(callbacks: ApprovalWebSocketCallbacks) {
    this.callbacks = callbacks;
  }

  connect(): void {
    if (this.ws) {
      return;
    }

    this.shouldReconnect = true;
    this.doConnect();
  }

  private doConnect(): void {
    const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
    const url = `${protocol}//${window.location.host}/api/v1/ws`;

    try {
      this.ws = new WebSocket(url);
    } catch {
      this.scheduleReconnect();
      return;
    }

    this.ws.onopen = () => {
      this.isConnected = true;
      this.reconnectDelay = 1000; // Reset on successful connection
      this.callbacks.onConnectionChange?.(true);
    };

    this.ws.onclose = (event) => {
      this.ws = null;
      const wasConnected = this.isConnected;
      this.isConnected = false;

      if (wasConnected) {
        this.callbacks.onConnectionChange?.(false);
      }

      // WebSocket close code 1008 is explicit policy violation (auth error)
      if (event.code === 1008) {
        this.callbacks.onAuthError?.();
        this.shouldReconnect = false;
      }

      // For code 1006 (abnormal closure) when we never connected,
      // verify auth status before assuming it's an auth error
      if (event.code === 1006 && !wasConnected) {
        this.verifyAuthAndReconnect();
        return;
      }

      if (this.shouldReconnect) {
        this.scheduleReconnect();
      }
    };

    this.ws.onerror = () => {
      // Error handling is done in onclose
    };

    this.ws.onmessage = (event) => {
      this.handleMessage(event.data);
    };
  }

  disconnect(): void {
    this.shouldReconnect = false;

    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }

    if (this.ws) {
      this.ws.close();
      this.ws = null;
    }

    if (this.isConnected) {
      this.isConnected = false;
      this.callbacks.onConnectionChange?.(false);
    }
  }

  private handleMessage(data: string): void {
    let msg: WSMessage;
    try {
      msg = JSON.parse(data);
    } catch {
      console.error("Failed to parse WebSocket message:", data);
      return;
    }

    switch (msg.type) {
      case "snapshot":
        // Check for version mismatch on reconnect
        if (this.clientVersion === null) {
          // First connection - store version
          this.clientVersion = msg.version ?? "";
        } else if (this.clientVersion !== (msg.version ?? "")) {
          // Version mismatch - server was updated
          this.callbacks.onVersionMismatch?.();
          return;
        }
        this.callbacks.onSnapshot?.(msg.requests ?? [], msg.clients ?? [], msg.history ?? [], msg.version ?? "", msg.auto_approve_rules ?? [], msg.trusted_signers ?? [], msg.auto_approve_duration_seconds ?? 120);
        break;
      case "request_created":
        this.callbacks.onRequestCreated?.(msg.request);
        break;
      case "request_resolved":
        this.callbacks.onRequestResolved?.(msg.id, msg.result);
        break;
      case "request_expired":
        this.callbacks.onRequestExpired?.(msg.id);
        break;
      case "request_cancelled":
        this.callbacks.onRequestCancelled?.(msg.id);
        break;
      case "client_connected":
        this.callbacks.onClientConnected?.(msg.client);
        break;
      case "client_disconnected":
        this.callbacks.onClientDisconnected?.(msg.client);
        break;
      case "history_entry":
        this.callbacks.onHistoryEntry?.(msg.history_entry);
        break;
      case "auto_approve_rule_added":
        this.callbacks.onAutoApproveRuleAdded?.(msg.auto_approve_rule);
        break;
      case "auto_approve_rule_removed":
        this.callbacks.onAutoApproveRuleRemoved?.(msg.id);
        break;
      case "ping":
        // Server ping, no action needed
        break;
    }
  }

  private async verifyAuthAndReconnect(): Promise<void> {
    // Check if the server is up and we're still authenticated
    try {
      const response = await fetch("/api/v1/status", {
        credentials: "include",
      });

      if (response.status === 401) {
        // Confirmed auth error
        this.callbacks.onAuthError?.();
        this.shouldReconnect = false;
        return;
      }

      // Server is up and we're authenticated (or server is up but returned other error)
      // Continue reconnecting
      if (this.shouldReconnect) {
        this.scheduleReconnect();
      }
    } catch {
      // Network error - server probably not up yet, continue reconnecting
      if (this.shouldReconnect) {
        this.scheduleReconnect();
      }
    }
  }

  private scheduleReconnect(): void {
    if (this.reconnectTimer) {
      return;
    }

    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      if (this.shouldReconnect) {
        this.doConnect();
      }
    }, this.reconnectDelay);

    // Exponential backoff
    this.reconnectDelay = Math.min(
      this.reconnectDelay * 2,
      this.maxReconnectDelay,
    );
  }

  get connected(): boolean {
    return this.isConnected;
  }
}

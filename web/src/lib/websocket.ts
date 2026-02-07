import type {
  WSMessage,
  PendingRequest,
  ClientInfo,
} from "./types";

export interface ApprovalWebSocketCallbacks {
  onSnapshot?: (requests: PendingRequest[], clients: ClientInfo[]) => void;
  onRequestCreated?: (request: PendingRequest) => void;
  onRequestResolved?: (id: string, result: "approved" | "denied") => void;
  onRequestExpired?: (id: string) => void;
  onRequestCancelled?: (id: string) => void;
  onClientConnected?: (client: ClientInfo) => void;
  onClientDisconnected?: (client: ClientInfo) => void;
  onConnectionChange?: (isConnected: boolean) => void;
  onAuthError?: () => void;
}

export class ApprovalWebSocket {
  private ws: WebSocket | null = null;
  private callbacks: ApprovalWebSocketCallbacks;
  private reconnectDelay = 1000;
  private maxReconnectDelay = 30000;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private shouldReconnect = true;
  private isConnected = false;

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

      // Check for auth error (HTTP 401 during WebSocket upgrade)
      // WebSocket close code 1008 is policy violation, often used for auth errors
      // Close code 1006 is abnormal closure, which can happen with 401
      if (event.code === 1008 || (event.code === 1006 && !wasConnected)) {
        // This might be an auth error - don't reconnect automatically
        this.callbacks.onAuthError?.();
        this.shouldReconnect = false;
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
        this.callbacks.onSnapshot?.(msg.requests ?? [], msg.clients ?? []);
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
      case "ping":
        // Server ping, no action needed
        break;
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

import type { PendingRequest } from "./types";

/**
 * Browser notification service for approval requests.
 * Only shows notifications when the page is not visible (user is on another tab).
 */

let permissionState: NotificationPermission = "default";

/**
 * Check if browser notifications are supported.
 */
export function isSupported(): boolean {
  return "Notification" in window;
}

/**
 * Request permission to show notifications.
 * Returns true if permission is granted.
 */
export async function requestPermission(): Promise<boolean> {
  if (!isSupported()) {
    return false;
  }

  permissionState = Notification.permission;

  if (permissionState === "granted") {
    return true;
  }

  if (permissionState === "denied") {
    return false;
  }

  // Request permission
  permissionState = await Notification.requestPermission();
  return permissionState === "granted";
}

/**
 * Check if notifications are enabled (permission granted).
 */
export function isEnabled(): boolean {
  return isSupported() && Notification.permission === "granted";
}

/**
 * Show a notification for a new approval request.
 * Only shows if the page is hidden and notifications are enabled.
 */
export function showRequestNotification(request: PendingRequest): void {
  // Only notify if page is hidden (user is on another tab)
  if (!document.hidden) {
    return;
  }

  if (!isEnabled()) {
    return;
  }

  const title = request.type === "gpg_sign"
    ? "Commit Signing Request"
    : "Secret Request";
  const body = formatBody(request);

  // Use window.Notification to ensure we use the (potentially mocked) global
  const NotificationCtor = window.Notification;
  const notification = new NotificationCtor(title, {
    body,
    icon: "/favicon.ico",
    tag: request.id, // Prevents duplicate notifications for same request
    requireInteraction: true, // Keep notification visible until user interacts
  });

  // Focus the tab when notification is clicked
  notification.onclick = () => {
    window.focus();
    notification.close();
  };
}

function formatBody(request: PendingRequest): string {
  const parts: string[] = [];

  parts.push(`Client: ${request.client}`);

  if (request.sender_info?.unit_name) {
    parts.push(`Process: ${request.sender_info.unit_name}`);
  } else if (request.sender_info?.pid) {
    parts.push(`PID: ${request.sender_info.pid}`);
  }

  if (request.type === "gpg_sign" && request.gpg_sign_info) {
    parts.push(`Repo: ${request.gpg_sign_info.repo_name}`);
    parts.push(request.gpg_sign_info.commit_msg.split('\n')[0]);
    return parts.join("\n");
  }

  if (request.type === "get_secret") {
    if (request.items.length === 1) {
      parts.push(`Secret: ${request.items[0].label || request.items[0].path}`);
    } else {
      parts.push(`Secrets: ${request.items.length} items`);
    }
  } else {
    parts.push("Type: search");
  }

  return parts.join("\n");
}

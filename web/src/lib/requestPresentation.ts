import type { PendingRequest } from "./types";

export function requestApplication(request: PendingRequest): string {
  const direct = request.sender_info?.process_chain?.[0]?.name?.trim();
  if (direct) return direct;

  const invoker = request.sender_info?.invoker_name?.trim();
  if (invoker) return invoker.endsWith(".service") ? invoker.slice(0, -8) : invoker;

  return request.client || "Unknown application";
}

export function requestAction(request: PendingRequest): string {
  switch (request.type) {
    case "search": return "Search secrets";
    case "delete": return "Delete secret";
    case "write": return "Write secret";
    case "ssh_sign": return "Use SSH key";
    case "unlock": return "Unlock collection";
    case "gpg_sign":
      if (request.gpg_sign_info?.kind === "tag") return "Sign tag";
      if (request.gpg_sign_info?.kind === "push") return "Sign push";
      return "Sign commit";
    default: return "Read secret";
  }
}

function firstAttribute(attributes: Record<string, string> | undefined, keys: string[]): string {
  if (!attributes) return "";
  for (const key of keys) {
    const value = attributes[key]?.trim();
    if (value) return value;
  }
  return "";
}

export function requestSecret(request: PendingRequest): string {
  if (request.type === "gpg_sign" && request.gpg_sign_info) {
    return request.gpg_sign_info.commit_msg.split("\n")[0] || request.gpg_sign_info.repo_name;
  }

  if (request.items.length === 1) {
    const item = request.items[0];
    if (item.label && !item.label.startsWith("org.freedesktop.Secret.")) {
      return item.label;
    }

    const service = firstAttribute(item.attributes, ["service", "application", "app", "xdg:schema"]);
    let account = firstAttribute(item.attributes, ["account", "username", "user"]);
    let purpose = "";
    if (account.endsWith("_accessTokenKey")) {
      account = account.slice(0, -"_accessTokenKey".length);
      purpose = "access token";
    }
    if (account.length > 8) account = `${account.slice(0, 8)}…`;

    let description: string;
    if (service && purpose) description = `${service} — ${purpose}`;
    else if (service) description = service;
    else if (purpose) description = purpose;
    else description = "Generic secret";

    return account ? `${description} (account ${account})` : description;
  }

  if (request.items.length > 1) return `${request.items.length} secrets`;

  if (request.search_attributes && Object.keys(request.search_attributes).length > 0) {
    return Object.entries(request.search_attributes)
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([key, value]) => `${key}=${value}`)
      .join(", ");
  }

  return "Unspecified";
}

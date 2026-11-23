// The name of a Secret Service collection (dbustypes.CollectionName).
export type CollectionName = string;

// A shell pattern, as Go path.Match reads it (approval.Glob).
export type Glob = string;

export interface ItemInfo {
  path: string;
  label: string;
  attributes: Record<string, string>;
  collection?: CollectionName;
}

export interface ProcessInfo {
  name: string;
  pid: number;
  exe?: string;
  args?: string[];
  cwd?: string;
}

export interface SenderInfo {
  sender?: string; // absent when the request did not come over D-Bus
  pid: number;
  uid?: number; // absent when the bus could not report it
  invoker_name: string; // invoker process comm, empty without a /proc chain; spoofable
  systemd_unit?: string; // real systemd unit (authoritative)
  process_chain?: ProcessInfo[];
}

export interface GPGSignInfo {
  repo_name: string;
  // What kind of object is being signed. git routes commits, annotated tags,
  // and signed-push certificates through the same signer. author holds the
  // author/tagger/pusher and commit_msg holds the commit message / tag message
  // / pushed ref-updates; the kind-specific fields below carry the rest.
  kind: "commit" | "tag" | "push";
  commit_msg: string;
  author: string;
  committer: string;
  key_id: string;
  changed_files: string[];
  parent_hash?: string;
  tag_name?: string; // tag only
  target?: string; // tag only: the tagged object hash
  pushee?: string; // push only: destination URL
}

// A request as a History entry carries it. PendingRequest adds what only a
// pending request has.
export interface RequestRecord {
  id: string;
  client: string;
  items: ItemInfo[];
  session: string;
  created_at: string;
  type:
    | "get_secret"
    | "search"
    | "gpg_sign"
    | "delete"
    | "write"
    | "unlock"
    | "ssh_sign";
  search_attributes?: Record<string, string>;
  sender_info: SenderInfo;
  gpg_sign_info?: GPGSignInfo;
  // The scope that an auto-approve rule made from the request gets, for a
  // request that Go approval.Request.AutoApprovable accepts.
  auto_approve_scope?: AutoApproveScope;
}

export interface PendingRequest extends RequestRecord {
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

export type Resolution =
  "approved" | "denied" | "expired" | "cancelled" | "auto_approved" | "ignored";

// The resolutions that a request_resolved message reports.
export type ResolvedResult = Extract<
  Resolution,
  "approved" | "denied" | "auto_approved"
>;

export interface HistoryEntry {
  request: RequestRecord;
  resolution: Resolution;
  resolved_at: string;
}

// The requests that an auto-approve rule covers (Go approval.AutoApproveScope).
export interface AutoApproveScope {
  invoker_exe: string;
  request_type: string;
  collection?: CollectionName;
  attributes?: Record<string, string>;
}

export interface AutoApproveRule extends AutoApproveScope {
  id: string;
  invoker_name: string;
  expires_at: string;
}

export interface TrustedSigner {
  exe_path: string;
  repo_path?: string;
  file_prefix?: string;
}

export interface ProcessMatcher {
  exe?: Glob;
  name?: Glob;
  args?: Glob;
  cwd?: Glob;
  unit?: Glob;
}

export interface SecretMatcher {
  collection?: Glob;
  label?: Glob;
  attributes?: Record<string, Glob>;
}

// What a matching trust rule does with a request (approval.RuleAction).
export type RuleAction = "approve" | "ignore" | "deny";

export interface TrustRule {
  name?: string;
  action: RuleAction;
  request_types?: string[];
  process?: ProcessMatcher;
  client: Glob;
  secret?: SecretMatcher;
  search_attributes?: Record<string, Glob>;
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
  | WSAutoApproveRuleAddedMessage
  | WSAutoApproveRuleRemovedMessage;

export interface WSSnapshotMessage {
  type: "snapshot";
  version?: string;
  requests: PendingRequest[];
  clients: ClientInfo[];
  history: HistoryEntry[];
  auto_approve_rules: AutoApproveRule[];
  trusted_signers: TrustedSigner[];
  trust_rules: TrustRule[];
  auto_approve_duration_seconds?: number;
  notification_delay_ms?: number;
}

export interface WSRequestCreatedMessage {
  type: "request_created";
  request: PendingRequest;
}

export interface WSRequestResolvedMessage {
  type: "request_resolved";
  id: string;
  result: ResolvedResult;
}

export interface WSRequestExpiredMessage {
  type: "request_expired";
  id: string;
  result: "expired";
}

export interface WSRequestCancelledMessage {
  type: "request_cancelled";
  id: string;
  result: "cancelled";
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

export interface WSAutoApproveRuleAddedMessage {
  type: "auto_approve_rule_added";
  auto_approve_rule: AutoApproveRule;
}

export interface WSAutoApproveRuleRemovedMessage {
  type: "auto_approve_rule_removed";
  id: string;
}

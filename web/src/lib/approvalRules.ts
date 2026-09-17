import type {
  ApprovalRuleInput,
  ManagedTrustRule,
  PendingRequest,
  ProcessMatcher,
  SecretMatcher,
} from "./types";

const REQUEST_TYPES = new Set([
  "get_secret",
  "search",
  "delete",
  "write",
  "unlock",
  "ssh_sign",
]);
const TOP_LEVEL_FIELDS = new Set([
  "name",
  "enabled",
  "request_types",
  "process",
  "secret",
  "search_attributes",
]);
const PROCESS_FIELDS = new Set([
  "exe",
  "name",
  "args",
  "cwd",
  "unit",
  "direct",
]);
const SECRET_FIELDS = new Set(["collection", "label", "attributes"]);

export interface RequestApprovalScope {
  requestType: string;
  process: ProcessMatcher;
  secret?: SecretMatcher;
  searchAttributes?: Record<string, string>;
  allItems: boolean;
}

function objectValue(value: unknown, field: string): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new Error(`${field} must be an object`);
  }
  return value as Record<string, unknown>;
}

function rejectUnknownFields(
  value: Record<string, unknown>,
  allowed: Set<string>,
  field: string,
): void {
  const unknown = Object.keys(value).filter((key) => !allowed.has(key));
  if (unknown.length > 0) {
    throw new Error(`Unknown ${field} field: ${unknown.join(", ")}`);
  }
}

function stringMap(value: unknown, field: string): Record<string, string> {
  const object = objectValue(value, field);
  for (const [key, entry] of Object.entries(object)) {
    if (!key || typeof entry !== "string") {
      throw new Error(`${field} must contain non-empty keys and string values`);
    }
  }
  return object as Record<string, string>;
}

function processMatcher(value: unknown): ProcessMatcher {
  const process = objectValue(value, "process");
  rejectUnknownFields(process, PROCESS_FIELDS, "process");
  for (const field of ["exe", "name", "args", "cwd", "unit"] as const) {
    if (process[field] !== undefined && typeof process[field] !== "string") {
      throw new Error(`process.${field} must be a string`);
    }
  }
  if (process.direct !== undefined && typeof process.direct !== "boolean") {
    throw new Error("process.direct must be a boolean");
  }
  if (![process.exe, process.name, process.cwd, process.unit].some(Boolean)) {
    throw new Error("process requires at least one of exe, name, cwd, or unit");
  }
  if (process.direct === true && typeof process.exe === "string" && !process.exe.startsWith("/")) {
    throw new Error("process.exe must be absolute when direct is true");
  }
  return process as ProcessMatcher;
}

function secretMatcher(value: unknown): SecretMatcher {
  const secret = objectValue(value, "secret");
  rejectUnknownFields(secret, SECRET_FIELDS, "secret");
  for (const field of ["collection", "label"] as const) {
    if (secret[field] !== undefined && typeof secret[field] !== "string") {
      throw new Error(`secret.${field} must be a string`);
    }
  }
  if (secret.attributes !== undefined) {
    stringMap(secret.attributes, "secret.attributes");
  }
  return secret as SecretMatcher;
}

export function validateApprovalRule(value: unknown): ApprovalRuleInput {
  const rule = objectValue(value, "rule");
  rejectUnknownFields(rule, TOP_LEVEL_FIELDS, "rule");
  if (typeof rule.name !== "string" || !rule.name.trim()) {
    throw new Error("name must be a non-empty string");
  }
  if (typeof rule.enabled !== "boolean") {
    throw new Error("enabled must be true or false");
  }
  if (!Array.isArray(rule.request_types) || rule.request_types.length === 0 ||
    rule.request_types.some((type) => typeof type !== "string" || !REQUEST_TYPES.has(type))) {
    throw new Error(`request_types must contain supported values: ${[...REQUEST_TYPES].join(", ")}`);
  }
  const validated: ApprovalRuleInput = {
    name: rule.name,
    enabled: rule.enabled,
    request_types: rule.request_types as string[],
    process: processMatcher(rule.process),
  };
  if (rule.secret !== undefined) validated.secret = secretMatcher(rule.secret);
  if (rule.search_attributes !== undefined) {
    validated.search_attributes = stringMap(
      rule.search_attributes,
      "search_attributes",
    );
  }
  return validated;
}

function globQuote(value: string): string {
  let quoted = "";
  for (const character of value) {
    if (["\\", "*", "?", "["].includes(character)) quoted += "\\";
    quoted += character;
  }
  return quoted;
}

function quotedMap(values: Record<string, string> | undefined): Record<string, string> | undefined {
  if (!values || Object.keys(values).length === 0) return undefined;
  return Object.fromEntries(Object.entries(values).map(([key, value]) => [key, globQuote(value)]));
}

function mapsEqual(
  left: Record<string, string> | undefined,
  right: Record<string, string> | undefined,
): boolean {
  const leftEntries = Object.entries(left ?? {});
  const rightValues = right ?? {};
  return leftEntries.length === Object.keys(rightValues).length &&
    leftEntries.every(([key, value]) => rightValues[key] === value);
}

function processMatchersEqual(left: ProcessMatcher | undefined, right: ProcessMatcher): boolean {
  if (!left) return false;
  const fields = ["exe", "name", "args", "cwd", "unit"] as const;
  return fields.every((field) => left[field] === right[field]) &&
    left.direct === right.direct;
}

function secretMatchersEqual(left: SecretMatcher | undefined, right: SecretMatcher | undefined): boolean {
  if (!left || !right) return left === right;
  return left.collection === right.collection &&
    left.label === right.label &&
    mapsEqual(left.attributes, right.attributes);
}

export function findRequestApprovalRule(
  request: PendingRequest,
  rules: ManagedTrustRule[],
): ManagedTrustRule | undefined {
  const scope = deriveRequestApprovalScope(request);
  if (!scope) return undefined;

  const expectedProcess: ProcessMatcher = {
    ...scope.process,
    ...Object.fromEntries(
      Object.entries(scope.process)
        .filter(([, value]) => typeof value === "string")
        .map(([key, value]) => [key, globQuote(value as string)]),
    ),
  };
  const expectedSecret = scope.secret
    ? {
      collection: scope.secret.collection ? globQuote(scope.secret.collection) : undefined,
      label: scope.secret.label ? globQuote(scope.secret.label) : undefined,
      attributes: quotedMap(scope.secret.attributes),
    }
    : undefined;
  const expectedSearchAttributes = quotedMap(scope.searchAttributes);

  return rules.find((rule) =>
    rule.action === "approve" &&
    rule.request_types?.length === 1 &&
    rule.request_types[0] === scope.requestType &&
    processMatchersEqual(rule.process, expectedProcess) &&
    secretMatchersEqual(rule.secret, expectedSecret) &&
    mapsEqual(rule.search_attributes, expectedSearchAttributes)
  );
}

export function extractCollection(itemPath: string): string {
  for (const prefix of [
    "/org/freedesktop/secrets/collection/",
    "/org/freedesktop/secrets/aliases/",
  ]) {
    if (itemPath.startsWith(prefix)) {
      const rest = itemPath.slice(prefix.length);
      const slash = rest.indexOf("/");
      return slash >= 0 ? rest.slice(0, slash) : rest;
    }
  }
  return "";
}

function isInterpreter(exe: string): boolean {
  const base = exe.slice(exe.lastIndexOf("/") + 1).toLowerCase();
  return base.startsWith("python") || [
    "sh",
    "bash",
    "dash",
    "zsh",
    "fish",
    "ksh",
    "csh",
    "tcsh",
    "node",
    "nodejs",
    "ruby",
    "perl",
  ].includes(base);
}

export function deriveRequestApprovalScope(
  request: PendingRequest,
): RequestApprovalScope | null {
  if (!REQUEST_TYPES.has(request.type)) return null;
  const direct = request.sender_info?.process_chain?.[0];
  if (!direct?.exe?.startsWith("/")) return null;

  const process: ProcessMatcher = { exe: direct.exe, direct: true };
  if (isInterpreter(direct.exe)) {
    const args = direct.args ?? [];
    const script = args[1] === "--" ? args[2] : args[1];
    if (!script || script.startsWith("-")) return null;
    process.args = script;
    if (!script.startsWith("/")) {
      if (!direct.cwd?.startsWith("/")) return null;
      process.cwd = direct.cwd;
    }
  }

  if (request.type === "search") {
    return {
      requestType: request.type,
      process,
      searchAttributes: request.search_attributes,
      allItems: false,
    };
  }
  const item = request.items[0];
  return {
    requestType: request.type,
    process,
    secret: item
      ? {
        collection: extractCollection(item.path),
        attributes: item.attributes,
      }
      : undefined,
    allItems: Boolean(item),
  };
}

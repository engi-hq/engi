import type {
  CredentialRef, Descriptor, NodeDefinition, Option, PortSpec, Property,
} from "./types.js";

export const PROPERTY_TYPES = [
  "string", "number", "boolean", "options", "multiOptions", "json", "code", "dateTime",
  "collection", "fixedCollection", "resourceLocator", "credentialsSelect", "notice",
];
/** Groups a v1 plugin may declare. */
export const ALLOWED_GROUPS = ["action", "transform", "flow"];
export const MERGE_POLICIES = ["", "waitAll", "any", "partialOnFlush"];
export const PORT_MAIN = "main";
export const RESERVED_TYPE_PREFIX = "engi.";

const typeRE = /^[a-zA-Z][a-zA-Z0-9_-]*(\.[a-zA-Z][a-zA-Z0-9_-]*)+$/;

/** Fills defaults: one main port each way, display name = type, group = action. */
export function normalizeDefinition(def: NodeDefinition): Descriptor {
  const ports = (v: Array<PortSpec | string> | undefined): PortSpec[] =>
    v === undefined ? [{ name: PORT_MAIN }] : v.map((p) => (typeof p === "string" ? { name: p } : p));
  return {
    type: def.type,
    version: def.version ?? 1,
    displayName: def.displayName ?? def.type,
    group: def.group ?? "action",
    description: def.description ?? "",
    icon: def.icon ?? "",
    properties: def.properties ?? [],
    inputs: ports(def.inputs),
    outputs: ports(def.outputs),
    credentials: def.credentials ?? [],
    mergePolicy: def.mergePolicy ?? "",
  };
}

/**
 * Returns why a plugin may not declare d (docs/15 section 15.4); empty when valid.
 * Mirrors pluginproto.CheckDescriptor except the manifest-listing rule, which the host checks.
 */
export function checkDescriptor(d: Descriptor): string[] {
  const errs: string[] = [];
  if (!typeRE.test(d.type ?? "")) {
    errs.push(`node type "${d.type}": use dot-separated names such as acme.text.reverse`);
  }
  if ((d.type ?? "").startsWith(RESERVED_TYPE_PREFIX)) {
    errs.push(`node type "${d.type}": the "${RESERVED_TYPE_PREFIX}" prefix is reserved for built-in nodes`);
  }
  if (!ALLOWED_GROUPS.includes(d.group)) {
    errs.push(`group "${d.group}": plugins may declare ${ALLOWED_GROUPS.join("/")} nodes only`);
  }
  if (!Number.isInteger(d.version) || d.version < 1) errs.push("version must be an integer of at least 1");
  if (!MERGE_POLICIES.includes(d.mergePolicy)) errs.push(`unknown merge policy "${d.mergePolicy}"`);
  for (const [dir, ports] of [["inputs", d.inputs], ["outputs", d.outputs]] as const) {
    ports.forEach((p, i) => {
      if ((p.kind || PORT_MAIN) !== PORT_MAIN) {
        errs.push(`${dir}[${i}] "${p.name}": plugins may only use main ports, not "${p.kind}"`);
      }
      if (p.maxConnections) errs.push(`${dir}[${i}]: maxConnections only applies to sub-port inputs`);
    });
  }
  const seen = new Set<string>();
  d.properties.forEach((p, i) => {
    if (!p.name) {
      errs.push(`properties[${i}]: name must not be empty`);
      return;
    }
    if (seen.has(p.name)) errs.push(`properties[${i}]: duplicate name "${p.name}"`);
    seen.add(p.name);
    if (!PROPERTY_TYPES.includes(p.type)) errs.push(`properties[${i}] "${p.name}": unknown type "${p.type}"`);
  });
  return errs;
}

type Dict = Record<string, unknown>;

/** Drops empty values like Go's omitempty, except the keys in always. */
function compact(o: Dict, always: string[] = []): Dict {
  const out: Dict = {};
  for (const [k, v] of Object.entries(o)) {
    const empty =
      v === undefined || v === null || v === false || v === 0 || v === "" ||
      (Array.isArray(v) && v.length === 0) ||
      (typeof v === "object" && !Array.isArray(v) && Object.keys(v as object).length === 0);
    if (always.includes(k) || !empty) out[k] = v;
  }
  return out;
}

const optionWire = (o: Option): Dict => compact({ ...o }, ["name", "value"]);

function propertyWire(p: Property): Dict {
  const d = compact({
    name: p.name,
    displayName: p.displayName,
    type: p.type,
    required: p.required,
    placeholder: p.placeholder,
    description: p.description,
    options: p.options?.map(optionWire),
    children: p.children?.map(propertyWire),
  }, ["name", "type"]);
  if (p.default !== undefined && p.default !== null) d.default = p.default;
  if (p.displayOptions) {
    const show = compact({ ...(p.displayOptions.show ? { show: p.displayOptions.show } : {}) });
    const hide = compact({ ...(p.displayOptions.hide ? { hide: p.displayOptions.hide } : {}) });
    const display = { ...show, ...hide };
    if (Object.keys(display).length) d.displayOptions = display;
  }
  return d;
}

const portWire = (p: PortSpec): Dict => compact({ ...p }, ["name"]);
const credentialWire = (c: CredentialRef): Dict => compact({ ...c }, ["name", "type"]);

/** The wire form of a descriptor (camelCase, empty values omitted). */
export function descriptorWire(d: Descriptor): Dict {
  return compact({
    type: d.type,
    version: d.version,
    displayName: d.displayName,
    description: d.description,
    group: d.group,
    icon: d.icon,
    inputs: d.inputs.map(portWire),
    outputs: d.outputs.map(portWire),
    properties: d.properties.map(propertyWire),
    credentials: d.credentials.map(credentialWire),
    mergePolicy: d.mergePolicy,
  }, ["type", "version", "displayName", "group"]);
}

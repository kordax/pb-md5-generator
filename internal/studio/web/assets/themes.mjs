import { APIError } from "./core.mjs";

export const PALETTE_TOKENS = ["background", "surface", "text", "muted", "border", "accent"];

export function mergeThemeFields(existing, fields) {
  const { builtin: _builtin, sync: _sync, ...result } = { ...existing, ...fields };
  for (const mode of ["light", "dark"]) {
    const palette = { ...(existing?.[mode] || {}) };
    for (const token of PALETTE_TOKENS) {
      const key = mode + "." + token;
      if (!Object.hasOwn(fields, key)) continue;
      const value = String(fields[key]).trim();
      if (value && !/^#[0-9a-fA-F]{6}$/.test(value)) throw new APIError("Use a six-digit hexadecimal color, or leave the palette color blank.", 400, "invalid_color");
      if (value) palette[token] = value; else delete palette[token];
      delete result[key];
    }
    if (Object.keys(palette).length) result[mode] = palette; else delete result[mode];
  }
  return result;
}

import {
  VERIDIAN_PROVIDER_CLASSES,
  VeridianProviderClass,
} from "../../services/api/workspace";

const RATES_KEY = "veridian_provider_class_rates";
const PIXEL_KEY = "veridian_open_pixel_by_class";

// Extrait la map {classe: rate>0} du metadata, en ignorant tout ce qui n'est
// pas une classe canonique avec un débit numérique strictement positif.
export function parseBroadcastRates(
  metadata?: Record<string, unknown>,
): Partial<Record<VeridianProviderClass, number>> {
  const out: Partial<Record<VeridianProviderClass, number>> = {};
  if (!metadata) return out;
  const raw = metadata[RATES_KEY];
  if (!raw || typeof raw !== "object") return out;
  const values = raw as Record<string, unknown>;
  for (const providerClass of VERIDIAN_PROVIDER_CLASSES) {
    const value = values[providerClass];
    if (typeof value === "number" && value > 0) out[providerClass] = value;
  }
  return out;
}

// Extrait la map {classe: bool} de la politique pixel posée sur le broadcast.
// Ignore tout ce qui n'est pas une classe canonique avec une valeur booléenne.
export function parseBroadcastPixels(
  metadata?: Record<string, unknown>,
): Partial<Record<VeridianProviderClass, boolean>> {
  const out: Partial<Record<VeridianProviderClass, boolean>> = {};
  if (!metadata) return out;
  const raw = metadata[PIXEL_KEY];
  if (!raw || typeof raw !== "object") return out;
  const values = raw as Record<string, unknown>;
  for (const providerClass of VERIDIAN_PROVIDER_CLASSES) {
    const value = values[providerClass];
    if (typeof value === "boolean") out[providerClass] = value;
  }
  return out;
}

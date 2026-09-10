// Type definitions for the model gallery API.
//
// These types mirror the Go structs in internal/classifier/model_manager.go
// and the API response types in internal/api/v2/models/models.go.

/**
 * A structured, localizable reason for a variant's recommendation or
 * incompatibility. `code` is an i18n key stem (e.g. "backend.recommended"), never
 * an English sentence; `args` carries values interpolated into the localized text.
 */
export interface VariantReason {
  code: string;
  args?: Record<string, string>;
}

/**
 * One selectable hardware or regional variant of a catalog entry. Mirrors the
 * Go CatalogVariantResponse (internal/api/v2/models/models.go). The
 * `compatible`/`recommended`/`reasons`/`blockers` fields are populated only when
 * the request is eligible for hardware recommendations; otherwise `compatible`
 * defaults to true and the reason lists are absent.
 */
export interface CatalogVariant {
  id: string;
  region?: string;
  precision?: string;
  speciesCount: number;
  default: boolean;
  installed: boolean;
  /** True for the embedded baseline variant (the built-in BirdNET v2.4 model); it carries no downloadable files. */
  builtIn?: boolean;
  sizeBytes: number;
  headlineLatencyMs?: number;
  compatible: boolean;
  recommended: boolean;
  reasons?: VariantReason[];
  blockers?: VariantReason[];
  /**
   * Coarse hardware-target token for the plain-language chip (never raw precision):
   * one of 'gpuNvidia' | 'gpuIntel' | 'amd64Cpu' | 'arm64Cpu' | 'armCpu' | 'cpu' | 'builtIn'.
   * Computed server-side from the host arch and chosen backend; absent on an older
   * server, in which case the client derives a coarser class from the id.
   */
  hardwareClass?: string;
}

/** A model entry in the catalog, enriched with install/compat status. */
export interface CatalogEntry {
  id: string;
  name: string;
  description: string;
  author: string;
  license: string;
  commercialUse: boolean;
  category: 'wildlife' | 'bird' | 'bat' | 'geomodel';
  region: string;
  speciesCount: number;
  version: string;
  /** Release channel: 'stable' for a GA build, 'preview' for a developer preview the gallery flags as not-GA. Always present. */
  channel: string;
  /** Human-facing build tag shown next to the version for a non-stable channel (e.g. 'preview3.1'); absent for stable releases. */
  buildLabel?: string;
  upstreamUrl?: string;
  installed: boolean;
  compatible: boolean;
  incompatibleReason?: string;
  totalSizeBytes: number;
  hasGeomodel: boolean;
  /** True for the permanent built-in BirdNET v2.4 model: always installed, never uninstallable, only its variant may be swapped. */
  permanent?: boolean;
  /** Selectable hardware/regional variants, absent for flat single-variant entries. */
  variants?: CatalogVariant[];
  /** The id of the currently installed variant, or absent when not installed or flat. */
  installedVariantId?: string;
  /** The variant the gallery preselects for this host, absent when not eligible. */
  recommendedVariantId?: string;
}

/** Response wrapper for the catalog endpoint. */
export interface CatalogResponse {
  catalog: CatalogEntry[];
}

/** A model that has been downloaded and is available on disk. */
export interface InstalledModel {
  catalogId: string;
  modelPath: string;
  labelsPath: string;
  installedAt: string;
  version: string;
  /** The installed variant id, absent for flat (pre-variant) entries. */
  variantId?: string;
}

/** Progress state for an ongoing model download, sent via SSE. */
export interface DownloadProgress {
  catalogId: string;
  status: 'downloading' | 'verifying' | 'loading' | 'complete' | 'failed';
  downloadedBytes: number;
  totalBytes: number;
  currentFile: number;
  totalFiles: number;
  error?: string;
}

/** ISO 3166-1 alpha-2 country codes a region covers. Mirrors Go region.Countries. */
export interface RegionCountries {
  // A nil Go slice marshals as null, so the types include null to force the
  // `?? []` guard at every call site.
  core: string[] | null; // fully inside the model's sampled footprint
  partial: string[] | null; // range-straddling or clipped at the footprint edge
}

/** One selectable region in the gallery region selector. Mirrors Go RegionOption. */
export interface RegionOption {
  slug: string;
  name: string;
  group: string; // continental bucket slug, for grouping in the UI
  groupDisplay: string; // continental bucket display name
  tier: number;
  // ISO codes localized client-side via Intl.DisplayNames. A nil Go slice
  // marshals as null, so consumers guard each list with `?? []`.
  countries: RegionCountries;
}

/**
 * How the server resolved the configured coordinates to a region. Mirrors Go
 * RegionResolution. The endpoint always computes this under automatic mode (a
 * preview), so in practice `source` is 'auto' or 'global'; 'pinned' and
 * 'pinned-fallback' exist in the contract for future per-family use. `slug` is
 * empty when the global model applies.
 */
export interface RegionResolution {
  slug: string;
  source: 'pinned' | 'auto' | 'pinned-fallback' | 'global';
  ambiguous: boolean;
  runnerUp?: string;
}

/** Per-family region resolution under the configured coordinates. Mirrors Go RegionFamily. */
export interface RegionFamily {
  catalogId: string;
  repo: string;
  installed: boolean;
  installedVariantRegion: string; // region of the installed variant, "" for a global/hardware variant
  resolved: RegionResolution;
}

/** Response of GET /api/v2/models/regions. Mirrors Go ModelRegionsResponse. */
export interface ModelRegionsResponse {
  modelRegion: string; // the saved BirdNET.ModelRegion setting
  locationConfigured: boolean;
  resolved: RegionResolution; // what "auto" resolves to from the coordinates
  regions: RegionOption[]; // dropdown options, union across families
  families: RegionFamily[]; // per-family resolution
}

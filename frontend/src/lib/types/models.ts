// Type definitions for the model gallery API.
//
// These types mirror the Go structs in internal/classifier/model_manager.go
// and the API response types in internal/api/v2/models.go.

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
  sizeBytes: number;
  headlineLatencyMs?: number;
  compatible: boolean;
  recommended: boolean;
  reasons?: VariantReason[];
  blockers?: VariantReason[];
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
  upstreamUrl?: string;
  installed: boolean;
  compatible: boolean;
  incompatibleReason?: string;
  totalSizeBytes: number;
  hasGeomodel: boolean;
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

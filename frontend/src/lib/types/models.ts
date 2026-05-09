// Type definitions for the model gallery API.
//
// These types mirror the Go structs in internal/classifier/model_manager.go
// and the API response types in internal/api/v2/models.go.

/** A model entry in the catalog, enriched with install/compat status. */
export interface CatalogEntry {
  id: string;
  name: string;
  description: string;
  author: string;
  license: string;
  commercialUse: boolean;
  category: 'bird' | 'bat';
  region: string;
  speciesCount: number;
  version: string;
  installed: boolean;
  compatible: boolean;
  totalSizeBytes: number;
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
}

/** Progress state for an ongoing model download, sent via SSE. */
export interface DownloadProgress {
  catalogId: string;
  status: 'downloading' | 'verifying' | 'loading' | 'complete' | 'failed';
  downloadedBytes: number;
  totalBytes: number;
  error?: string;
}

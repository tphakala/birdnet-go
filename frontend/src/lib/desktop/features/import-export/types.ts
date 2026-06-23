// API types for BirdNET-Pi import feature.
// Hand-written per project convention (no codegen).

export interface ExternalMediaResponse {
  environment: string;
  containerized: boolean;
  mount_path: string;
  mount_present: boolean;
  guidance: ExternalMediaGuidance | null;
}

export interface ExternalMediaGuidance {
  environment: string;
  steps: string[];
}

export type SourceAccessState = 'native' | 'container-mount' | 'container-missing';

export interface StartImportRequest {
  mode: 'db-only';
  source_path: string;
  location?: string;
}

export interface StartImportResponse {
  job_id: string;
  status: 'started';
}

export type ImportPhase = 'validate' | 'dedup' | 'import';

export interface ImportProgress {
  total: number;
  processed: number;
  inserted: number;
  skipped: number;
  errors: number;
  phase: ImportPhase | 'done';
}

export interface ImportErrorEvent extends ImportProgress {
  message: string;
}

export interface CancelResponse {
  status: 'cancelling' | 'done';
}

export interface ImportStatusResponse {
  running: boolean;
  job_id?: string;
  status: 'idle' | 'running' | 'done';
  progress?: ImportProgress;
  error?: string;
}

export type WizardStep = 'source' | 'mode' | 'confirm' | 'progress' | 'done';
export type ImportMode = 'db-only' | 'db-audio';

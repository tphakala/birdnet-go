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
  mode: 'db-only' | 'db-audio';
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

// --- Candidate-driven source discovery types (native import) ---

export type CandidateKind = 'local' | 'removable' | 'network';

/** A single auto-detected or manually validated BirdNET-Pi database. */
export interface SourceCandidate {
  path: string;
  kind: CandidateKind;
  detection_count: number;
  latest_date: string;
  audio_dir_guess: string;
  size: number;
  valid: boolean;
  /** Empty string, 'permission_denied', 'invalid_schema', or 'open_failed'. */
  reason: string;
  owner_uid: number;
  owner_name: string;
}

export interface ImportGuidance {
  environment: string;
  steps: string[];
}

export interface ImportSourcesResponse {
  environment: string;
  containerized: boolean;
  run_as_user: string;
  run_as_uid: number;
  candidates: SourceCandidate[];
  guidance: ImportGuidance | null;
}

export interface ValidateSourceResponse {
  valid: boolean;
  /** Empty string, 'not_found', 'invalid_path', 'permission_denied', 'invalid_schema', or 'open_failed'. */
  reason: string;
  detection_count: number;
  latest_date: string;
  audio_dir_guess: string;
  owner_name: string;
}

export interface ElevateResponse {
  method: 'direct' | 'sudo' | 'fallback';
  job_id: string;
  status: string;
  fallback_commands: string[];
}

/** Derived state for the wizard source step. */
export type SourceStepState = 'candidates' | 'zero-candidates';

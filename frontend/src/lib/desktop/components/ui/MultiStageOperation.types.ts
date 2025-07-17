export interface Stage {
  id: string;
  title: string;
  description?: string;
  status: 'pending' | 'in_progress' | 'completed' | 'error' | 'skipped';
  error?: string;
  message?: string;
  progress?: number;
}

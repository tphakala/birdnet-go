export interface Column<T> {
  key: string;
  header: string;
  sortable?: boolean;
  width?: string;
  align?: 'left' | 'center' | 'right';
  className?: string;
  /** Custom render function for returning plain text or numbers for display */
  render?: (_item: T, _index: number) => string | number;
  /** Custom render function for returning HTML strings that will be rendered as HTML content */
  renderHtml?: (_item: T, _index: number) => string;
}

export type SortDirection = 'asc' | 'desc' | null;

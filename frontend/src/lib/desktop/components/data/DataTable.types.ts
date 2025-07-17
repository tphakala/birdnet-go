export interface Column<T> {
  key: string;
  header: string;
  sortable?: boolean;
  width?: string;
  align?: 'left' | 'center' | 'right';
  className?: string;
  render?: (_item: T, _index: number) => string | number;
  renderHtml?: (_item: T, _index: number) => string;
}

export type SortDirection = 'asc' | 'desc' | null;

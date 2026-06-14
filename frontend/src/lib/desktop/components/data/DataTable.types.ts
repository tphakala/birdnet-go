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
  /**
   * Value used for client-side sorting of this column. Strings are compared with
   * localeCompare, numbers by subtraction. Used by SortableDataTable, which sorts
   * internally; the controlled DataTable ignores this.
   */
  sortValue?: (_item: T) => string | number;
  /**
   * Sort direction applied the first time this column becomes the active sort
   * column (subsequent clicks toggle). Defaults to 'asc'. Used by SortableDataTable.
   */
  defaultDirection?: 'asc' | 'desc';
}

export type SortDirection = 'asc' | 'desc' | null;

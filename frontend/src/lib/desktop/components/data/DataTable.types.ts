/** Horizontal text alignment of a table column; unset means left. */
export type ColumnAlign = 'left' | 'center' | 'right';

/** Tailwind text-align class for a column alignment; unset means left. */
export function textAlignClass(align?: ColumnAlign): string {
  if (align === 'right') return 'text-right';
  if (align === 'center') return 'text-center';
  return 'text-left';
}

export interface Column<T> {
  key: string;
  header: string;
  sortable?: boolean;
  width?: string;
  align?: ColumnAlign;
  className?: string;
  /** Custom render function for returning plain text or numbers for display */
  render?: (_item: T, _index: number) => string | number;
  /**
   * Value used for client-side sorting of this column. Strings are compared with
   * localeCompare, numbers by subtraction. Return the SAME type for every row of a
   * given column: mixing string and number across rows falls back to string
   * comparison and yields undefined ordering. Used by SortableDataTable, which
   * sorts internally.
   * When omitted, SortableDataTable sorts by the raw `item[key]` value (numbers
   * numerically, dates by time, anything else as a string; an empty, null, NaN or
   * invalid-date value sorts as an empty string, first ascending and last
   * descending), so set it whenever the displayed value differs from the field.
   */
  sortValue?: (_item: T) => string | number;
  /**
   * Sort direction applied the first time this column becomes the active sort
   * column (subsequent clicks toggle). Defaults to 'asc'. Used by SortableDataTable.
   */
  defaultDirection?: 'asc' | 'desc';
}

<!--
  DatabaseIcon Component

  Purpose: Renders database type icons based on database type.
  This component provides a type-safe way to display database icons.

  Props:
  - database: The database type (sqlite, mysql)
  - className: Optional CSS classes for sizing/styling

  Note: Uses {@html} for SVG rendering - safe because icons are static build-time
  assets, not user-generated content.

  @component
-->
<script lang="ts">
  import type { HTMLAttributes } from 'svelte/elements';
  import { cn } from '$lib/utils/cn.js';

  // Import all database icons as raw SVG strings
  import SqliteIcon from '$lib/assets/icons/database/sqlite.svg?raw';
  import MysqlIcon from '$lib/assets/icons/database/mysql.svg?raw';

  // Database type definition
  export type DatabaseType = 'sqlite' | 'mysql';

  interface Props extends HTMLAttributes<HTMLElement> {
    database: DatabaseType;
    className?: string;
  }

  let { database, className = '', ...rest }: Props = $props();

  // Map database types to their SVG content
  const databaseIcons: Record<DatabaseType, string> = {
    sqlite: SqliteIcon,
    mysql: MysqlIcon,
  };

  // Runtime type guard to satisfy static analysis (object injection sink warning)
  const isDatabaseType = (v: unknown): v is DatabaseType =>
    typeof v === 'string' && v in databaseIcons;

  // Get the icon for the current database type with runtime validation
  // eslint-disable-next-line security/detect-object-injection -- Validated by isDatabaseType type guard
  let iconSvg = $derived(isDatabaseType(database) ? databaseIcons[database] : SqliteIcon);
</script>

<span
  class={cn('size-5 shrink-0 [&>svg]:size-full [&>svg]:block', className)}
  aria-hidden="true"
  {...rest}
>
  {@html iconSvg}
</span>

<!--
  DatabaseIcon Component

  Purpose: Renders database type icons based on database type.
  This component provides a type-safe way to display database icons
  without using raw HTML injection.

  Props:
  - database: The database type (sqlite, mysql)
  - className: Optional CSS classes for sizing/styling

  @component
-->
<script lang="ts">
  // Import all database icons as raw SVG strings
  import SqliteIcon from '$lib/assets/icons/database/sqlite.svg?raw';
  import MysqlIcon from '$lib/assets/icons/database/mysql.svg?raw';

  // Database type definition
  export type DatabaseType = 'sqlite' | 'mysql';

  interface Props {
    database: DatabaseType;
    className?: string;
  }

  let { database, className = 'size-5' }: Props = $props();

  // Map database types to their SVG content
  const databaseIcons: Record<DatabaseType, string> = {
    sqlite: SqliteIcon,
    mysql: MysqlIcon,
  };

  // Get the icon for the current database type
  let iconSvg = $derived(databaseIcons[database] || SqliteIcon);
</script>

<!--
  Note: We use {@html} here because the SVG icons are static assets
  imported at build time, not user-generated content. The icons are
  trusted and sanitized by the build process.
-->
<span class="{className} shrink-0 [&>svg]:size-full [&>svg]:block">
  {@html iconSvg}
</span>

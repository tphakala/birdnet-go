# Dashboard Edit Mode UX Redesign

**Date:** 2026-03-13
**Status:** Approved

## Problem

The current dashboard edit mode has several UX issues:
1. Floating "Edit Dashboard" button is too visually distracting
2. Eye/EyeOff toggle for element visibility is confusing — disabled elements shown dimmed instead of removed
3. No way to add new element types or restore removed ones
4. Save doesn't reflect immediately — settings store not updated after API patch (reactivity bug)

## Design

### 1. Edit Mode Entry Point

**Current:** Floating "Edit Dashboard" button (bottom-right), always visible to admins.

**New:** Dashboard becomes a collapsible sidebar group in `DesktopSidebar.svelte` (like Analytics, Settings, System) with two sub-items:
- **Dashboard** — navigates to dashboard page (same as current behavior)
- **Edit Dashboard** — navigates to dashboard page AND activates edit mode (admin-only, hidden from guests)

The `DashboardEditMode` component removes its floating edit button entirely. Edit mode is triggered via sidebar navigation (URL parameter `?edit=true` or a shared store flag).

### 2. Element Remove, Hide & Add Model

Each element in edit mode gets two actions in its toolbar:
- **Hide** (eye-off icon) — sets `enabled: false`. Element stays in config with all settings preserved. Hidden elements appear collapsed/dimmed in edit mode with an "unhide" action, but are invisible on the normal dashboard.
- **Delete** (X icon) — hard-removes the element from the `layout.elements` array. Config is lost.

**"+" button in the floating edit toolbar:**
- Opens a dropdown listing all element types NOT currently in the layout array (neither active nor hidden)
- Clicking a type adds a fresh instance with default config to the end of the array
- Available types: `banner`, `daily-summary`, `currently-hearing`, `detections-grid`, `video-embed`, `search`

**Visibility rules:**
- Normal mode: only `enabled: true` elements render
- Edit mode: all elements in the array render — enabled ones normally, hidden ones as collapsed/dimmed cards with label + unhide button

### 3. Search Bar as Dashboard Element

**Current:** Search bar is hardcoded at the top of the dashboard page, always visible.

**New:**
- Search bar becomes a dashboard element type `search` with its own entry in `layout.elements`
- Default layout includes it as the first element, `enabled: true`
- Can be hidden, deleted, or reordered like any other element
- No config modal needed — no configurable options
- Backend `DashboardElement` type gains `search` as a valid type

### 4. Save & Reactivity Fix

**Bug:** `DashboardEditMode.saveLayout()` calls `api.patch()` then `onLayoutChange()` which triggers `fetchDashboardConfig()`. That function updates local component variables (`summaryLimit`, `showThumbnails`) but never updates `settingsStore.formData.realtime.dashboard.layout` — the derived store source. Layout appears stale until page reload.

**Fix:** After successful `api.patch()`, update the settings store directly:
```typescript
settingsStore.formData.realtime.dashboard.layout = newLayout;
```

No extra fetch needed. The `fetchDashboardConfig()` call can be kept for non-layout fields.

## Changes Summary

| Area | Change |
|------|--------|
| `DesktopSidebar.svelte` | Dashboard becomes collapsible group with "Dashboard" + "Edit Dashboard" (admin-only) sub-items |
| `DashboardEditMode.svelte` | Remove floating edit button. Accept `editMode` as prop driven by sidebar/URL. Replace Eye/EyeOff with Hide + Delete. Add "+" button with dropdown in floating toolbar. |
| `DashboardElementWrapper.svelte` | Replace toggle with hide + delete buttons. Add collapsed/dimmed state for hidden elements with unhide action. |
| `DashboardPage.svelte` | Fix reactivity: update `settingsStore.formData` directly after save. Extract search bar into a dashboard element. Support edit mode activation via sidebar navigation. |
| `ElementConfigModal.svelte` | Add `search` type handling (no-config message). |
| Backend `config.go` | Add `search` as valid element type. |
| Backend `dashboard_migration.go` | Add `search` element to default layout migration. |
| Settings store | No structural changes — just write to it after save. |
| i18n (all 10 locales) | Add keys for new UI strings (delete, add, search label, unhide, etc.) |

## Not Changing

- Drag-and-drop reordering (stays as-is via svelte-dnd-action)
- Config modals for existing element types
- Backend API endpoint (`PATCH /api/v2/settings/dashboard`)
- Backend persistence model (settings store → YAML)

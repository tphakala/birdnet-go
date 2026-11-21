# Component Inventory

This folder contains **shared components** used across the application. Feature-specific components are located in their respective feature folders (e.g., `features/dashboard/components/`, `features/settings/components/`).

## Weather Components Guide

### WeatherDetails vs WeatherMetrics

- **WeatherDetails**: Use in modals, cards, and detailed views where vertical space is available
  - Vertical stacked layout
  - Always shows all icons (weather, temperature, wind)
  - Larger text and icon sizes
  - Supports loading and error states
  - Best for: ReviewModal, detail panels, dashboards

- **WeatherMetrics**: Use in table rows and compact layouts where horizontal space is limited
  - Horizontal two-line layout
  - Responsive hiding of elements based on container width
  - Compact sizing options
  - Icons can be toggled on/off via constants
  - Best for: DetectionRow, tables, lists

## Data

- `ConfidenceCircle.svelte` - Circular confidence indicator with progress ring
- `SortableDataTable.svelte` - Card-wrapped table with built-in client-side sorting and search, header bar (icon/title/count/search/actions), and loading/empty/no-results states; composes SortableHeader, ResizableContainer, EmptyState, and LoadingSpinner
  - Loading: a spinner with a visible label (`loadingLabel`, default "Loading") inside a `role="status"` region that stays in the document, so the label is announced when loading starts
  - Body: resizable box by default; with `resizable={false}`, `bodyMaxHeight` (a CSS length such as `min(70vh, 40rem)`) caps the body so long lists scroll under the sticky header
  - A body with no sortable column is a tab stop so the arrow keys can scroll it; with a `title` it is also a region named by it
  - `notice` snippet renders between the header bar and the body in every state (an `ErrorAlert` above rows that stay visible); `empty` snippet replaces the default `EmptyState`
  - Columns with `align: 'right'` right-align header and cells and use tabular figures; a `sortable` column without `sortValue` sorts by its raw field value
  - `SortableHeader.svelte` (in `ui/`) is the sortable `<th>` it uses; standalone tables can use it too. The button is named by the column label, `aria-sort` is set on the active column only, and the button's hit area and focus ring cover the whole header cell. Its `align` prop puts the indicator before the label for right-aligned columns
- `StatsCard.svelte` - Statistical information card
- `StatusBadges.svelte` - Status indicators (verified, locked, etc.)
- `WeatherDetails.svelte` - Detailed weather display for modals (vertical layout, icons always visible)
  ```svelte
  <WeatherDetails
    weatherIcon={detection.weather.weatherIcon}
    weatherDescription={detection.weather.description}
    temperature={detection.weather.temperature}
    windSpeed={detection.weather.windSpeed}
    windGust={detection.weather.windGust}
    units={detection.weather.units}
    size="lg"
    loading={isLoadingWeather}
    error={weatherError}
  />
  ```
- `WeatherIcon.svelte` - Weather condition icons
- `WeatherInfo.svelte` - Weather information display
- `WeatherMetrics.svelte` - Compact weather display for tables (responsive horizontal layout)
  ```svelte
  <WeatherMetrics
    weatherIcon={detection.weather.weatherIcon}
    weatherDescription={detection.weather.description}
    temperature={detection.weather.temperature}
    windSpeed={detection.weather.windSpeed}
    windGust={detection.weather.windGust}
    units={detection.weather.units}
    size="sm"
  />
  ```

## Forms

- `Checkbox.svelte` - Checkbox input with label, for acknowledgements, row selection and multi-select lists (boolean settings use `ToggleField`) (`labelClassName` replaces the label's default `py-1`; its base classes `flex items-center justify-start cursor-pointer` always stay and are not merged with conflicting utilities)
- `DateRangePicker.svelte` - Date range selection
- `FormField.svelte` - Form field wrapper with validation
- `InlineSlider.svelte` - Inline slider input for compact layouts
- `LocationMap.svelte` - Map for picking a station location (click or drag the pin, coordinates rounded to 3 decimals), with zoom buttons, Ctrl/Cmd+wheel zoom and an expand button opening a full screen map in a dialog portalled into the surrounding dialog or the page body. `onLocationChange` fires only for a click, a pin drag or a chosen searched place, never for prop changes. Required props: `latitude`, `longitude`, `locationSet` (pin shown), `onLocationChange`, `title` (expanded dialog heading). Optional props (defaults reproduce the settings page map): `ready=true` (build the map once true), `mapClass='h-[350px]'`, `controls='below'|'overlay'` (button placement), `pinchZoom=false` (two-finger zoom, rotation stays off), `doubleTapZoomKeepsPin=false` (a pick waits 300 ms and is dropped when a double click, double tap or zoom gesture follows), `startView='region'|'world'` (the start zoom is chosen when the inline map is created and depends on whether the coordinates are set (not 0,0): 11 when set, else 5 for `region` or 1 for `world`; the expanded map starts at the inline map's current zoom; `world` also raises the zoom to at least 11 when a first location arrives through the `latitude` and `longitude` props with `locationSet` true and no pin yet, a click does not), `placeSearch=false` (show `PlaceSearch` above the map and in the expanded dialog; choosing a place reports its rounded coordinates through `onLocationChange` like a click and flies both maps there at zoom 11), `className`. Map logic lives in `locationMapController.ts`
- `ModelToggleList.svelte` - Classifier switches of an audio source: one `ToggleField` per listed model with its live backend and precision, load state and sample rate badges; a selected alias the server no longer lists shows as a missing row with Remove; an empty selection follows the default models; the last selected model stays on with its reason; skeleton, fetch-failed (Retry) and no-model states. `onChange(models)` receives the complete new list
- `NetworkAddressListInput.svelte` - Generic list editor for IP addresses and CIDR ranges, rows with remove buttons. Props: `values`, `onUpdate(values)`, `accept` (`'ip'`, `'cidr'` or `'ip-or-cidr'`, validated as Go's `net.ParseIP` and `net.ParseCIDR` read them), `label`, optional `id`, `helpText`, `placeholder`, `emptyStateMessage`, `disabled`, `maxItems`, `rowAnnotation` (snippet receiving `{ value, index, valid }`, for example a badge) and a bindable `draft` (text typed but not added; bind it to keep it across a remount and to block saving while it is not empty). Add takes several values separated by commas, spaces or line breaks; valid ones are added, duplicates (same network) are dropped and reported, invalid ones stay in the input with a message. Values passed in are never changed by rendering: invalid and duplicated rows are flagged until removed. No store access
- `NumberField.svelte` - Number input field
- `PasswordField.svelte` - Password input with show/hide
- `PlaceSearch.svelte` - Place search combobox (WAI-ARIA combobox with a listbox popup) over Photon (photon.komoot.io, OpenStreetMap data), called from the browser by `$lib/utils/placeSearch.ts`. Searches while typing after 3 characters and on Enter or the search button for any query; the request carries only the query and, for de/en/fr, `lang`, never coordinates, cookies or a referrer. A superseded or late response is dropped. Shows where searches go and a clear inline message (error icon and colour for a failure, announced in an always-rendered `role="alert"` region) when the service is unreachable. Each result shows a translated kind label (city, town, village, station, airport) when Photon says what it is, so look-alike results can be told apart. Colours come from `PlaceSearch.styles.ts` and the shared option highlight in `SelectDropdown.styles.ts`, and are checked in `src/test/color-contrast.test.ts`. Props: `onSelect(place)`, `disabled=false` (disables the input and button, drops the results and cancels a pending or running search), `className`. Used through `LocationMap`'s `placeSearch` prop
- `RTSPUrlInput.svelte` - RTSP URL input with validation
- `RTSPUrlManager.svelte` - Manage multiple RTSP URLs
- `SampleRateBadge.svelte` - Whether a source's capture rate suits a model: an error badge naming the minimum rate when the source is below it, a warning badge naming the recommended rate when it is below that, nothing otherwise
- `SelectDropdown.svelte` - Dropdown selection component
- `SelectField.svelte` - Select field wrapper
- `SliderField.svelte` - Slider input field
- `SpeciesInput.svelte` - Species selection input
- `SoundCardCard.svelte` - One sound card source whose settings go into the settings form (the page's Save Changes bar persists them), composed from `SourceCard` and `SettingsGroup` (`features/settings/`): a header (name, device, badges, a "needs attention" badge while the page's save rules report a problem, delete) that opens and closes the body, and body groups for name (written as typed, trimmed when the field is left, inline message from `problem`), gain, capture (a device pick waits for the sample rate probe of that device and is written together with a rate it supports; opening the card only probes), classifiers (`ModelToggleList`), equalizer and quiet hours. Controlled: `expanded` and `onToggleExpanded`; every change goes out through `onChange(source)` with the whole source
- `SourceModelBadges.svelte` - Classifier badges of a source card header: a warning badge when a selected model is missing or not loaded, and a summary of the selected or default models. Used by `SoundCardCard` and `StreamCard`
- `SoundCardManager.svelte` - Manage the sound card sources of a tab that saves with the page: keeps a UI-only row id per source (cards, open state), opens a single source by default, and gives every row a fresh id when it receives a list with another name and device sequence than the one it sent (Reset, a reload); add appends to the form and closes the form at once, delete asks first; `onUpdateSources(sources)` receives the whole array, `savedSources` lets only changed problems be reported
- `StreamCard.svelte` - One audio stream whose settings go into the settings form (the page's Save Changes bar persists them), composed from the same shared pieces as `SoundCardCard`: a header (status pill, masked URL, badges, a "needs attention" badge while the page's save rules report a problem, an inline enabled `ToggleField`, delete) with the live stats and warnings below it, and body groups for name and URL (written as typed, trimmed when the field is left; inline messages from `problem`, an untested URL says so at once), gain, connection (type, transport, media mode and channel mode, each writing only its own key, and a test button whose result carries the URL it tested), classifiers (`ModelToggleList` with `isStream`), equalizer, quiet hours and diagnostics. Controlled: `expanded` and `onToggleExpanded`; every change goes out through `onChange(stream)` with the whole stream, a successful test through `onUrlTested(url)`
- `StreamChannelControls.svelte` - Adaptive channel handling UI (format display, downmix/left/right selector, stereo energy analysis) shared by the stream add and edit forms
- `StreamManager.svelte` - Manage the streams of a tab that saves with the page: live health over SSE shown for the saved streams only (`savedStreams`), on the card that still has its saved URL (`healthUrl`; an edited or copied URL shows none), a UI-only row id per stream (cards, open state) that a list with another name and URL sequence than the one the manager sent (Reset, a reload) replaces, one stream open by default, add (tested first; every successful connection test, of a card or of the add form, is reported through `onUrlTested`, and Add accepts a URL that already passed a test in this page session) appends to the form and closes the form at once, delete asks first; `onUpdateStreams(streams)` receives the whole array, `testedUrls` and `savedStreams` let only changed problems be reported
- `TextInput.svelte` - Text input field; `error` shows an alert and marks the input invalid, `aria-required` marks it required for assistive technology without the native `required` validation, `reserveErrorSpace` keeps two lines for that alert so nothing below moves
- `ToggleField.svelte` - Toggle/switch field; `variant="card"` draws a bordered card that toggles as a whole (icon turns primary while on); `variant="inline"` is a compact label-then-switch control for toolbars and table header bars, where the label text toggles too; its hit area reaches 0.375rem above and below the label, so keep that space free of other controls; `disabled` keeps the switch focusable (`aria-disabled`) in every variant, so a screen reader can still read why it is off; the default variant takes an optional `badges` snippet shown on the label line, outside the accessible name

## Media

- `AudioPlayer.svelte` - Audio playback controls with spectrogram
- `SpectrogramPlayer.svelte` - Compact spectrogram player for table rows (play overlay + progress bar)
- `SpectrogramImage.svelte` - Image-only spectrogram for detections whose audio was removed by retention (no player, no download)

## Modals

- `ConfirmModal.svelte` - Confirmation dialog
- `KeyboardShortcutsModal.svelte` - Keyboard shortcut cheat sheet, mounted once in the root layout; lists the current page's shortcuts, then the ones for all pages, under the switch that turns shortcuts off, and registers the `?` key that opens it
- `LoginModal.svelte` - User login modal
- `ReviewModal.svelte` - Detection review modal
- `SpeciesBadges.svelte` - Reusable species status and lock badges for modals
- `SpeciesThumbnail.svelte` - Reusable species thumbnail image component

## UI

- `ActionMenu.svelte` - Dropdown action menu
- `AudioLevelIndicator.svelte` - Audio level visualization
- `Badge.svelte` - Status/count badges
- `Button.svelte` - Reusable button with variant (default, primary, success, warning, error, ghost) and size (xs, sm, md, lg) support, a bindable `ref` to the `<button>`, aria-disabled styling that keeps the button focusable, and a shared keyboard focus ring
- `Card.svelte` - Generic card container; `variant="panel"` is the dense bordered panel for data views, with `headerActions` and `bodyClassName`
- `CollapsibleCard.svelte` - Collapsible card container
- `CollapsibleSection.svelte` - Collapsible content section
- `DatePicker.svelte` - Date picker input
- `EmptyState.svelte` - Empty state display
- `ErrorAlert.svelte` - Error message display
- `ExpandableText.svelte` - Text clamped to a few lines with a Show more / Show less toggle that appears only when the text is cut off
- `Input.svelte` - Generic input field
- `Kbd.svelte` - Key caps for a keyboard shortcut (one key or a combination); the caps are hidden from assistive technology and an optional `label` gives the spoken form. `size="md"` for shortcut lists, `size="sm"` for badges inside buttons and menu items, which take the control's text colour
- `LanguageSelector.svelte` - Language selection dropdown
- `LoadingSpinner.svelte` - Loading animation
- `Meter.svelte` - Static measurement bar (disk, CPU, table size) on the `--viz-*` tokens
- `Modal.svelte` - Generic modal container
- `MultiStageOperation.svelte` - Multi-step operation UI
- `NotificationBell.svelte` - Notification bell icon
- `NotificationToast.svelte` - Toast notification
- `Pagination.svelte` - Pagination controls
- `ProgressBar.svelte` - Progress bar indicator for running tasks
- `RadioCardGroup.svelte` - Card-style radio group with one Tab stop and arrow-key selection (WAI-ARIA radio pattern)
- `SearchBox.svelte` - Search input box
- `Select.svelte` - Select dropdown
- `SelectionToolbar.svelte` - Sticky toolbar for multiselect list views (selection count, select-all banner, action slot)
- `Skeleton.svelte` - One decorative placeholder bar of a loading skeleton
- `SkeletonDailySummary.svelte` - Loading skeleton for daily summary cards
- `SkeletonStatus.svelte` - The one visible `role="status"` loading label of a skeleton
- `StatusPill.svelte` - Status pill indicator
- `TestSuccessNote.svelte` - Test success notification component
- `ThemeToggle.svelte` - Theme switching toggle
- `TimeOfDayIcon.svelte` - Time-based icons (day/night)
- `ToastContainer.svelte` - Container for toast notifications

## Type Files

- `DataTable.types.ts` - Data table type definitions
- `MultiStageOperation.types.ts` - Multi-stage operation types
- `SelectDropdown.types.ts` - Select dropdown types

## Utility Files

- `hls-config.ts` - HLS video streaming configuration
- `image-utils.ts` - Image utility functions

## Test Files

Each component has corresponding `.test.ts` or `.test.svelte` files for unit testing.

## Feature-Specific Components

The following components are located in their feature directories:

### Dashboard (`features/dashboard/components/`)

- `AcousticModelBanner.svelte` - No-model banner (none installed / load failed) fed by the acoustic model store
- `LegacyDatabaseBanner.svelte` - Session-dismissible banner prompting legacy-schema installs to run the database migration
- `DailySummaryCard.svelte` - Daily species summary with hourly heatmap
- `DetectionCardGrid.svelte` - Card grid view of recent detections
- `DetectionCard.svelte` - Individual detection card with spectrogram background
- `PlayOverlay.svelte` - Audio play button overlay for detection cards
- `ConfidenceBadge.svelte` - Confidence level badge display
- `WeatherBadge.svelte` - Weather condition badge display
- `SpeciesInfoBar.svelte` - Species information bar with thumbnail
- `BirdThumbnailPopup.svelte` - Hover popup showing larger bird image

### Detections (`features/detections/components/`)

- `DetectionRow.svelte` - Single detection row for lists
- `DetectionsList.svelte` - List of detection rows
- `DetectionsCard.svelte` - Detection card component

### Analytics (`features/analytics/components/`)

- `FilterForm.svelte` - Generic filtering form
- `SpeciesFilterForm.svelte` - Species-specific filtering
- `SpeciesCard.svelte` - Species information card
- `StatCard.svelte` - Statistics card

### Settings (`features/settings/components/`)

- `AutoSaveStatus.svelte` - Inline feedback of one immediately saved setting (no tab uses it now), driven by the field key given to `commitSetting` (`$lib/stores/settingsAutoSave.svelte`): a persistent visually hidden `role="status"` region announces "Saving...", "Saved" and the failure reason; the visible text shows the same, and the reason comes with a dismiss button (focus moves to the component's wrapper on dismissal). Props: `field`, optional `id` (of the visible error message), `class`
- `SettingsGroup.svelte` - One group of settings (the `field` and `statusId` props serve tabs that save as they change): a title row (a heading, a `<label>` with `labelFor`, or a `heading` snippet such as a disclosure button) with the group's `AutoSaveStatus` on the right, then the controls. Props: `title`, `field`, `statusId`, `headingId`, `labelFor`, `required`, `heading`, `class`, `titleVariant` (`'group'` shows a heading title as a group label), `children`
- `SettingsGroupLabel.svelte` - Small uppercase muted label for a subgroup of fields inside a settings card, on a chosen element. Props: `as` (`h2` to `h6`, `div` or `span`; default `h4`), `divided` (hairline and spacing above), `class` (spacing and layout only), `children`; other attributes (`id`, `aria-*`) go to the element. Exports `SETTINGS_GROUP_LABEL_CLASSES` for elements it cannot render, and `SETTINGS_GROUP_DISCLOSURE_CLASSES` for a disclosure button that names a group
- `CommitTextField.svelte` - Text setting that commits on blur or Enter and restores on Escape; `validate(value)` returns the reason a trimmed value is refused, shown at the field with `aria-invalid`. Props: `id`, `value` (saved), `validate`, `onCommit`, `placeholder`, `maxlength`, `required`, `disabled`, `describedBy`
- `ScopedApplyGroup.svelte` - Controls whose change restarts something, saved with a scoped Apply: renders the controls and, while the draft differs or an apply runs, a row with the hint, Cancel and the Apply button named for its effect (busy label while saving, failure hint after a failed apply, draft kept). Driven by an `ApplyDraft` (`applyDraft.svelte.ts`: the draft follows the saved values until edited; `set`, `cancel`, `apply(commit)`, `dirty`, `applying`, `failed`, `visible`). A blocked apply (`disabled`) shows the hint in place of the failure hint and describes the Apply button by it, so the caller's hint should name the reason. Props: `draft`, `onApply`, `hint`, `failedHint`, `applyLabel`, `applyingLabel`, `disabled`, `statusId`, `children`
- `SourceCard.svelte` - Shell of one audio source (sound card, stream): header with a disclosure button (icon, name, subtitle; `aria-expanded`, `aria-controls`), a `badges` snippet, an `actions` snippet before delete (an enable switch) and delete with a confirm overlay; an optional `nameAddon` after the name (a status pill), `details` lines under the header that stay visible (stats, warnings) and `iconClass` for the icon tile colours; the body shows while `expanded`. Props: `name`, `subtitle`, `icon`, `iconClass`, `expanded`, `onToggleExpanded`, `deleteConfirm`, `onDelete`, `disabled`, `nameAddon`, `badges`, `actions`, `details`, `children`
- `ModelCard.svelte` - Model gallery card (installed or available) whose regions line up across a row through subgrid
- `ModelCardGrid.svelte` - Model gallery grid with columns sized by its own width
- `SettingsCard.svelte` - Settings display card
- `SettingsSection.svelte` - Settings section wrapper

### Layouts (`layouts/`)

- `RootLayout.svelte` - Root layout wrapper
- `DesktopSidebar.svelte` - Navigation sidebar

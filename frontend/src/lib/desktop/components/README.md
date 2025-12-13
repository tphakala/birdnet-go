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
- `DataTable.svelte` - Generic data table with sorting and pagination
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

- `Checkbox.svelte` - Checkbox input with label
- `DateRangePicker.svelte` - Date range selection
- `FormField.svelte` - Form field wrapper with validation
- `InlineSlider.svelte` - Inline slider input for compact layouts
- `NumberField.svelte` - Number input field
- `PasswordField.svelte` - Password input with show/hide
- `RTSPUrlInput.svelte` - RTSP URL input with validation
- `RTSPUrlManager.svelte` - Manage multiple RTSP URLs
- `SelectDropdown.svelte` - Dropdown selection component
- `SelectField.svelte` - Select field wrapper
- `SliderField.svelte` - Slider input field
- `SpeciesInput.svelte` - Species selection input
- `SpeciesManager.svelte` - Species management interface
- `StreamCard.svelte` - Individual stream card for stream management
- `StreamManager.svelte` - Manage multiple video/audio streams
- `SubnetInput.svelte` - Subnet input with validation
- `TextInput.svelte` - Text input field
- `ToggleField.svelte` - Toggle/switch field

## Media

- `AudioPlayer.svelte` - Audio playback controls with spectrogram

## Modals

- `ConfirmModal.svelte` - Confirmation dialog
- `LoginModal.svelte` - User login modal
- `ReviewModal.svelte` - Detection review modal
- `SpeciesBadges.svelte` - Reusable species status and lock badges for modals
- `SpeciesThumbnail.svelte` - Reusable species thumbnail image component

## Review

- `ReviewCard.svelte` - Detection review card component

## UI

- `ActionMenu.svelte` - Dropdown action menu
- `AudioLevelIndicator.svelte` - Audio level visualization
- `Badge.svelte` - Status/count badges
- `Card.svelte` - Generic card container
- `CollapsibleCard.svelte` - Collapsible card container
- `CollapsibleSection.svelte` - Collapsible content section
- `DatePicker.svelte` - Date picker input
- `EmptyState.svelte` - Empty state display
- `ErrorAlert.svelte` - Error message display
- `Input.svelte` - Generic input field
- `LanguageSelector.svelte` - Language selection dropdown
- `LoadingSpinner.svelte` - Loading animation
- `Modal.svelte` - Generic modal container
- `MultiStageOperation.svelte` - Multi-step operation UI
- `NotificationBell.svelte` - Notification bell icon
- `NotificationToast.svelte` - Toast notification
- `Pagination.svelte` - Pagination controls
- `ProcessTable.svelte` - Process status table
- `ProgressBar.svelte` - Progress bar indicator
- `ProgressCard.svelte` - Progress display card
- `SearchBox.svelte` - Search input box
- `Select.svelte` - Select dropdown
- `SkeletonDailySummary.svelte` - Loading skeleton for daily summary cards
- `StatusPill.svelte` - Status pill indicator
- `SystemInfoCard.svelte` - System information display
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

- `DailySummaryCard.svelte` - Daily species summary with hourly heatmap
- `DetectionCardGrid.svelte` - Card grid view of recent detections
- `DetectionCard.svelte` - Individual detection card with spectrogram background
- `CardActionMenu.svelte` - Dropdown action menu for detection cards
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
- `ChartCard.svelte` - Card wrapper for charts
- `SpeciesCard.svelte` - Species information card
- `StatCard.svelte` - Statistics card

### Settings (`features/settings/components/`)

- `SettingsCard.svelte` - Settings display card
- `SettingsSection.svelte` - Settings section wrapper

### Layouts (`layouts/`)

- `RootLayout.svelte` - Root layout wrapper
- `DesktopSidebar.svelte` - Navigation sidebar

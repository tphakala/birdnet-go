# Component Inventory

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

## Charts

- `ChartWrapper.svelte` - Wrapper for chart components with common styling

## Data

- `ConfidenceCircle.svelte` - Circular confidence indicator with progress ring
- `DailySummaryCard.svelte` - Daily species summary with hourly heatmap
- `DataTable.svelte` - Generic data table with sorting and pagination
- `DetectionRow.svelte` - Single detection row for lists
- `DetectionsList.svelte` - List of detection rows
- `DetectionCardGrid.svelte` - Card grid view of recent detections (in features/dashboard/components/)
- `RecentDetectionsTable.svelte` - Table view of recent detections
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
- `FilterForm.svelte` - Generic filtering form
- `FormField.svelte` - Form field wrapper with validation
- `NumberField.svelte` - Number input field
- `PasswordField.svelte` - Password input with show/hide
- `RTSPUrlInput.svelte` - RTSP URL input with validation
- `RTSPUrlManager.svelte` - Manage multiple RTSP URLs
- `SelectDropdown.svelte` - Dropdown selection component
- `SelectField.svelte` - Select field wrapper
- `SliderField.svelte` - Slider input field
- `SpeciesFilterForm.svelte` - Species-specific filtering
- `SpeciesInput.svelte` - Species selection input
- `SpeciesManager.svelte` - Species management interface
- `SubnetInput.svelte` - Subnet input with validation
- `TextInput.svelte` - Text input field
- `ToggleField.svelte` - Toggle/switch field

## Layout

- `Header.svelte` - Application header
- `RootLayout.svelte` - Root layout wrapper
- `Sidebar.svelte` - Navigation sidebar

## Media

- `AudioPlayer.svelte` - Audio playback controls with spectrogram

## Modals

- `ConfirmModal.svelte` - Confirmation dialog
- `ReviewModal.svelte` - Detection review modal
- `SpeciesBadges.svelte` - Reusable species status and lock badges for modals
- `SpeciesThumbnail.svelte` - Reusable species thumbnail image component

## UI

- `ActionMenu.svelte` - Dropdown action menu
- `AudioLevelIndicator.svelte` - Audio level visualization
- `Badge.svelte` - Status/count badges
- `Card.svelte` - Generic card container
- `ChartCard.svelte` - Card wrapper for charts
- `CollapsibleCard.svelte` - Collapsible card container
- `CollapsibleSection.svelte` - Collapsible content section
- `DatePicker.svelte` - Date picker input
- `EmptyState.svelte` - Empty state display
- `ErrorAlert.svelte` - Error message display
- `Input.svelte` - Generic input field
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
- `SettingsCard.svelte` - Settings display card
- `SettingsSection.svelte` - Settings section wrapper
- `SpeciesCard.svelte` - Species information card
- `StatCard.svelte` - Statistics card
- `SystemInfoCard.svelte` - System information display
- `ThemeToggle.svelte` - Theme switching toggle
- `TimeOfDayIcon.svelte` - Time-based icons (day/night)

## Test Files

Each component has corresponding `.test.ts` or `.test.svelte` files for unit testing.

## Type Files

- `DataTable.types.ts` - Data table type definitions
- `MultiStageOperation.types.ts` - Multi-stage operation types
- `SelectDropdown.types.ts` - Select dropdown types

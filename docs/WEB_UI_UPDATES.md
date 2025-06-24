# Web UI Updates for Telemetry and Support

This document describes the updates made to the BirdNET-Go web interface to support the new package structure and Sentry telemetry features.

## Changes Overview

### 1. Package Naming Updates

#### Integration Settings Page (`views/pages/settings/integrationSettings.html`)
- **Renamed "Telemetry" to "Observability"**: Updated the Prometheus metrics section to reflect the new package naming
- **Updated all references**: Changed variable names, IDs, and labels from `telemetry` to `observability`
- **Improved descriptions**: Enhanced tooltips to clarify that this is for Prometheus metrics monitoring

**Key Changes:**
- Section title: "Telemetry" → "Observability"
- Variable names: `telemetry.*` → `observability.*`
- Element IDs: `telemetryEnabled` → `observabilityEnabled`
- Enhanced tooltip descriptions for clarity

### 2. New Support Settings Page

#### Created Support Page (`views/pages/settings/supportSettings.html`)
A comprehensive new settings page dedicated to support and error tracking features:

**Features:**
- **Privacy-First Design**: Clear privacy notice explaining data collection practices
- **Sentry Integration**: Complete Sentry configuration interface with opt-in design
- **Setup Instructions**: Step-by-step guide for Sentry configuration
- **Data Transparency**: Explicit explanation of what data is and isn't collected
- **Future-Ready**: Placeholder section for local support tools

**Sentry Configuration Options:**
- Enable/Disable toggle (opt-in required)
- Sentry DSN configuration
- Sample rate adjustment (0.0 to 1.0)
- Debug mode toggle
- Advanced settings section

**Privacy Compliance:**
- Prominent privacy notice
- Clear explanation of data collection practices
- GDPR-compliant opt-in design
- Transparent about what data is NOT collected

### 3. Navigation Updates

#### Settings Navigation (`views/elements/sidebar.html`)
- **Added Support menu item**: New "Support" link in the settings submenu
- **Proper positioning**: Added as the last item in the settings navigation
- **Consistent styling**: Follows the same pattern as other settings menu items

#### Route Configuration (`internal/httpcontroller/htmx_routes.go`)
- **Added Support route**: New `/settings/support` route configuration
- **Authorization required**: Support page requires authentication like other settings pages
- **Proper integration**: Uses the same `settingsBase` template system

## Technical Implementation

### Template Structure
The new Support page follows the established BirdNET-Go template patterns:

```html
{{define "supportSettings"}}
  <!-- Hidden template name input -->
  <input type="hidden" name="templateName" value="{{.TemplateName}}">
  
  <!-- Sentry settings section with Alpine.js data binding -->
  <div x-data="{ sentry: { ... } }">
    <!-- Privacy notice -->
    <!-- Configuration fields -->
    <!-- Setup instructions -->
  </div>
  
  <!-- Future features placeholder -->
{{end}}
```

### Data Binding
- **Alpine.js integration**: Uses Alpine.js for reactive data binding
- **Settings mapping**: Maps to `{{.Settings.Sentry.*}}` configuration values
- **Form submission**: Integrates with existing settings save functionality
- **Change tracking**: Implements change detection for unsaved modifications

### Accessibility
- **ARIA labels**: Proper accessibility labels and roles
- **Semantic HTML**: Uses appropriate HTML5 semantic elements
- **Keyboard navigation**: Fully keyboard accessible
- **Screen reader support**: Proper heading structure and descriptions

## Configuration Integration

### Settings Structure
The Support page integrates with the existing settings configuration:

```yaml
sentry:
  enabled: false          # Opt-in required
  dsn: ""                 # Sentry project DSN
  samplerate: 1.0         # Error sampling rate
  debug: false            # Debug mode
```

### Form Field Mapping
- `sentry.enabled` → Enable Error Tracking checkbox
- `sentry.dsn` → Sentry DSN password field
- `sentry.samplerate` → Sample Rate number field (0.0-1.0)
- `sentry.debug` → Debug Mode checkbox

## User Experience

### Setup Flow
1. User navigates to Settings → Support
2. Reads privacy notice and data collection explanation
3. Optionally enables error tracking (opt-in)
4. Configures Sentry DSN if enabled
5. Adjusts advanced settings if needed
6. Saves configuration

### Privacy-First Approach
- Error tracking is **disabled by default**
- Requires **explicit user consent**
- Clear explanation of **what data is collected**
- Transparent about **what data is NOT collected**
- Compliant with **GDPR and privacy regulations**

## Future Enhancements

The Support page includes a placeholder section for future local support tools:

- **Local log collection**: Tools to gather system logs without external transmission
- **Diagnostic utilities**: System health checks and troubleshooting tools
- **Export functionality**: Ability to export logs and diagnostics for manual review
- **Privacy-focused tools**: All future tools will maintain the same privacy-first approach

## Benefits

### For Users
- **Centralized support**: All support-related features in one location
- **Privacy control**: Complete control over data sharing and telemetry
- **Transparency**: Clear understanding of what data is collected and why
- **Optional enhancement**: Error tracking is completely optional

### For Developers
- **Better debugging**: Access to error patterns and RTSP stream issues
- **Reliability insights**: Understanding of common failure modes
- **Performance monitoring**: Ability to identify and fix system bottlenecks
- **User privacy**: Maintained trust through transparent data practices

## Compatibility

### Browser Support
- **Modern browsers**: Full support for all modern browsers
- **Progressive enhancement**: Graceful degradation for older browsers
- **Mobile responsive**: Optimized for mobile and tablet interfaces
- **Touch-friendly**: Appropriate touch targets and interactions

### Existing Integration
- **No breaking changes**: Maintains compatibility with existing settings
- **Seamless integration**: Uses established template and routing patterns
- **Consistent UX**: Follows existing design language and interaction patterns
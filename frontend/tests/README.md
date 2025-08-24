# E2E Testing with Playwright

## Overview

This directory contains end-to-end (E2E) tests for BirdNET-Go using Playwright. These tests validate the complete user journey from frontend to backend, covering critical functionality, cross-browser compatibility, and real-world scenarios.

## Directory Structure

```
tests/
├── e2e/                    # E2E test suites
│   ├── auth/              # Authentication flow tests
│   ├── dashboard/         # Main dashboard interactions
│   ├── detections/        # Bird detection workflows
│   ├── settings/          # Configuration management
│   ├── real-time/         # SSE, WebSocket, streaming
│   ├── mobile/            # Responsive design tests
│   └── performance/       # Load time, memory usage
├── fixtures/              # Test data and utilities
└── support/               # Helper functions and Page Objects
    └── pages/             # Page Object Models
```

## Quick Start

### Install Dependencies

```bash
# From project root
task e2e-install

# Or from frontend directory
npm install -D @playwright/test playwright
npx playwright install
```

### Run Tests

```bash
# From project root using Task
task e2e-test                    # Run all E2E tests
task e2e-test-headed            # Run with browser visible
task e2e-test-ui                # Interactive UI mode
task e2e-report                 # View test report

# From frontend directory using npm
npm run test:e2e                # Run all tests
npm run test:e2e:headed         # Headed mode
npm run test:e2e:ui             # UI mode
npm run test:e2e:debug          # Debug mode
npm run test:e2e:report         # View report
```

### Target Specific Tests

```bash
# Mobile/responsive tests only
task e2e-test-mobile

# Performance tests only
task e2e-test-performance

# Specific test file
npx playwright test tests/e2e/dashboard/smoke.spec.ts
```

## Test Configuration

The tests are configured via `playwright.config.ts`:

- **Base URL**: `http://localhost:8080/ui`
- **Browsers**: Chrome, Firefox, Safari, Mobile devices
- **Timeouts**: 30s test timeout, 10s expect timeout
- **Retries**: 2x in CI, 0x locally
- **Web Server**: Automatically starts `task dev_server`

## Writing Tests

### Basic Test Structure

```typescript
import { test, expect } from '@playwright/test';

test.describe('Feature Tests', () => {
  test('should perform action', async ({ page }) => {
    await page.goto('/feature');
    await expect(page.locator('[data-testid="element"]')).toBeVisible();
  });
});
```

### Using Page Objects

```typescript
import { DashboardPage } from '../../support/pages/dashboard.page';

test('Dashboard interaction', async ({ page }) => {
  const dashboard = new DashboardPage(page);
  await dashboard.navigate();
  await dashboard.waitForDetection('American Robin');
});
```

### Test Data Management

```typescript
import { TestDataManager } from '../../fixtures/test-data';

test.beforeEach(async () => {
  await TestDataManager.cleanup();
  await TestDataManager.generateDetections(5);
});
```

## Best Practices

### Selectors

- Use `data-testid` attributes for reliable element selection
- Avoid fragile CSS selectors that may change
- Use semantic locators when possible (`role`, `aria-label`)

### Waiting Strategies

```typescript
// Wait for element to be visible
await expect(page.locator('[data-testid="result"]')).toBeVisible();

// Wait for API response
await page.waitForResponse('**/api/v2/detections');

// Wait for network idle
await page.waitForLoadState('networkidle');
```

### Error Handling

```typescript
// Check for error states
await expect(page.locator('[role="alert"]')).not.toBeVisible();

// Verify successful operations
const response = await page.waitForResponse('**/api/v2/settings');
expect(response.ok()).toBeTruthy();
```

## Debugging Tests

### Visual Debugging

```bash
# Run with browser visible
npm run test:e2e:headed

# Interactive mode with timeline
npm run test:e2e:ui

# Step-by-step debugging
npm run test:e2e:debug
```

### Test Artifacts

- Screenshots on failure: `test-results/`
- Videos on failure: `test-results/`
- Traces on failure: `test-results/`
- HTML reports: `playwright-report/`

### Common Issues

1. **Server not ready**: Tests wait for server health check
2. **Timing issues**: Use proper waits instead of `setTimeout`
3. **Flaky tests**: Add retry logic and better assertions
4. **Authentication**: Use setup scripts for login state

## CI/CD Integration

Tests run automatically in GitHub Actions:

- On pull requests to main branch
- On pushes to main branch
- Artifacts uploaded on failure (screenshots, videos, reports)

## Test Categories

### Smoke Tests

Basic functionality verification:

- Dashboard loads
- API health checks
- Navigation works

### Integration Tests

Complete user workflows:

- Detection monitoring
- Settings persistence
- Real-time updates

### Cross-browser Tests

Compatibility validation:

- Chrome, Firefox, Safari
- Mobile and desktop viewports
- Touch interactions

### Performance Tests

Load time and resource usage:

- Core Web Vitals (FCP, LCP, CLS)
- Memory usage over time
- Bundle size validation

## Contributing

When adding new E2E tests:

1. Follow the existing directory structure
2. Use Page Object Models for reusable interactions
3. Add appropriate `data-testid` attributes to components
4. Include accessibility checks with `@axe-core/playwright`
5. Update this README if adding new test categories

## Related Documentation

- [Frontend Testing Guide](../src/test/README.md)
- [Playwright Documentation](https://playwright.dev/docs/)
- [E2E Testing Strategy](../../plans/e2e-testing-strategy.md)

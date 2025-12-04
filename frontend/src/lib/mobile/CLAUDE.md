# Mobile UI Development Guide

## Overview

Mobile UI for screens < 768px width. Shares stores/utils with desktop, separate component tree.

## Critical Rules

- Follow all rules from `frontend/CLAUDE.md`
- Minimum tap target: 44x44px
- Use `systemIcons` from `$lib/utils/icons` (no emojis)
- Test on actual mobile devices, not just DevTools

## Structure

```
mobile/
├── layouts/        # MobileLayout, BottomNav, Header
├── components/     # Shared mobile components
│   ├── audio/      # AudioPlayer, StickyPlayer
│   ├── detection/  # DetectionRow, DetectionCard
│   └── ui/         # Common UI components
└── features/       # Page-specific features
    ├── dashboard/
    ├── detections/
    ├── analytics/
    └── settings/
```

## Patterns

### Touch Gestures

- Swipe actions on detection rows (verify/dismiss)
- Pull-to-refresh on list pages
- 44px minimum tap targets

### Navigation

- Bottom tab bar (4 tabs)
- Back button in header for sub-pages
- Sticky audio player above tab bar

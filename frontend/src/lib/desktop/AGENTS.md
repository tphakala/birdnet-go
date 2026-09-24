# Desktop UI

Everything under `src/lib/desktop/` targets **tablet and desktop** screens only.
Mobile will get its own, completely separate UI, so do not add phone-specific
breakpoints, layouts, or workarounds here.

- Reusable pieces live in `components/` (see `components/AGENTS.md`), feature
  modules in `features/`, page shells in `layouts/`, and top-level pages in
  `views/`.
- Settings pages follow `features/settings/AGENTS.md`.

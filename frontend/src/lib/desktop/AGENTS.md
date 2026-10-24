# Desktop UI

Everything under `src/lib/desktop/` targets **tablet and desktop** screens only.
Mobile is out of scope here by maintainer decision (see the root `AGENTS.md`):
it will get its own, completely separate UI, so do not add phone-specific
layouts or workarounds here. Tablets are touch devices, though: touch input and
affordances that do not depend on hover are still required, and `sm:`/`md:`
breakpoints for narrow windows are fine.

- Reusable pieces live in `components/` (see `components/AGENTS.md`), feature
  modules in `features/`, page shells in `layouts/`, and top-level pages in
  `views/`.
- Settings pages follow `features/settings/AGENTS.md`.

# Settings Pages

Settings pages live in `pages/`, shared settings UI in `components/`, and all
state in `$lib/stores/settings.ts`. Every setting must take effect without a
server restart (hot reload), so a new setting also needs backend support that
reads the value at the point of use.

## Store Structure: Verify, Never Assume

The form state mirrors the backend config JSON (`SettingsFormData` in
`$lib/stores/settings.ts`), not the page layout. Before wiring a control:

- Check the actual exports in `settings.ts` (for example `birdnetSettings`,
  `audioSettings`, `speciesSettings`, `securitySettings`, `settingsStore`,
  `settingsActions`) and import only what exists.
- Find the real path of the value. Many settings are nested under `realtime`
  (audio, species, privacy and dog-bark filters) rather than at the top level,
  and the top-level sections are named after the backend config (`main`,
  `birdnet`, `realtime`, `security`, `webServer`, `output`, ...).
- Change-detection paths must use the same structure:

```typescript
import { settingsStore } from '$lib/stores/settings';
import { hasSettingsChanged } from '$lib/utils/settingsChanges';

let store = $derived($settingsStore);
let privacyFilterHasChanges = $derived(
  hasSettingsChanged(
    store.originalData.realtime?.privacyFilter,
    store.formData.realtime?.privacyFilter
  )
);
```

## Updating Values

`settingsActions.updateSection(section, data)` merges `data` into the section
**shallowly** and then coerces values into their valid ranges. Nested objects
are replaced, not merged, so spread the existing nested object yourself:

```typescript
import { privacyFilterSettings, settingsActions } from '$lib/stores/settings';

// A $derived view of the store with every field defaulted, so the spread
// below always produces a complete object (FilterSettingsPage.svelte does this)
let privacy = $derived({
  enabled: $privacyFilterSettings?.enabled ?? false,
  confidence: $privacyFilterSettings?.confidence ?? 0.05,
  // ... every other field of the nested object
});

function updatePrivacyConfidence(confidence: number) {
  settingsActions.updateSection('realtime', {
    privacyFilter: { ...privacy, confidence },
  });
}
```

The other `realtime` fields are kept by the top-level merge; only the nested
`privacyFilter` object needs the spread. Spreading the raw store value instead
can produce an incomplete object when the section has not loaded yet.

`section` is typed as `keyof SettingsFormData`, so a wrong section name is a
type error; do not cast around it.

## Form Controls

Settings controls (in `$lib/desktop/components/forms/`) are used as controlled
components: pass the current value one way and write changes back through the
store in the callback. This is the pattern the existing pages use:

| Component        | Pattern                |
| ---------------- | ---------------------- |
| `NumberField`    | `value` + `onUpdate`   |
| `PasswordField`  | `value` + `onUpdate`   |
| `SubnetInput`    | `subnets` + `onUpdate` |
| `TextInput`      | `value` + `onchange`   |
| `Checkbox`       | `checked` + `onchange` |
| `SelectDropdown` | `value` + `onChange`   |

Avoid combining `bind:value` with an update callback on the same control: the
value is then written twice, and the store update in the callback is the one
that counts.

Wrap each group in `SettingsSection` (`components/SettingsSection.svelte`) and
pass its `hasChanges` flag so the section shows an unsaved-changes badge.

## Types

Do not use `as any` or other casts to get past settings typing problems. If a
property is missing, add it to the interface in `settings.ts` and to the
defaults in the same change. When the IDE and the build disagree, trust
`npm run typecheck` and restart the TypeScript language server.

## Checklist for a New Setting

- [ ] Backend config field exists and is read at the point of use (hot reload)
- [ ] Interface and defaults updated in `$lib/stores/settings.ts`
- [ ] Control wired with the pattern above, change detection on the right path
- [ ] Disabled or invalid states explain why (see `frontend/AGENTS.md` UX rules)
- [ ] Labels and help text translated in every locale
- [ ] Test covering the update handler or the rendered control

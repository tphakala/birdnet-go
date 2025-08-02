# Security Utilities Usage Examples

## Common Patterns for Fixing Object Injection Warnings

### 1. Dynamic Object Property Access

**Before (vulnerable):**

```typescript
const value = config[userInput]; // Warning: Object injection
const size = sizeClasses[variant]; // Warning: Object injection
```

**After (secure):**

```typescript
import { safeGet, createSafeMap } from '$lib/utils/security';

// Option 1: Use safeGet for simple lookups
const value = safeGet(config, userInput);

// Option 2: Convert to Map for frequent lookups
const sizeMap = createSafeMap(sizeClasses);
const size = sizeMap.get(variant);
```

### 2. Weather Icon Example

**Before:**

```typescript
const iconData = weatherIconMap[iconCode] || { day: '❓', night: '❓' };
```

**After:**

```typescript
import { safeGet } from '$lib/utils/security';

const DEFAULT_ICON = { day: '❓', night: '❓', description: 'Unknown' };
const iconData = safeGet(weatherIconMap, iconCode, DEFAULT_ICON) ?? DEFAULT_ICON;
```

### 3. Size/Variant Classes

**Before:**

```typescript
const sizeClasses = {
  sm: 'text-sm',
  md: 'text-base',
  lg: 'text-lg',
};
const className = sizeClasses[size];
```

**After:**

```typescript
// Option 1: Use Map
const sizeClasses = new Map([
  ['sm', 'text-sm'],
  ['md', 'text-base'],
  ['lg', 'text-lg'],
]);
const className = sizeClasses.get(size) ?? 'text-base';

// Option 2: Use switch statement
function getSizeClass(size: string): string {
  switch (size) {
    case 'sm':
      return 'text-sm';
    case 'md':
      return 'text-base';
    case 'lg':
      return 'text-lg';
    default:
      return 'text-base';
  }
}
```

### 4. Form Field Access

**Before:**

```typescript
const fieldValue = formData[fieldName];
errors[fieldName] = 'Required';
```

**After:**

```typescript
import { safeGet } from '$lib/utils/security';

// For reading values
const fieldValue = safeGet(formData, fieldName);

// For setting errors, create new object
const newErrors = { ...errors, [fieldName]: 'Required' };
```

### 5. Array Access from User Input

**Before:**

```typescript
const item = items[userIndex];
```

**After:**

```typescript
import { safeArrayAccess } from '$lib/utils/security';

const item = safeArrayAccess(items, userIndex);
```

### 6. URL Validation

**Before:**

```typescript
// Complex regex prone to ReDoS
const rtspPattern = /^rtsp:\/\/[\w.-]+(?::[0-9]{1,5})?(?:\/[\w/.?&=%-]*)?$/i;
const isValid = rtspPattern.test(url);
```

**After:**

```typescript
import { validateProtocolURL } from '$lib/utils/security';

const isValid = validateProtocolURL(url, ['rtsp'], 2048);
```

### 7. CIDR Validation

**Before:**

```typescript
const cidrPattern = /^(\d{1,3}\.){3}\d{1,3}\/\d{1,2}$/;
const isValid = cidrPattern.test(subnet);
```

**After:**

```typescript
import { validateCIDR } from '$lib/utils/security';

const isValid = validateCIDR(subnet);
```

## When Warnings Are False Positives

Some patterns are inherently safe but still trigger warnings:

### 1. Array Index from `#each` loops

```svelte
{#each items as item, index}
  <!-- This is safe, index comes from the loop -->
  <div class={errors[index] ? 'error' : ''}>
    {item}
  </div>
{/each}
```

### 2. Object.entries() iterations

```typescript
// This is safe, keys come from the object itself
for (const [key, value] of Object.entries(config)) {
  processConfig(key, value);
}
```

### 3. Known enum values

```typescript
type Size = 'sm' | 'md' | 'lg';
const sizeClasses: Record<Size, string> = {
  sm: 'text-sm',
  md: 'text-base',
  lg: 'text-lg',
};

// If size is typed as Size, this is safe
function getClass(size: Size) {
  return sizeClasses[size]; // TypeScript ensures this is safe
}
```

For these cases, you can add `// eslint-disable-next-line security/detect-object-injection` with a comment explaining why it's safe.

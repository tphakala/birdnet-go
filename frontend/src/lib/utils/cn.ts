/**
 * Utility for combining class names
 * Similar to clsx/classnames but simpler and type-safe
 */

type ClassValue = string | number | boolean | undefined | null | ClassDictionary | ClassArray;
type ClassDictionary = Record<string, boolean | undefined | null>;
type ClassArray = ClassValue[];

/**
 * Combines multiple class values into a single string
 * @param args - Class values to combine
 * @returns Combined class string
 */
export function cn(...args: ClassValue[]): string {
  const classes: string[] = [];

  for (const arg of args) {
    if (arg === null || arg === undefined || arg === false || arg === '') continue;

    const type = typeof arg;

    if (type === 'string' || type === 'number') {
      classes.push(String(arg));
    } else if (Array.isArray(arg)) {
      const innerClasses = cn(...arg);
      if (innerClasses) {
        classes.push(innerClasses);
      }
    } else if (type === 'object') {
      for (const key in arg as ClassDictionary) {
        if ((arg as ClassDictionary)[key]) {
          classes.push(key);
        }
      }
    }
  }

  return classes.join(' ');
}

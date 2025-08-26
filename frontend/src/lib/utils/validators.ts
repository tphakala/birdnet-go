/**
 * Form validation utilities
 */

import { safeArrayAccess } from './security';
import { parseLocalDateString } from './date';

export type ValidationResult = string | null;
export type Validator<T = unknown> = (value: T) => ValidationResult;

/**
 * Required field validator
 */
export function required(message: string = 'This field is required'): Validator {
  return (value: unknown): ValidationResult => {
    if (value === null || value === undefined || value === '') {
      return message;
    }
    if (Array.isArray(value) && value.length === 0) {
      return message;
    }
    if (typeof value === 'string' && value.trim() === '') {
      return message;
    }
    return null;
  };
}

/**
 * Email validator
 */
export function email(message: string = 'Invalid email address'): Validator<string> {
  const emailRegex = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

  return (value: string): ValidationResult => {
    if (!value) return null; // Use required() for required fields
    if (!emailRegex.test(value)) {
      return message;
    }
    return null;
  };
}

/**
 * URL validator
 */
export function url(message: string = 'Invalid URL'): Validator<string> {
  return (value: string): ValidationResult => {
    if (!value) return null; // Use required() for required fields

    try {
      new URL(value);
      return null;
    } catch {
      return message;
    }
  };
}

/**
 * Minimum length validator
 */
export function minLength(min: number, message?: string): Validator<string | unknown[]> {
  return (value: string | unknown[]): ValidationResult => {
    if (!value) return null; // Use required() for required fields

    const length = typeof value === 'string' ? value.length : value.length;
    if (length < min) {
      return message ?? `Must be at least ${min} characters long`;
    }
    return null;
  };
}

/**
 * Maximum length validator
 */
export function maxLength(max: number, message?: string): Validator<string | unknown[]> {
  return (value: string | unknown[]): ValidationResult => {
    if (!value) return null; // Use required() for required fields

    const length = typeof value === 'string' ? value.length : value.length;
    if (length > max) {
      return message ?? `Must be no more than ${max} characters long`;
    }
    return null;
  };
}

/**
 * Number range validator
 */
export function range(min: number, max: number, message?: string): Validator<number> {
  return (value: number): ValidationResult => {
    if (!value && value !== 0) return null; // Use required() for required fields

    if (value < min || value > max) {
      return message ?? `Must be between ${min} and ${max}`;
    }
    return null;
  };
}

/**
 * Minimum value validator
 */
export function min(minValue: number, message?: string): Validator<number> {
  return (value: number): ValidationResult => {
    if (!value && value !== 0) return null; // Use required() for required fields

    if (value < minValue) {
      return message ?? `Must be at least ${minValue}`;
    }
    return null;
  };
}

/**
 * Maximum value validator
 */
export function max(maxValue: number, message?: string): Validator<number> {
  return (value: number): ValidationResult => {
    if (!value && value !== 0) return null; // Use required() for required fields

    if (value > maxValue) {
      return message ?? `Must be no more than ${maxValue}`;
    }
    return null;
  };
}

/**
 * Pattern/regex validator
 */
export function pattern(regex: RegExp, message: string = 'Invalid format'): Validator<string> {
  return (value: string): ValidationResult => {
    if (!value) return null; // Use required() for required fields

    if (!regex.test(value)) {
      return message;
    }
    return null;
  };
}

/**
 * Integer validator
 */
export function integer(message: string = 'Must be a whole number'): Validator<number> {
  return (value: number): ValidationResult => {
    if (!value && value !== 0) return null; // Use required() for required fields

    if (!Number.isInteger(value)) {
      return message;
    }
    return null;
  };
}

/**
 * Positive number validator
 */
export function positive(message: string = 'Must be a positive number'): Validator<number> {
  return (value: number): ValidationResult => {
    if (!value && value !== 0) return null; // Use required() for required fields

    if (value <= 0) {
      return message;
    }
    return null;
  };
}

/**
 * Phone number validator
 */
export function phone(message: string = 'Invalid phone number'): Validator<string> {
  // Basic phone validation - adjust regex based on requirements
  const phoneRegex = /^[+]?[(]?[0-9]{3}[)]?[-\s.]?[(]?[0-9]{3}[)]?[-\s.]?[0-9]{4,6}$/;

  return (value: string): ValidationResult => {
    if (!value) return null; // Use required() for required fields

    const cleaned = value.replace(/\s/g, '');
    if (!phoneRegex.test(cleaned)) {
      return message;
    }
    return null;
  };
}

/**
 * Alpha characters only validator
 */
export function alpha(message: string = 'Must contain only letters'): Validator<string> {
  const alphaRegex = /^[a-zA-Z]+$/;

  return (value: string): ValidationResult => {
    if (!value) return null; // Use required() for required fields

    if (!alphaRegex.test(value)) {
      return message;
    }
    return null;
  };
}

/**
 * Alphanumeric validator
 */
export function alphanumeric(
  message: string = 'Must contain only letters and numbers'
): Validator<string> {
  const alphanumericRegex = /^[a-zA-Z0-9]+$/;

  return (value: string): ValidationResult => {
    if (!value) return null; // Use required() for required fields

    if (!alphanumericRegex.test(value)) {
      return message;
    }
    return null;
  };
}

/**
 * Date validator
 */
export function date(message: string = 'Invalid date'): Validator<string | Date> {
  return (value: string | Date): ValidationResult => {
    if (!value) return null; // Use required() for required fields

    const date = value instanceof Date ? value : parseLocalDateString(value);
    if (!date || isNaN(date.getTime())) {
      return message;
    }
    return null;
  };
}

/**
 * Future date validator
 */
export function futureDate(
  message: string = 'Date must be in the future'
): Validator<string | Date> {
  return (value: string | Date): ValidationResult => {
    if (!value) return null; // Use required() for required fields

    const date = value instanceof Date ? value : parseLocalDateString(value);
    if (!date || isNaN(date.getTime())) {
      return 'Invalid date';
    }

    if (date <= new Date()) {
      return message;
    }
    return null;
  };
}

/**
 * Past date validator
 */
export function pastDate(message: string = 'Date must be in the past'): Validator<string | Date> {
  return (value: string | Date): ValidationResult => {
    if (!value) return null; // Use required() for required fields

    const date = value instanceof Date ? value : parseLocalDateString(value);
    if (!date || isNaN(date.getTime())) {
      return 'Invalid date';
    }

    if (date >= new Date()) {
      return message;
    }
    return null;
  };
}

/**
 * Custom validator
 */
export function custom<T>(validatorFn: (value: T) => boolean, message: string): Validator<T> {
  return (value: T): ValidationResult => {
    if (!validatorFn(value)) {
      return message;
    }
    return null;
  };
}

/**
 * Compose multiple validators
 */
export function compose<T>(...validators: Validator<T>[]): Validator<T> {
  return (value: T): ValidationResult => {
    for (const validator of validators) {
      const result = validator(value);
      if (result !== null) {
        return result;
      }
    }
    return null;
  };
}

/**
 * Conditional validator
 */
export function when<T>(condition: (value: T) => boolean, validator: Validator<T>): Validator<T> {
  return (value: T): ValidationResult => {
    if (condition(value)) {
      return validator(value);
    }
    return null;
  };
}

/**
 * Array validator - validates each item
 */
export function arrayOf<T>(itemValidator: Validator<T>): Validator<T[]> {
  return (values: T[]): ValidationResult => {
    if (!Array.isArray(values)) return null;

    for (let i = 0; i < values.length; i++) {
      const item = safeArrayAccess(values, i);
      if (item === undefined) continue;
      const result = itemValidator(item);
      if (result !== null) {
        return `Item ${i + 1}: ${result}`;
      }
    }
    return null;
  };
}

/**
 * Matches another field validator
 */
export function matches<T>(
  getOtherValue: () => T,
  message: string = 'Values do not match'
): Validator<T> {
  return (value: T): ValidationResult => {
    if (value !== getOtherValue()) {
      return message;
    }
    return null;
  };
}

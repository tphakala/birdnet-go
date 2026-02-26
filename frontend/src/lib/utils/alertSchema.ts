/**
 * Alert schema translation utilities.
 *
 * Constructs i18n keys from schema `name` fields (which are stable identifiers)
 * and resolves them via the existing translateField() helper.  Falls back to the
 * English label supplied by the backend when the key is missing or hasn't loaded.
 */
import { translateField } from '$lib/utils/notifications';

/** Convert a dotted schema name to a flat key segment: "stream.connected" → "stream_connected" */
function toKeySegment(name: string): string {
  return name.replace(/\./g, '_');
}

/** Translate an object-type name (e.g. "stream" → "Audio Stream"). */
export function schemaObjectTypeLabel(name: string, fallback: string): string {
  return translateField(
    `settings.alerts.schema.objectTypes.${toKeySegment(name)}`,
    undefined,
    fallback
  );
}

/** Translate an event name (e.g. "stream.connected" → "Stream Connected"). */
export function schemaEventLabel(name: string, fallback: string): string {
  return translateField(`settings.alerts.schema.events.${toKeySegment(name)}`, undefined, fallback);
}

/** Translate a metric name (e.g. "system.cpu_usage" → "CPU Usage"). */
export function schemaMetricLabel(name: string, fallback: string): string {
  return translateField(
    `settings.alerts.schema.metrics.${toKeySegment(name)}`,
    undefined,
    fallback
  );
}

/** Translate a property name (e.g. "species_name" → "Species Name"). */
export function schemaPropertyLabel(name: string, fallback: string): string {
  return translateField(
    `settings.alerts.schema.properties.${toKeySegment(name)}`,
    undefined,
    fallback
  );
}

/** Translate an operator name (e.g. "greater_than" → "greater than"). */
export function schemaOperatorLabel(name: string, fallback: string): string {
  return translateField(
    `settings.alerts.schema.operators.${toKeySegment(name)}`,
    undefined,
    fallback
  );
}

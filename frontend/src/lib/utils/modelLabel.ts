import type { BackendModel } from '$lib/stores/models.svelte';

/**
 * Display label for a model in the source editors: the neutral name, followed by
 * the live execution backend in parentheses when the server reported one. A model
 * that is not loaded has no backend, so it shows no suffix.
 */
export function modelLabel(model: Pick<BackendModel, 'name' | 'backend'>): string {
  return model.backend ? `${model.name} (${model.backend})` : model.name;
}

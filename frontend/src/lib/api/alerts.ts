import { api } from '$lib/utils/api';

// ---------- Entity types (match Go entities) ----------

export interface AlertCondition {
  id: number;
  rule_id: number;
  property: string;
  operator: string;
  value: string;
  duration_sec: number;
  sort_order: number;
}

export interface AlertAction {
  id: number;
  rule_id: number;
  target: string;
  template_title: string;
  template_message: string;
  sort_order: number;
}

export interface AlertRule {
  id: number;
  name: string;
  description: string;
  enabled: boolean;
  built_in: boolean;
  object_type: string;
  trigger_type: string;
  event_name: string;
  metric_name: string;
  cooldown_sec: number;
  created_at: string;
  updated_at: string;
  conditions: AlertCondition[];
  actions: AlertAction[];
}

export interface AlertHistory {
  id: number;
  rule_id: number;
  fired_at: string;
  event_data: string;
  actions: string;
  created_at: string;
  rule?: AlertRule;
}

// ---------- Schema types (match Go alerting.Schema) ----------

export interface PropertySchema {
  name: string;
  label: string;
  type: 'string' | 'number';
  operators: string[];
}

export interface EventSchema {
  name: string;
  label: string;
  properties: PropertySchema[];
}

export interface MetricSchema {
  name: string;
  label: string;
  unit: string;
  properties: PropertySchema[];
}

export interface OperatorSchema {
  name: string;
  label: string;
  type: 'string' | 'number' | 'all';
}

export interface ObjectTypeSchema {
  name: string;
  label: string;
  events?: EventSchema[];
  metrics?: MetricSchema[];
}

export interface AlertSchema {
  objectTypes: ObjectTypeSchema[];
  operators: OperatorSchema[];
}

// ---------- API response types ----------

interface ListRulesResponse {
  rules: AlertRule[];
  count: number;
}

interface ListHistoryResponse {
  history: AlertHistory[];
  total: number;
  limit: number;
  offset: number;
}

interface ExportResponse {
  rules: AlertRule[];
  version: number;
}

interface ImportResponse {
  imported: number;
  total: number;
}

// ---------- Filter types ----------

export interface AlertRuleFilter {
  object_type?: string;
  enabled?: boolean;
  built_in?: boolean;
}

export interface AlertHistoryFilter {
  rule_id?: number;
  limit?: number;
  offset?: number;
}

// ---------- API functions ----------

const BASE = '/api/v2/alerts';

function buildRuleFilterParams(filter?: AlertRuleFilter): string {
  if (!filter) return '';
  const params = new URLSearchParams();
  if (filter.object_type) params.set('object_type', filter.object_type);
  if (filter.enabled !== undefined) params.set('enabled', String(filter.enabled));
  if (filter.built_in !== undefined) params.set('built_in', String(filter.built_in));
  const qs = params.toString();
  return qs ? `?${qs}` : '';
}

function buildHistoryFilterParams(filter?: AlertHistoryFilter): string {
  if (!filter) return '';
  const params = new URLSearchParams();
  if (filter.rule_id !== undefined) params.set('rule_id', String(filter.rule_id));
  if (filter.limit !== undefined) params.set('limit', String(filter.limit));
  if (filter.offset !== undefined) params.set('offset', String(filter.offset));
  const qs = params.toString();
  return qs ? `?${qs}` : '';
}

/** Fetch all alert rules, optionally filtered. */
export async function fetchAlertRules(filter?: AlertRuleFilter): Promise<AlertRule[]> {
  const resp = await api.get<ListRulesResponse>(`${BASE}/rules${buildRuleFilterParams(filter)}`);
  return resp.rules;
}

/** Fetch a single alert rule by ID. */
export async function getAlertRule(id: number): Promise<AlertRule> {
  return api.get<AlertRule>(`${BASE}/rules/${id}`);
}

/** Create a new alert rule. */
export async function createAlertRule(rule: Partial<AlertRule>): Promise<AlertRule> {
  return api.post<AlertRule>(`${BASE}/rules`, rule);
}

/** Update an existing alert rule. */
export async function updateAlertRule(id: number, rule: Partial<AlertRule>): Promise<AlertRule> {
  return api.put<AlertRule>(`${BASE}/rules/${id}`, rule);
}

/** Toggle an alert rule's enabled state. */
export async function toggleAlertRule(id: number, enabled: boolean): Promise<void> {
  await api.patch<{ id: number; enabled: boolean }>(`${BASE}/rules/${id}/toggle`, { enabled });
}

/** Delete an alert rule. */
export async function deleteAlertRule(id: number): Promise<void> {
  await api.delete(`${BASE}/rules/${id}`);
}

/** Fire a test event for an alert rule. */
export async function testAlertRule(id: number): Promise<void> {
  await api.post(`${BASE}/rules/${id}/test`);
}

/** Reset all built-in rules to defaults. */
export async function resetAlertDefaults(): Promise<void> {
  await api.post(`${BASE}/rules/reset-defaults`);
}

/** Fetch paginated alert history. */
export async function fetchAlertHistory(filter?: AlertHistoryFilter): Promise<ListHistoryResponse> {
  return api.get<ListHistoryResponse>(`${BASE}/history${buildHistoryFilterParams(filter)}`);
}

/** Clear all alert history records. */
export async function clearAlertHistory(): Promise<{ deleted: number }> {
  return api.delete<{ deleted: number }>(`${BASE}/history`);
}

/** Fetch the alerting schema for the UI. */
export async function fetchAlertSchema(): Promise<AlertSchema> {
  return api.get<AlertSchema>(`${BASE}/schema`);
}

/** Export all alert rules as JSON. */
export async function exportAlertRules(): Promise<ExportResponse> {
  return api.get<ExportResponse>(`${BASE}/rules/export`);
}

/** Import alert rules from JSON. */
export async function importAlertRules(
  rules: AlertRule[],
  version: number = 1
): Promise<ImportResponse> {
  return api.post<ImportResponse>(`${BASE}/rules/import`, { rules, version });
}

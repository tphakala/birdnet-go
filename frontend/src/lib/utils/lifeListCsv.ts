/**
 * Parses an eBird life list CSV export into life-list entries.
 *
 * Only structural parseability is validated here (a non-empty, binomial-looking
 * scientific name) — not whether the species exists in BirdNET's own taxonomy or
 * eBird's current taxonomy. That matching happens on the backend, continuously,
 * at detection time (see internal/conf.Settings.IsOnLifeList); attempting to
 * validate species identity at import time would be misleading given known
 * taxonomy-version mismatches (splits/lumps) that can't be resolved client-side.
 */

export interface LifeListParseRejection {
  rowNumber: number; // 1-based, counting the header as row 1
  rawRow: string;
  reason: string;
}

export interface LifeListParseResult {
  accepted: string[]; // "ScientificName_CommonName" entries, deduplicated
  rejected: LifeListParseRejection[];
}

const SCIENTIFIC_NAME_HEADERS = ['scientific name', 'sci_name', 'scientific_name'];
const COMMON_NAME_HEADERS = ['common name', 'com_name', 'common_name'];

// eBird's own explanation of "spuh"/slash/hybrid taxonomic categories, linked
// from the rejected-rows panel so users understand these are a real eBird
// concept, not a parsing bug.
export const EBIRD_TAXONOMY_HELP_URL =
  'https://support.ebird.org/en/support/solutions/articles/48000837816-the-ebird-taxonomy';

// Two space-separated words, letters (and hyphens, for hyphenated epithets)
// only, optionally followed by a subspecies epithet. Deliberately permissive:
// this only screens out obviously-garbage rows (blank cells, header-like
// repeats, free-text notes), not taxonomically invalid names.
// eslint-disable-next-line security/detect-unsafe-regex -- three space-separated quantified groups with no overlap or nesting; linear-time, not susceptible to catastrophic backtracking
const BINOMIAL_PATTERN = /^[A-Za-z][A-Za-z-]+ [a-z][a-z-]+( [a-z][a-z-]+)?$/;

/**
 * Splits a single CSV row into fields, honoring double-quoted fields that may
 * contain commas (eBird's Location column commonly does). Does not handle
 * escaped quotes inside quoted fields beyond the standard "" doubling.
 */
function splitCsvRow(row: string): string[] {
  const fields: string[] = [];
  let current = '';
  let inQuotes = false;

  for (let i = 0; i < row.length; i++) {
    // eslint-disable-next-line security/detect-object-injection -- i is a numeric loop counter bounded by row.length
    const char = row[i];

    if (inQuotes) {
      if (char === '"') {
        if (row[i + 1] === '"') {
          current += '"';
          i++;
        } else {
          inQuotes = false;
        }
      } else {
        current += char;
      }
      continue;
    }

    if (char === '"') {
      inQuotes = true;
    } else if (char === ',') {
      fields.push(current);
      current = '';
    } else {
      current += char;
    }
  }
  fields.push(current);

  return fields.map(f => f.trim());
}

function findColumn(header: string[], candidates: string[]): number {
  const normalized = header.map(h => h.toLowerCase().trim());
  for (const candidate of candidates) {
    const idx = normalized.indexOf(candidate);
    if (idx !== -1) return idx;
  }
  return -1;
}

/**
 * Describes why a scientific name that failed BINOMIAL_PATTERN can't be
 * matched, naming the specific eBird taxonomic category when recognizable
 * (spuh, slash, hybrid) rather than a generic "invalid" message — these are
 * real, common eBird export categories, not malformed data.
 */
function describeUnmatchable(scientificName: string, commonName: string): string {
  const label = commonName ? `"${scientificName}" ("${commonName}")` : `"${scientificName}"`;

  if (scientificName.includes('/')) {
    return (
      `${label} is an eBird "slash" entry — identification to one of two similar species ` +
      `(e.g. Tundra/Trumpeter Swan). BirdNET always reports one specific species per ` +
      `detection, so a slash entry can never match.`
    );
  }
  if (/ x /i.test(scientificName)) {
    return `${label} is a hybrid entry — BirdNET does not report hybrid identifications.`;
  }
  if (/\ssp\.?$/i.test(scientificName)) {
    return (
      `${label} is an eBird "spuh" — a genus/family-level placeholder for a bird not ` +
      `identified to species (e.g. "Empidonax sp."). BirdNET always reports a specific ` +
      `species, so a spuh can never match.`
    );
  }
  return `${label} does not look like a single scientific species name.`;
}

export function parseLifeListCsv(csvText: string): LifeListParseResult {
  const lines = csvText.split(/\r\n|\r|\n/).filter(line => line.trim().length > 0);

  const accepted: string[] = [];
  const rejected: LifeListParseRejection[] = [];
  const seen = new Set<string>();

  if (lines.length === 0) {
    return { accepted, rejected };
  }

  const header = splitCsvRow(lines[0]);
  const sciCol = findColumn(header, SCIENTIFIC_NAME_HEADERS);
  const comCol = findColumn(header, COMMON_NAME_HEADERS);

  if (sciCol === -1) {
    rejected.push({
      rowNumber: 1,
      rawRow: lines[0],
      reason: 'No "Scientific Name" column found in header',
    });
    return { accepted, rejected };
  }

  for (let i = 1; i < lines.length; i++) {
    const rowNumber = i + 1;
    // eslint-disable-next-line security/detect-object-injection -- i is a numeric loop counter bounded by lines.length
    const rawRow = lines[i];
    const fields = splitCsvRow(rawRow);

    // eslint-disable-next-line security/detect-object-injection -- sciCol is a numeric column index resolved by findColumn, not user-controlled key access
    const scientificName = (fields[sciCol] ?? '').trim();
    // eslint-disable-next-line security/detect-object-injection -- comCol is a numeric column index resolved by findColumn, not user-controlled key access
    const commonName = comCol !== -1 ? (fields[comCol] ?? '').trim() : '';

    if (!scientificName) {
      const reason = commonName
        ? `Empty scientific name (common name: "${commonName}")`
        : 'Empty scientific name';
      rejected.push({ rowNumber, rawRow, reason });
      continue;
    }
    if (!BINOMIAL_PATTERN.test(scientificName)) {
      rejected.push({
        rowNumber,
        rawRow,
        reason: describeUnmatchable(scientificName, commonName),
      });
      continue;
    }

    const entry = commonName ? `${scientificName}_${commonName}` : scientificName;
    const dedupeKey = entry.toLowerCase();
    if (seen.has(dedupeKey)) {
      continue; // silent dedupe, not a rejection
    }
    seen.add(dedupeKey);
    accepted.push(entry);
  }

  return { accepted, rejected };
}

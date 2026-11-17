// Helpers for the licence and policy documents of model catalog entries: the
// display name of a document kind, the documents an install still needs an
// agreement for, and the document list of the server's agreement refusal.

import { t } from '$lib/i18n';
import type { AgreementDocument, CatalogDocument, CatalogEntry } from '$lib/types/models';
import { isPlainObject } from '$lib/utils/security';

/** Document kind of a licence. */
export const DOCUMENT_KIND_LICENSE = 'license';
/** Document kind of an acceptable use policy. */
export const DOCUMENT_KIND_ACCEPTABLE_USE = 'acceptable_use';

/**
 * The display name of a document kind. The kind vocabulary is open, so a kind
 * this build does not know gets a generic name instead of its raw identifier.
 */
export function documentKindLabel(kind: string): string {
  switch (kind) {
    case DOCUMENT_KIND_LICENSE:
      return t('analysis.gallery.documents.kinds.license');
    case DOCUMENT_KIND_ACCEPTABLE_USE:
      return t('analysis.gallery.documents.kinds.acceptable_use');
    default:
      return t('analysis.gallery.documents.genericKind');
  }
}

/** An entry's documents, split by what the install flow does with them. */
export interface EntryDocumentSplit {
  /** Hosted documents that require an agreement the server has not recorded. */
  outstanding: AgreementDocument[];
  /**
   * The other documents, shown for reference (a link or text to view). An outstanding
   * document whose sha256 is already in `outstanding` is in neither list.
   */
  reference: CatalogDocument[];
}

/**
 * Split an entry's documents into those still to agree to and those to show for
 * reference. A text listed twice is asked for once, since agreeing is per text.
 */
export function splitEntryDocuments(entry: CatalogEntry): EntryDocumentSplit {
  const split: EntryDocumentSplit = { outstanding: [], reference: [] };
  for (const doc of entry.documents ?? []) {
    if (doc.requiresAgreement && !doc.agreed && doc.sha256) {
      const sha256 = doc.sha256;
      if (!split.outstanding.some(d => d.sha256 === sha256)) {
        split.outstanding.push({ repo: doc.repo, kind: doc.kind, sha256 });
      }
    } else {
      split.reference.push(doc);
    }
  }
  return split;
}

function isAgreementDocument(doc: unknown): doc is AgreementDocument {
  return (
    isPlainObject(doc) &&
    typeof doc.repo === 'string' &&
    typeof doc.kind === 'string' &&
    typeof doc.sha256 === 'string' &&
    doc.sha256 !== ''
  );
}

/**
 * The documents listed by the server's agreement refusal (error_params.documents).
 * Malformed items are dropped and a repeated sha256 is kept once (the dialog lists
 * one checkbox per text), so an empty result means the refusal named nothing the
 * user could agree to.
 */
export function refusalDocuments(params: Record<string, unknown> | undefined): AgreementDocument[] {
  const docs = params?.documents;
  if (!Array.isArray(docs)) return [];
  const seen = new Set<string>();
  const out: AgreementDocument[] = [];
  for (const d of docs) {
    if (!isAgreementDocument(d) || seen.has(d.sha256)) continue;
    seen.add(d.sha256);
    out.push({ repo: d.repo, kind: d.kind, sha256: d.sha256 });
  }
  return out;
}

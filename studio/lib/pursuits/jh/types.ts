/* Job Hunt cockpit types.
 *
 * Mirrors core/internal/pursuits/jh one-for-one: types.go, store.go (Role,
 * CorpusEntry, PursuitHeader), contacts.go, artifacts.go, cockpit.go and
 * write.go. The Go side is the source of truth; every field here exists there
 * with the same json tag.
 *
 * Two nullability shapes come across the wire and they are NOT the same:
 *
 *   `?: number`      the Go field carries `omitempty`, so it is absent when
 *                    unset (comp_min, comp_max, posted_at, fit_score,
 *                    ghost_score).
 *   `: string | null` the Go field is a pointer WITHOUT `omitempty`, so it
 *                    arrives as an explicit null (a contact's role_id, a
 *                    contact's outreach_sent_at, an artifact's stored id).
 *
 * Getting that wrong is how a missing salary becomes a rendered zero, which is
 * the one thing the board must never do.
 */

/** A pursuit header, as the cockpit read returns it. */
export type JHPursuitHeader = {
  id: string;
  title: string;
  cadence: string;
  experience: string;
  config: Record<string, unknown>;
  created_at: string;
};

/** One posting in the pipeline. `stage` is the kanban column it sits in. */
export type JHRole = {
  id: string;
  pursuit_id: string;
  company: string;
  role_title: string;
  source: string;
  url: string;
  location: string;
  /** Absent when the posting stated no band. Never render a missing band as 0. */
  comp_min?: number;
  comp_max?: number;
  /** The posting's own wording, kept when the band could not be parsed. */
  comp_text: string;
  posted_at?: string;
  discovered_at: string;
  /** 0 to 100. Absent until something has scored the role. */
  fit_score?: number;
  fit_reasoning: string;
  /** 0 to 100. Absent until something has scored the posting. */
  ghost_score?: number;
  /** Individual ghost-listing signals. Always an array, empty when clean. */
  ghost_flags: string[];
  stage: string;
  stage_changed_at: string;
  notes: string;
  external_id: string;
  created_at: string;
  updated_at: string;
};

/** A banked interview answer. */
export type JHCorpusEntry = {
  id: string;
  pursuit_id: string;
  theme: string;
  question: string;
  answer: string;
  metrics: Record<string, unknown>;
  tags: string[];
  source: string;
  created_at: string;
};

/** A hiring manager or recruiter, with where the outreach has got to. */
export type JHContact = {
  id: string;
  pursuit_id: string;
  /** The role this person was found for, or null when they are company-wide. */
  role_id: string | null;
  name: string;
  title: string;
  company: string;
  linkedin_url: string;
  email: string;
  outreach_status: string;
  outreach_sent_at: string | null;
  last_message: string;
  created_at: string;
  updated_at: string;
};

/** A document produced for a role. */
export type JHArtifact = {
  id: string;
  pursuit_id: string;
  role_id: string;
  kind: string;
  /** The stored document's own id, null until the document itself exists. */
  artifact_id: string | null;
  title: string;
  status: string;
  created_at: string;
};

/** The derived read of the board. Every figure is computed from the rows in
 *  the same payload, so it can never disagree with what is on screen. */
export type JHSummary = {
  total_roles: number;
  /** Carries EVERY stage in the vocabulary, including the ones at zero. */
  roles_by_stage: Record<string, number>;
  corpus_count: number;
  contacts_awaiting_reply: number;
  artifact_count: number;
  contact_count: number;
};

/** Every constrained value the board can hold, shipped with the board.
 *
 *  `role_stages` is in pipeline order, and that order IS the left-to-right
 *  order of the columns. Nothing in the UI may hardcode these lists: a stage
 *  added to the schema has to appear as a column without a client change. */
export type JHVocabulary = {
  role_stages: string[];
  role_sources: string[];
  corpus_sources: string[];
  contact_statuses: string[];
  artifact_kinds: string[];
  artifact_statuses: string[];
};

export type JHCockpit = {
  pursuit: JHPursuitHeader;
  roles: JHRole[];
  corpus: JHCorpusEntry[];
  contacts: JHContact[];
  artifacts: JHArtifact[];
  summary: JHSummary;
  vocabulary: JHVocabulary;
};

/** Write actions, mirroring jh.WriteActions() in Go. These double as the HTTP
 *  path suffix under /api/pursuits/jh/. */
export type JHAction =
  | "role"
  | "role/stage"
  | "corpus"
  | "contact"
  | "contact/status"
  | "artifact"
  | "artifact/status";

/** The write envelope, mirroring jh.WriteRequest.
 *
 *  Note the two artifact ids, which are different things: `artifact_id` names
 *  the row on the board, `stored_artifact_id` names the document's bytes. */
export type JHWriteRequest = {
  role_id?: string;
  company?: string;
  role_title?: string;
  source?: string;
  url?: string;
  location?: string;
  comp_min?: number | null;
  comp_max?: number | null;
  comp_text?: string;
  posted_at?: string | null;
  fit_score?: number | null;
  fit_reasoning?: string;
  ghost_score?: number | null;
  ghost_flags?: string[];
  notes?: string;
  external_id?: string;
  stage?: string;

  theme?: string;
  question?: string;
  answer?: string;
  metrics?: Record<string, unknown>;
  tags?: string[];

  contact_id?: string;
  name?: string;
  title?: string;
  linkedin_url?: string;
  email?: string;
  last_message?: string;

  artifact_id?: string;
  kind?: string;
  stored_artifact_id?: string | null;

  /** The outreach ladder for a contact, the approval ladder for a document.
   *  Which one it means is decided by the action, never guessed. */
  status?: string;
};

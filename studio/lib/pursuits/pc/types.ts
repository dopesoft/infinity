/* Psycho-Cybernetics cockpit types.
 *
 * Mirrors core/internal/pursuits/pc/types.go one-for-one. The Go side is the
 * source of truth; every field here exists there with the same json tag.
 */

export type PCPhase =
  | "onboarding"
  | "morning"
  | "midday"
  | "evening"
  | "recovery"
  | "review"
  | "adjustment"
  | "idle";

export type PCPressureTest = {
  fear: string;
  doubt: string;
  alternate: string;
};

export type PCState = {
  pursuit_id: string;
  cycle_number: number;
  cycle_length_days: number;
  current_day: number;
  cycle_started_at: string;
  missed_days_count: number;
  current_identity: string;
  current_objective: string;
  current_limiting_pattern: string;
  pressure_test: PCPressureTest;
  timezone: string;
  last_morning_at?: string;
  last_midday_at?: string;
  last_evening_at?: string;
  created_at: string;
  updated_at: string;
};

export type PCSession = {
  id: string;
  pursuit_id: string;
  kind: string;
  cycle_number: number;
  day_in_cycle: number;
  answers: Record<string, unknown>;
  coach_note: string;
  occurred_at: string;
  created_at: string;
};

export type PCProof = {
  id: string;
  pursuit_id: string;
  session_id?: string;
  label: string;
  cycle_number: number;
  day_in_cycle: number;
  planned_at: string;
  taken: boolean;
  taken_at?: string;
  note: string;
  created_at: string;
  updated_at: string;
};

export type PCEvidence = {
  id: string;
  pursuit_id: string;
  session_id?: string;
  kind: "evidence" | "resistance";
  body: string;
  tags: string[];
  cycle_number: number;
  day_in_cycle: number;
  captured_at: string;
};

export type PCMemory = {
  id: string;
  pursuit_id: string;
  title: string;
  body: string;
  tags: string[];
  weight: number;
  saved_at: string;
};

export type PCPattern = {
  id: string;
  pursuit_id: string;
  kind: "limiting" | "operating" | "correction";
  body: string;
  refs: Record<string, unknown>;
  cycle_number: number;
  day_in_cycle: number;
  created_at: string;
};

export type PCReview = {
  id: string;
  pursuit_id: string;
  cycle_number: number;
  wins: string;
  misses: string;
  next_identity: string;
  next_objective: string;
  next_pattern: string;
  adjustments: Record<string, unknown>;
  completed_at: string;
  created_at: string;
};

export type PCGuidancePrompt = {
  key: string;
  label: string;
  placeholder: string;
  help?: string;
};

export type PCGuidance = {
  phase: PCPhase;
  headline: string;
  body: string;
  hints: string[];
  prompt: string;
  secondary_prompts?: PCGuidancePrompt[];
};

export type PCPursuitHeader = {
  id: string;
  title: string;
  cadence: string;
  experience: string;
  config: Record<string, unknown>;
  created_at: string;
};

export type PCCockpit = {
  pursuit: PCPursuitHeader;
  state: PCState;
  today_proofs: PCProof[];
  recent_proofs: PCProof[];
  today_evidence: PCEvidence[];
  recent_evidence: PCEvidence[];
  memories: PCMemory[];
  patterns: PCPattern[];
  corrections: PCPattern[];
  recent_sessions: PCSession[];
  cycle_reviews: PCReview[];
  /** The banked memory to rehearse today. Absent while the bank is empty. */
  rehearsal_memory?: PCMemory;
  /** Adaptive note, present only on a day whose resistance outweighed its
   *  evidence. Sits alongside `guidance`, never replaces it. */
  adjustment?: PCGuidance;
  guidance: PCGuidance;
};

/** Write actions, mirroring pc.WriteActions() in Go. These double as the HTTP
 *  path suffix under /api/pursuits/pc/. */
export type PCAction =
  | "identity"
  | "session"
  | "proof"
  | "proof/taken"
  | "evidence"
  | "memory"
  | "pattern"
  | "review";

export type PCWriteRequest = {
  identity?: string;
  objective?: string;
  pattern?: string;
  pressure_test?: PCPressureTest;
  timezone?: string;
  kind?: string;
  answers?: Record<string, unknown>;
  coach_note?: string;
  proof_id?: string;
  label?: string;
  taken?: boolean;
  note?: string;
  session_id?: string;
  body?: string;
  title?: string;
  tags?: string[];
  weight?: number;
  wins?: string;
  misses?: string;
  next_identity?: string;
  next_objective?: string;
  next_pattern?: string;
  adjustments?: Record<string, unknown>;
};

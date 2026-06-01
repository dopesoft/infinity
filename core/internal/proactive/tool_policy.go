package proactive

import "github.com/dopesoft/infinity/core/internal/toolclass"

// tool_policy.go delegates to the shared toolclass package (the single source
// of truth, also consulted by the plasticity Gym) so the proactive detectors —
// high-surprise curiosity, repeated-tool-error self-heal, skill-pattern
// authoring — classify tools identically to every other subsystem. The thin
// wrappers keep the detector call sites unqualified and readable.

// EligibleForHighSurprise: see toolclass.EligibleForHighSurprise.
func EligibleForHighSurprise(tool string) bool { return toolclass.EligibleForHighSurprise(tool) }

// EligibleForRepeatedError: see toolclass.EligibleForRepeatedError.
func EligibleForRepeatedError(tool string) bool { return toolclass.EligibleForRepeatedError(tool) }

// SubstantiveForSkillPattern: see toolclass.SubstantiveForSkillPattern.
func SubstantiveForSkillPattern(tool string) bool { return toolclass.SubstantiveForSkillPattern(tool) }

// Package jh implements the Job Hunt experience on top of the generic
// pursuits substrate.
//
// Design intent (see Rule #1 in CLAUDE.md): the ordinary pursuits surface
// stays intact and this package plugs in ONLY when mem_pursuits.experience
// equals ExperienceJobHunt. It is a sibling of the pc package, not a fork of
// it: both hang off the same mem_pursuits row and neither knows about the
// other's tables.
//
// The experience is a search for a remote Head of Product or VP Product
// role. What makes it bespoke rather than a checkbox is that the boss is
// tracking a pipeline, not a streak: roles move between stages, interview
// answers get banked and reused, hiring managers get contacted, and a
// tailored resume or cover letter is generated per role. None of that fits a
// habit with a done-today flag, which is why it opens its own cockpit.
//
// Backing tables live in 206_jobhunt_roles.sql and 207_jobhunt_support.sql:
//
//	mem_jobhunt_roles      the pipeline, one row per role, kanban stage on it
//	mem_jobhunt_corpus     banked interview answers, grouped by theme
//	mem_jobhunt_contacts   hiring managers and recruiters, with outreach state
//	mem_jobhunt_artifacts  generated resumes, cover letters, positioning reads
package jh

// ExperienceJobHunt is the mem_pursuits.experience discriminator that wires a
// pursuit into this package's cockpit.
//
// Declared here rather than alongside ExperiencePsychoCybernetics so each
// experience owns its own constant, and 208_jobhunt_experience.sql admits the
// same literal on the database side. The two have to agree: a value the
// column rejects is a pursuit that can never be created.
const ExperienceJobHunt = "job_hunt"

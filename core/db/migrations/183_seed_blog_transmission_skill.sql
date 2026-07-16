-- 183_seed_blog_transmission_skill.sql - seed the write-dopesoft-transmission
-- skill.
--
-- The recipe for turning the boss's own take into a published post on
-- dopesoft.io/blog ("Transmissions"). Seeded straight into the durable store
-- (mem_skills + mem_skill_versions + mem_skill_active) exactly like
-- 023_seed_self_improve_skill.sql: on boot MaterializeActiveSkills derives it
-- to the on-disk skills root and Registry.Reload loads it, so it is live
-- immediately and survives Railway's ephemeral filesystem.
--
-- No new mechanics ship with this. The whole publish path already exists:
-- github__create_or_update_file is in GitHubGate's default block list, so the
-- write is queued to mem_trust_contracts as a high-risk contract, the boss
-- reads the draft in Studio (ObjectViewer renders long string args as prose
-- rather than escaped JSON), and taps approve. If he approves inside the
-- gate's 15 minute wait the gate itself runs it; if he approves later -
-- which is the normal case for reading a post - TrustExecutor picks up the
-- orphaned approval and runs it out-of-band. Either way the file lands on
-- main, Railway rebuilds, and the post is live. The sitemap is derived from
-- the content directory, so nothing else in dopesoft-v2 needs touching.
--
-- Judgment-only per Rule #1b: this body carries WHICH weeks are worth a post,
-- WHAT makes one publishable, and WHOSE opinion it has to be. It carries no
-- behaviour that the runtime silently depends on.
--
-- ON CONFLICT DO NOTHING throughout: idempotent, and it never clobbers a
-- version the agent or Voyager has since evolved.

BEGIN;

INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source)
VALUES (
  'write-dopesoft-transmission',
  'Turn one of the boss''s own takes into a post on dopesoft.io/blog. His opinion is the input you cannot generate - get it from him, draft around it in his voice, and queue the publish for his approval. Publishing nothing is the common, correct outcome.',
  'high',
  '["api.github.com"]'::jsonb,
  '["write a blog post","draft a transmission","write a transmission","post this to the dopesoft blog","write this up for the blog","turn this into a post","publish this to dopesoft"]'::jsonb,
  '[{"name":"topic","type":"string","required":false,"doc":"what the post is about - usually already in this conversation as a news breakdown, a voice call, or something he just argued about. Rarely passed explicitly."}]'::jsonb,
  '[{"name":"outcome","type":"string","doc":"either the contract id of the queued publish plus the slug, or a plain statement that nothing was worth posting this week and why"}]'::jsonb,
  0.8,
  'active',
  'manual'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions
  (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'write-dopesoft-transmission',
  '1.0.0',
  $skill$---
name: write-dopesoft-transmission
version: "1.0.0"
description: Turn one of the boss's own takes into a post on dopesoft.io/blog. His opinion is the input you cannot generate - get it from him, draft around it in his voice, and queue the publish for his approval. Publishing nothing is the common, correct outcome.
trigger_phrases:
  - write a blog post
  - draft a transmission
  - write a transmission
  - post this to the dopesoft blog
  - write this up for the blog
  - turn this into a post
  - publish this to dopesoft
inputs:
  - name: topic
    type: string
    required: false
    doc: what the post is about - usually already in this conversation as a news breakdown, a voice call, or something he just argued about. Rarely passed explicitly.
outputs:
  - name: outcome
    type: string
    doc: either the contract id of the queued publish plus the slug, or a plain statement that nothing was worth posting this week and why
risk_level: high
network_egress: api.github.com
confidence: 0.8
---

# Write a dopesoft transmission

The blog is called **Transmissions**. It is the boss's written record of what he
actually thinks. Post one, "For the record", states the premise in his own words:
he was never short on opinions, he was short a legible trail of them. Spoken
thinking evaporates; a document does not.

That tells you exactly what your job is, and what it is not. **You are the
stenographer and the editor, not the author.** He has the opinions. What he
lacks is the time to write them down. You close that gap. You do not close it by
inventing an opinion he never held.

## 1. The bar - apply it before you write a word

One question decides everything:

> **Does this post contain a claim only he could make?**

If the post would be equally true published under someone else's byline, it is a
summary, and summaries do not go on this blog. Kill it. A weekly recap of AI news
is the single worst thing you could publish here: it is derivative by
construction, it is what everyone else already wrote, and at volume it is exactly
what search engines treat as scaled content with no first-hand value. The site's
entire standing rests on posts like the iCIMS hackathon story - things that
happened to him, that exist nowhere else on the internet.

**Publishing nothing is a normal week.** Do not fill a slot. There is no
schedule to satisfy. Some weeks he has a real argument and some weeks he does
not, and shipping filler on the quiet weeks actively devalues the loud ones. If
nothing clears the bar, say so and stop. That is a successful run of this skill.

## 2. Get his take - this is the part you cannot skip

The news breakdown you already write him is **raw material, not a post**. It is
the input. His disagreement with it is the post. From "For the record":

> I build in AI and software every day, which means I read the same headlines you
> do and disagree with plenty of them. When a piece gets it wrong, or gets it
> right for the wrong reasons, I am going to say so.

So go get the disagreement:

- Surface the two or three items that might actually be worth his breath. Not ten.
- **Ask him.** Voice is best - he thinks out loud far better than he types, which
  is the whole reason the record was missing in the first place. A five minute
  call where he argues about one headline is worth more than anything you could
  draft alone.
- Search memory first (`memory_search` / `memory_recall`). He may have already
  argued this exact point weeks ago - a position he has held and repeated is a
  strong signal it is real, and a stronger post than a fresh reaction.
- If you cannot get his take, **do not proceed**. Park it and ask later. A post
  whose argument you supplied is a fabrication with his name on it, and that is
  worse than no post.

## 3. Draft it in his voice

Read the existing posts before you draft, every time - `content/blog/*.mdx` in
`dopesoft/dopesoft-v2`. Match what you find there, do not imitate a blog voice in
general.

What that voice actually is:
- First person, direct, argued rather than explained.
- Specific. Real numbers, real dates, real names, real rooms. "A CEO gasped" beats
  "leadership responded positively."
- Willing to be wrong on the record. Positions taken before the outcome was known
  are the point, not a risk to hedge away.
- The story carries the lesson. He does not append a section of takeaways.

Hard house rules from the repo:
- **Never an em dash.** Anywhere. Restructure the sentence instead.
- **dopesoft is always lowercase**, including at the start of a sentence.
- The byline is **Kai**, never "Khaya Malabie".

One more, because it is the tell that gives you away: vary your sentence rhythm.
The short-declarative-then-reversal cadence ("That is not a courage problem. It is
a record-keeping problem.") is already overused in the existing posts. A reader who
reads a lot of machine text clocks it instantly, and the share is what a small blog
lives on.

## 4. Publish

One file. `content/blog/<slug>.mdx` in `dopesoft/dopesoft-v2` on `main`. The
filename is the URL slug, so `for-the-record.mdx` serves at `/blog/for-the-record`.

The file starts with frontmatter, and it is an **ESM export, not YAML** - there is
no `---` block. Copy the shape from any existing post; if it does not match, the
site build fails and the post never appears:

    export const frontmatter = {
      title: 'The thing',
      description: 'One sentence, under 155 characters.',
      date: '2026-07-16',
      tags: ['essay', 'ai'],
    };

Then the body in normal markdown, headings at `##`.

Write it with `github__create_or_update_file`. That verb is gated, so it will not
just happen: it queues a Trust contract and the boss reads the full draft on his
phone before anything is published. **That is the design - do not try to route
around the gate, and never tell him a post is live when it is only queued.**
Report the contract id and the slug. He approves on his own schedule and it goes
live from there whether or not you are still in the session.

## Rules

- The bar is the whole skill: a claim only he could make, or no post.
- His take comes from him. Never manufacture an opinion and never smooth a vague
  one into a confident one.
- Nothing worth posting is a good week, not a failed run. Say so plainly and stop.
- Queued is not published. Report what actually happened.
- One post. Never batch a backlog into a single run to catch up.
$skill$,
  '',
  0.8,
  'manual'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('write-dopesoft-transmission', '1.0.0')
ON CONFLICT (skill_name) DO NOTHING;

COMMIT;

"""GEPA optimizer sidecar — minimal FastAPI server.

Inputs (POST /optimize):
    {
      "skill_name":  "weekly-standup-summary",
      "skill_md":    "<current SKILL.md text>",
      "traces":      [{"input": ..., "output": ..., "success": bool, "error": str?}, ...],
      "eval_set":    [{"input": ..., "expected": ...}, ...]   # optional
      "budget":      {"max_candidates": 6, "max_calls": 24}    # optional
    }

Outputs:
    {
      "candidates": [
        {"skill_md": "...", "score": 0.92, "size_chars": 1840, "rationale": "..."}, ...
      ],
      "model": "claude-haiku-4-5-20251001",
      "calls":  18,
      "elapsed_ms": 42190
    }

The Go side (core/internal/voyager/optimizer.go) calls /optimize, applies the
hard gates (test pass, size cap, semantic preservation), then routes the
winning variant through the Trust queue. This service never writes to disk,
never touches the database, never has credentials beyond ANTHROPIC_API_KEY.
"""

from __future__ import annotations

import os
import time
from typing import Any, Optional

import httpx
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field


# DSPy is intentionally imported lazily so the container starts fast even when
# the optimizer is rarely called.
def _load_dspy():
    import dspy  # noqa: WPS433
    return dspy


app = FastAPI(title="infinity-gepa", version="0.1.0")


class Trace(BaseModel):
    input: dict[str, Any] | str = Field(default_factory=dict)
    output: str = ""
    success: bool = True
    error: Optional[str] = None


class EvalCase(BaseModel):
    input: dict[str, Any] | str = Field(default_factory=dict)
    expected: str | dict[str, Any] = ""


class Budget(BaseModel):
    max_candidates: int = 6
    max_calls: int = 24


class OptimizeRequest(BaseModel):
    skill_name: str
    skill_md: str
    traces: list[Trace] = Field(default_factory=list)
    eval_set: list[EvalCase] = Field(default_factory=list)
    budget: Budget = Field(default_factory=Budget)
    model: str = "claude-haiku-4-5-20251001"


class Candidate(BaseModel):
    skill_md: str
    score: float
    size_chars: int
    rationale: str


class OptimizeResponse(BaseModel):
    candidates: list[Candidate]
    model: str
    calls: int
    elapsed_ms: int


@app.get("/health")
def health():
    return {"ok": True, "service": "gepa", "version": "0.1.0"}


@app.post("/optimize", response_model=OptimizeResponse)
def optimize(req: OptimizeRequest):
    api_key = os.environ.get("ANTHROPIC_API_KEY", "").strip()
    if not api_key:
        raise HTTPException(status_code=503, detail="ANTHROPIC_API_KEY not configured")
    if not req.skill_md.strip():
        raise HTTPException(status_code=400, detail="skill_md is empty")

    started = time.time()
    calls = 0
    candidates: list[Candidate] = []
    failures = [t for t in req.traces if not t.success or t.error]

    # Skip DSPy import unless we actually have failure traces to reflect on.
    # For instruction-only skills with no failures the right answer is
    # "no change" — return the original as the sole candidate.
    if not failures:
        candidates.append(
            Candidate(
                skill_md=req.skill_md,
                score=1.0,
                size_chars=len(req.skill_md),
                rationale="No failure traces supplied — proposing no change.",
            )
        )
        return OptimizeResponse(
            candidates=candidates,
            model=req.model,
            calls=0,
            elapsed_ms=int((time.time() - started) * 1000),
        )

    # Real GEPA path. We follow the documented Genetic-Pareto pattern:
    #   1. Reflective failure analysis — Haiku reads N failure traces and
    #      proposes a hypothesis for each ("the skill misses recent
    #      timestamps because the prompt does not anchor 'today'").
    #   2. Mutation — Haiku rewrites SKILL.md targeted at the hypothesis.
    #   3. Self-eval — Haiku scores the candidate against the eval_set or,
    #      when none was supplied, against the failure traces themselves
    #      ("would this revised skill have made the right call here?").
    #
    # We DO NOT install GEPA's full RL loop here — it is not needed for
    # Phase 1 SKILL.md optimization. The flow above is what Hermes Phase 1
    # ships. If it ever isn't enough, swap in dspy.Optimizer / GEPA proper.
    client = _AnthropicClient(api_key=api_key, model=req.model)

    failure_summary = _summarise_failures(client, req.skill_md, failures[:8])
    calls += 1

    for i in range(req.budget.max_candidates):
        if calls >= req.budget.max_calls:
            break
        candidate_md, rationale = client.mutate(req.skill_md, failure_summary, seed=i)
        calls += 1
        if calls >= req.budget.max_calls:
            score = 0.5  # eval skipped
        else:
            score = client.score(candidate_md, req.eval_set, failures[:4])
            calls += 1
        candidates.append(
            Candidate(
                skill_md=candidate_md,
                score=score,
                size_chars=len(candidate_md),
                rationale=rationale,
            )
        )

    # Sort: highest score first, ties broken by smaller size.
    candidates.sort(key=lambda c: (-c.score, c.size_chars))
    return OptimizeResponse(
        candidates=candidates,
        model=req.model,
        calls=calls,
        elapsed_ms=int((time.time() - started) * 1000),
    )


# ---------------------------------------------------------------------------
# Internals
# ---------------------------------------------------------------------------


class _AnthropicClient:
    """Thin wrapper over the messages API. We intentionally don't use the
    anthropic SDK here so the container image stays lean — httpx is enough."""

    BASE = "https://api.anthropic.com/v1/messages"

    def __init__(self, api_key: str, model: str):
        self.headers = {
            "x-api-key": api_key,
            "anthropic-version": "2023-06-01",
            "content-type": "application/json",
        }
        self.model = model
        self.client = httpx.Client(timeout=60)

    def _call(self, system: str, user: str, max_tokens: int = 1024) -> str:
        r = self.client.post(
            self.BASE,
            headers=self.headers,
            json={
                "model": self.model,
                "max_tokens": max_tokens,
                "system": system,
                "messages": [{"role": "user", "content": user}],
            },
        )
        r.raise_for_status()
        body = r.json()
        out_parts = [c.get("text", "") for c in body.get("content", []) if c.get("type") == "text"]
        return "\n".join(out_parts)

    def summarise(self, skill_md: str, failures: list[Trace]) -> str:
        system = (
            "You read failure traces of a skill and produce ONE paragraph "
            "naming the most likely root cause. No fluff, no apologies."
        )
        bullets = "\n".join(f"- {f.error or 'no error'} | output={f.output[:120]!r}" for f in failures)
        user = f"SKILL.md:\n{skill_md}\n\nFailures:\n{bullets}\n\nRoot cause:"
        return self._call(system, user, max_tokens=400)

    def mutate(self, skill_md: str, root_cause: str, seed: int) -> tuple[str, str]:
        system = (
            "You rewrite a SKILL.md to fix a known root cause. Preserve the "
            "frontmatter exactly. Output ONLY the new SKILL.md content with "
            "frontmatter — no commentary, no code fences."
        )
        user = (
            f"Root cause: {root_cause}\n"
            f"Variant seed: {seed}\n"
            f"Current SKILL.md:\n---\n{skill_md}\n---\n"
            "Rewrite:"
        )
        text = self._call(system, user, max_tokens=2048)
        rationale = f"variant {seed}: targeted at {root_cause[:160]}"
        return text.strip(), rationale

    def score(self, candidate_md: str, eval_set: list[EvalCase], failures: list[Trace]) -> float:
        # Cheap heuristic — Haiku rates the candidate on a 0-1 scale.
        system = (
            "You rate a SKILL.md against a set of cases on a 0.0-1.0 scale "
            "for likely correctness. Output ONLY a single decimal."
        )
        cases = []
        for c in eval_set[:4]:
            cases.append(f"input={c.input!r}\nexpected={c.expected!r}")
        for f in failures[:4]:
            cases.append(f"failure_input={f.input!r}\nerror={f.error!r}")
        user = (
            f"Candidate SKILL.md:\n---\n{candidate_md[:4000]}\n---\n\n"
            f"Cases:\n" + "\n\n".join(cases) + "\n\nScore:"
        )
        try:
            raw = self._call(system, user, max_tokens=8).strip()
            return max(0.0, min(1.0, float(raw)))
        except Exception:
            return 0.5


def _summarise_failures(client: _AnthropicClient, skill_md: str, failures: list[Trace]) -> str:
    if not failures:
        return ""
    return client.summarise(skill_md, failures)

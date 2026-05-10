"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import {
  IconRefresh,
  IconSearch,
  IconTopologyComplex,
  IconX,
  IconZoomReset,
} from "@tabler/icons-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";
import { fetchGraph, type GraphEdgeDTO, type GraphNodeDTO, type GraphResponse } from "@/lib/api";

// Knowledge graph viewer — force-directed SVG with no external dep. The
// dataset is small by design (capped server-side); a custom Verlet-style sim
// is plenty here and keeps the studio bundle lean.
//
// Layout: the SVG fills its container; Inspector slides in on node click.

type Sim = {
  nodes: SimNode[];
  edges: SimEdge[];
};

type SimNode = GraphNodeDTO & {
  x: number;
  y: number;
  vx: number;
  vy: number;
  fx: number | null; // pinned position when dragging
  fy: number | null;
  radius: number;
  color: string;
};

type SimEdge = Omit<GraphEdgeDTO, "source" | "target"> & {
  source: SimNode;
  target: SimNode;
};

// Type → color. Tier palette so it harmonizes with the rest of studio.
const TYPE_COLOR: Record<string, string> = {
  person: "#7dd3fc",      // sky-300
  project: "#a5b4fc",     // indigo-300
  file: "#fcd34d",        // amber-300
  concept: "#86efac",     // green-300
  decision: "#fda4af",    // rose-300
  error: "#f87171",       // red-400
  skill: "#c4b5fd",       // violet-300
  tool: "#5eead4",        // teal-300
  default: "#94a3b8",     // slate-400
};

const colorFor = (type: string) => TYPE_COLOR[type] ?? TYPE_COLOR.default;

export function KnowledgeGraphPanel() {
  const containerRef = useRef<HTMLDivElement>(null);
  const svgRef = useRef<SVGSVGElement>(null);
  const rafRef = useRef<number | null>(null);
  const simRef = useRef<Sim | null>(null);

  const [data, setData] = useState<GraphResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [filterType, setFilterType] = useState<string>("");
  const [search, setSearch] = useState("");
  const [selected, setSelected] = useState<SimNode | null>(null);
  const [size, setSize] = useState({ w: 800, h: 600 });
  const [transform, setTransform] = useState({ x: 0, y: 0, k: 1 });
  const [tick, setTick] = useState(0); // forces re-render on each sim step

  const draggingRef = useRef<{ id: string; offsetX: number; offsetY: number } | null>(null);
  const panRef = useRef<{ x: number; y: number; tx: number; ty: number } | null>(null);

  async function load() {
    setLoading(true);
    const res = await fetchGraph({ limit: 120, type: filterType || undefined });
    setData(res);
    setLoading(false);
  }

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [filterType]);

  // Track container size for responsive sim bounds.
  useEffect(() => {
    if (!containerRef.current) return;
    const el = containerRef.current;
    const ro = new ResizeObserver(() => {
      const rect = el.getBoundingClientRect();
      setSize({ w: Math.max(rect.width, 200), h: Math.max(rect.height, 200) });
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  // Build/refresh the simulation when new data arrives.
  useEffect(() => {
    if (!data) return;
    const cx = size.w / 2;
    const cy = size.h / 2;
    const nodesById = new Map<string, SimNode>();
    const nodes: SimNode[] = data.nodes.map((n, i) => {
      // Spread initial positions on a circle so the sim doesn't start with
      // every node stacked at center — that produces stable, consistent layout.
      const angle = (i / Math.max(data.nodes.length, 1)) * Math.PI * 2;
      const radius = 6 + Math.min(14, n.degree * 1.2);
      const sn: SimNode = {
        ...n,
        x: cx + Math.cos(angle) * 180,
        y: cy + Math.sin(angle) * 180,
        vx: 0,
        vy: 0,
        fx: null,
        fy: null,
        radius,
        color: colorFor(n.type),
      };
      nodesById.set(n.id, sn);
      return sn;
    });
    const edges: SimEdge[] = [];
    for (const e of data.edges) {
      const s = nodesById.get(e.source);
      const t = nodesById.get(e.target);
      if (!s || !t) continue;
      edges.push({ ...e, source: s, target: t });
    }
    simRef.current = { nodes, edges };
    setSelected(null);
    setTick((t) => t + 1);
  }, [data, size.w, size.h]);

  // Force-simulation tick. Constant-time per frame (O(N²) repulsion + O(E)
  // attraction). At our N≤120 cap this is ~30k ops/frame → trivial.
  useEffect(() => {
    let alpha = 1; // cooling schedule
    const step = () => {
      const sim = simRef.current;
      if (!sim || draggingRef.current) {
        rafRef.current = requestAnimationFrame(step);
        return;
      }
      const cx = size.w / 2;
      const cy = size.h / 2;

      // Repulsion (Coulomb-ish) — every pair pushes apart.
      const k = 4500 * alpha;
      for (let i = 0; i < sim.nodes.length; i++) {
        const a = sim.nodes[i];
        for (let j = i + 1; j < sim.nodes.length; j++) {
          const b = sim.nodes[j];
          const dx = a.x - b.x;
          const dy = a.y - b.y;
          const d2 = dx * dx + dy * dy + 0.01;
          const f = k / d2;
          const d = Math.sqrt(d2);
          const fx = (dx / d) * f;
          const fy = (dy / d) * f;
          a.vx += fx;
          a.vy += fy;
          b.vx -= fx;
          b.vy -= fy;
        }
      }

      // Attraction along edges (Hooke's law toward rest length 80).
      for (const e of sim.edges) {
        const dx = e.target.x - e.source.x;
        const dy = e.target.y - e.source.y;
        const d = Math.sqrt(dx * dx + dy * dy) + 0.01;
        const f = (d - 80) * 0.04 * alpha;
        const fx = (dx / d) * f;
        const fy = (dy / d) * f;
        e.source.vx += fx;
        e.source.vy += fy;
        e.target.vx -= fx;
        e.target.vy -= fy;
      }

      // Center gravity + integrate.
      for (const n of sim.nodes) {
        n.vx += (cx - n.x) * 0.005 * alpha;
        n.vy += (cy - n.y) * 0.005 * alpha;
        n.vx *= 0.85; // damping
        n.vy *= 0.85;
        if (n.fx !== null && n.fy !== null) {
          n.x = n.fx;
          n.y = n.fy;
        } else {
          n.x += n.vx;
          n.y += n.vy;
        }
      }

      alpha = Math.max(0.05, alpha * 0.992); // never freeze fully — keeps it lively under interaction
      setTick((t) => (t + 1) % 1_000_000);
      rafRef.current = requestAnimationFrame(step);
    };
    rafRef.current = requestAnimationFrame(step);
    return () => {
      if (rafRef.current) cancelAnimationFrame(rafRef.current);
    };
  }, [size.w, size.h]);

  // ---- Pointer handling --------------------------------------------------
  const screenToWorld = (clientX: number, clientY: number) => {
    const svg = svgRef.current;
    if (!svg) return { x: 0, y: 0 };
    const rect = svg.getBoundingClientRect();
    const sx = clientX - rect.left;
    const sy = clientY - rect.top;
    return { x: (sx - transform.x) / transform.k, y: (sy - transform.y) / transform.k };
  };

  const onPointerDownNode = (e: React.PointerEvent<SVGCircleElement>, n: SimNode) => {
    e.stopPropagation();
    (e.target as SVGElement).setPointerCapture(e.pointerId);
    const w = screenToWorld(e.clientX, e.clientY);
    n.fx = n.x;
    n.fy = n.y;
    draggingRef.current = { id: n.id, offsetX: w.x - n.x, offsetY: w.y - n.y };
    setSelected(n);
  };

  const onPointerMoveSvg = (e: React.PointerEvent<SVGSVGElement>) => {
    if (draggingRef.current) {
      const sim = simRef.current;
      if (!sim) return;
      const node = sim.nodes.find((n) => n.id === draggingRef.current!.id);
      if (!node) return;
      const w = screenToWorld(e.clientX, e.clientY);
      node.fx = w.x - draggingRef.current.offsetX;
      node.fy = w.y - draggingRef.current.offsetY;
      setTick((t) => t + 1);
      return;
    }
    if (panRef.current) {
      const dx = e.clientX - panRef.current.x;
      const dy = e.clientY - panRef.current.y;
      setTransform((t) => ({ ...t, x: panRef.current!.tx + dx, y: panRef.current!.ty + dy }));
    }
  };

  const onPointerUp = () => {
    if (draggingRef.current) {
      const sim = simRef.current;
      if (sim) {
        const node = sim.nodes.find((n) => n.id === draggingRef.current!.id);
        if (node) {
          node.fx = null;
          node.fy = null;
        }
      }
      draggingRef.current = null;
    }
    panRef.current = null;
  };

  const onPointerDownSvg = (e: React.PointerEvent<SVGSVGElement>) => {
    if (e.target === svgRef.current) {
      panRef.current = { x: e.clientX, y: e.clientY, tx: transform.x, ty: transform.y };
      setSelected(null);
    }
  };

  const onWheel = (e: React.WheelEvent<SVGSVGElement>) => {
    e.preventDefault();
    const factor = e.deltaY < 0 ? 1.12 : 1 / 1.12;
    const svg = svgRef.current;
    if (!svg) return;
    const rect = svg.getBoundingClientRect();
    const sx = e.clientX - rect.left;
    const sy = e.clientY - rect.top;
    setTransform((t) => {
      const newK = Math.max(0.3, Math.min(3, t.k * factor));
      const newX = sx - ((sx - t.x) / t.k) * newK;
      const newY = sy - ((sy - t.y) / t.k) * newK;
      return { x: newX, y: newY, k: newK };
    });
  };

  const resetView = () => setTransform({ x: 0, y: 0, k: 1 });

  // Node visibility filter (search). Edges to invisible nodes drop.
  const sim = simRef.current;
  const visibleNodes = useMemo(() => {
    if (!sim) return [];
    const q = search.trim().toLowerCase();
    if (!q) return sim.nodes;
    return sim.nodes.filter((n) => n.name.toLowerCase().includes(q) || n.type.toLowerCase().includes(q));
    // tick is intentionally part of deps so node positions update during sim
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sim, search, tick]);
  const visibleIds = useMemo(() => new Set(visibleNodes.map((n) => n.id)), [visibleNodes]);
  const visibleEdges = useMemo(() => {
    if (!sim) return [];
    return sim.edges.filter((e) => visibleIds.has(e.source.id) && visibleIds.has(e.target.id));
  }, [sim, visibleIds]);

  return (
    <div className="flex min-h-0 flex-1 flex-col bg-background">
      {/* Toolbar */}
      <div className="flex flex-wrap items-center gap-2 border-b px-3 py-2">
        <div className="flex items-center gap-1.5">
          <IconTopologyComplex className="size-4 text-muted-foreground" aria-hidden />
          <span className="text-[11px] font-mono uppercase tracking-wide text-muted-foreground">
            graph
          </span>
        </div>
        <div className="text-[11px] text-muted-foreground">
          {data ? (
            <>
              {visibleNodes.length}/{data.total_nodes} nodes · {visibleEdges.length}/{data.total_edges}{" "}
              edges
            </>
          ) : (
            "loading"
          )}
        </div>
        <div className="ml-auto flex flex-wrap items-center gap-1.5">
          <div className="relative">
            <IconSearch
              className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground"
              aria-hidden
            />
            <Input
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="filter"
              className="h-8 w-32 pl-7 text-xs sm:w-40"
              inputMode="search"
            />
          </div>
          <Button
            type="button"
            size="icon"
            variant="ghost"
            onClick={resetView}
            aria-label="Reset view"
            className="h-8 w-8"
            title="Reset zoom & pan"
          >
            <IconZoomReset className="size-4" />
          </Button>
          <Button
            type="button"
            size="icon"
            variant="ghost"
            onClick={() => load()}
            aria-label="Refresh"
            disabled={loading}
            className="h-8 w-8"
          >
            <IconRefresh className="size-4" />
          </Button>
        </div>
      </div>

      {/* Type filter chips */}
      {data && data.node_types.length > 0 && (
        <div className="flex flex-wrap items-center gap-1 border-b px-3 py-1.5">
          <button
            onClick={() => setFilterType("")}
            className={cn(
              "rounded-full border px-2 py-0.5 font-mono text-[10px] uppercase tracking-wide",
              filterType === ""
                ? "border-info bg-info/10 text-info"
                : "border-transparent bg-muted text-muted-foreground hover:bg-accent",
            )}
          >
            all
          </button>
          {data.node_types.map((t) => (
            <button
              key={t}
              onClick={() => setFilterType(t)}
              className={cn(
                "flex items-center gap-1 rounded-full border px-2 py-0.5 font-mono text-[10px] uppercase tracking-wide",
                filterType === t
                  ? "border-info bg-info/10 text-info"
                  : "border-transparent bg-muted text-muted-foreground hover:bg-accent",
              )}
            >
              <span
                className="inline-block size-2 rounded-full"
                style={{ backgroundColor: colorFor(t) }}
                aria-hidden
              />
              {t}
            </button>
          ))}
        </div>
      )}

      {/* Canvas */}
      <div ref={containerRef} className="relative min-h-0 flex-1 overflow-hidden bg-background">
        {!data ? (
          <div className="flex h-full items-center justify-center text-xs text-muted-foreground">
            loading…
          </div>
        ) : data.total_nodes === 0 ? (
          <div className="flex h-full items-center justify-center px-4 text-center text-xs text-muted-foreground">
            No graph nodes yet. They appear as the agent&apos;s compressor extracts entities from
            your conversations. Auto-compress is on, so just keep talking.
          </div>
        ) : (
          <svg
            ref={svgRef}
            width={size.w}
            height={size.h}
            className="block touch-none"
            onPointerMove={onPointerMoveSvg}
            onPointerUp={onPointerUp}
            onPointerCancel={onPointerUp}
            onPointerDown={onPointerDownSvg}
            onWheel={onWheel}
            style={{ cursor: panRef.current ? "grabbing" : "grab" }}
          >
            <defs>
              <marker
                id="arrow"
                viewBox="0 -5 10 10"
                refX="10"
                refY="0"
                markerWidth="6"
                markerHeight="6"
                orient="auto"
              >
                <path d="M0,-4L10,0L0,4" className="fill-muted-foreground/50" />
              </marker>
            </defs>
            <g transform={`translate(${transform.x},${transform.y}) scale(${transform.k})`}>
              {/* Edges */}
              {visibleEdges.map((e) => (
                <g key={e.id}>
                  <line
                    x1={e.source.x}
                    y1={e.source.y}
                    x2={e.target.x}
                    y2={e.target.y}
                    className="stroke-muted-foreground/30"
                    strokeWidth={Math.max(0.6, e.confidence * 1.6)}
                    markerEnd="url(#arrow)"
                  />
                </g>
              ))}
              {/* Nodes */}
              {visibleNodes.map((n) => {
                const isSelected = selected?.id === n.id;
                return (
                  <g key={n.id}>
                    <circle
                      cx={n.x}
                      cy={n.y}
                      r={n.radius}
                      fill={n.color}
                      fillOpacity={n.stale ? 0.3 : 0.9}
                      stroke={isSelected ? "currentColor" : "var(--background)"}
                      strokeWidth={isSelected ? 2.5 : 1.5}
                      className={cn(isSelected ? "text-info" : "text-background", "cursor-pointer")}
                      onPointerDown={(e) => onPointerDownNode(e, n)}
                    />
                    {(transform.k > 0.7 || isSelected) && (
                      <text
                        x={n.x}
                        y={n.y + n.radius + 11}
                        textAnchor="middle"
                        className="pointer-events-none select-none fill-foreground/80 text-[9px] font-medium"
                      >
                        {n.name.length > 22 ? n.name.slice(0, 22) + "…" : n.name}
                      </text>
                    )}
                  </g>
                );
              })}
            </g>
          </svg>
        )}

        {/* Inspector overlay */}
        {selected && (
          <div className="absolute right-3 top-3 z-10 w-64 max-w-[calc(100%-1.5rem)] rounded-xl border bg-card/95 p-3 shadow-lg backdrop-blur">
            <div className="flex items-start justify-between gap-2">
              <div className="min-w-0">
                <div className="flex items-center gap-1.5">
                  <span
                    className="inline-block size-2 rounded-full"
                    style={{ backgroundColor: selected.color }}
                    aria-hidden
                  />
                  <span className="font-mono text-[10px] uppercase tracking-wide text-muted-foreground">
                    {selected.type}
                  </span>
                  {selected.stale && (
                    <span className="rounded-full bg-warning/10 px-1.5 py-0 font-mono text-[9px] uppercase text-warning">
                      stale
                    </span>
                  )}
                </div>
                <div className="mt-1 break-words text-sm font-semibold text-foreground">
                  {selected.name}
                </div>
              </div>
              <Button
                size="icon"
                variant="ghost"
                onClick={() => setSelected(null)}
                aria-label="Close"
                className="size-6"
              >
                <IconX className="size-3.5" />
              </Button>
            </div>
            <dl className="mt-2 grid grid-cols-2 gap-y-1 text-[11px]">
              <dt className="text-muted-foreground">degree</dt>
              <dd className="text-right font-mono tabular-nums">{selected.degree}</dd>
              <dt className="text-muted-foreground">id</dt>
              <dd className="truncate text-right font-mono">{selected.id.slice(0, 8)}</dd>
            </dl>
            {selected.metadata != null && (
              <pre className="mt-2 max-h-40 overflow-auto rounded-md bg-muted/40 p-2 font-mono text-[10px] leading-snug">
                {JSON.stringify(selected.metadata, null, 2)}
              </pre>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

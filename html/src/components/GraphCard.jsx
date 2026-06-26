import { useEffect, useRef, useState, useCallback } from "react";
import ForceGraph2D from "react-force-graph-2d";

// ── Node colors ───────────────────────────────────────────────────────────────
const TYPE_COLOR = {
  File: "#6366f1",
  Class: "#0ea5e9",
  Function: "#10b981",
  Method: "#f59e0b",
  Module: "#94a3b8",
  Variable: "#e879f9",
};
const DEFAULT_COLOR = "#94a3b8";

// ── Convert backend graph → force graph format ───────────────────────────────
function toForceGraph(raw = {}) {
  const nodesObj = raw.nodes ?? {};
  const edges = raw.edges ?? [];

  const nodes = Object.values(nodesObj).map((n) => ({
    id: n.id,
    name: n.name,
    type: n.type,
    color: TYPE_COLOR[n.type] ?? DEFAULT_COLOR,
  }));

  const links = edges.map((e) => ({
    source: e.from,
    target: e.to,
    edgeType: e.type,
  }));

  return { nodes, links };
}

// ── Edge styling ──────────────────────────────────────────────────────────────
const EDGE_OPACITY = {
  Imports: 0.55,
  Contains: 0.25,
  Calls: 0.6,
  Instantiates: 0.5,
  Inherits: 0.7,
  Decorates: 0.5,
};

export default function GraphCard({ repoFullName }) {
  const [jobId, setJobId] = useState(null);
  const [status, setStatus] = useState("idle");
  const [graph, setGraph] = useState(null);
  const [error, setError] = useState(null);
  const [selected, setSelected] = useState(null);

  const pollRef = useRef(null);
  const fgRef = useRef(null);
  const wrapRef = useRef(null);

  const [dims, setDims] = useState({ w: 800, h: 560 });
  const roRef = useRef(null);

  // ── Resize observer ─────────────────────────────────────────────────────────
  useEffect(() => {
    if (!wrapRef.current) return;

    roRef.current?.disconnect();

    const ro = new ResizeObserver(([entry]) => {
      setDims({
        w: entry.contentRect.width,
        h: entry.contentRect.height,
      });
    });

    ro.observe(wrapRef.current);
    roRef.current = ro;

    return () => ro.disconnect();
  }, []);

  // ── Start indexing ──────────────────────────────────────────────────────────
  const startIndex = useCallback(async () => {
    if (!repoFullName) {
      setStatus("failed");
      setError("No repository specified");
      return;
    }

    setStatus("pending");
    setError(null);
    setGraph(null);
    setJobId(null);

    try {
      const res = await fetch("/api/index", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({ repo: repoFullName }),
      });

      if (!res.ok) throw new Error(await res.text());

      const data = await res.json();
      setJobId(data.jobId);
    } catch (e) {
      setStatus("failed");
      setError(e.message);
    }
  }, [repoFullName]);

  // ── Poll job ────────────────────────────────────────────────────────────────
  useEffect(() => {
    if (!jobId) return;

    if (pollRef.current) clearInterval(pollRef.current);

    const poll = async () => {
      try {
        const res = await fetch(`/api/graph/${jobId}`, {
          credentials: "include",
        });
        if (!res.ok) throw new Error(await res.text());

        const data = await res.json();

        if (data.status === "done") {
          clearInterval(pollRef.current);

          const graphData =
            typeof data.graph === "string"
              ? JSON.parse(data.graph)
              : data.graph;

          setGraph(toForceGraph(graphData));
          setStatus("done");
        }

        if (data.status === "failed") {
          clearInterval(pollRef.current);
          setStatus("failed");
          setError(data.error || "Indexing failed");
        }
      } catch (e) {
        clearInterval(pollRef.current);
        setStatus("failed");
        setError(e.message);
      }
    };

    pollRef.current = setInterval(poll, 2000);
    poll();

    return () => clearInterval(pollRef.current);
  }, [jobId]);

  // ── Node rendering ─────────────────────────────────────────────────────────
  const paintNode = useCallback(
    (node, ctx, globalScale) => {
      const isSelected = selected && selected.id === node.id;
      const r = isSelected ? 7 : 5;

      ctx.beginPath();
      ctx.arc(node.x, node.y, r, 0, 2 * Math.PI);
      ctx.fillStyle = node.color;
      ctx.fill();

      const showLabel = globalScale > 1.5 || isSelected;

      if (showLabel) {
        const fs = Math.max(10 / globalScale, 3);
        ctx.font = `${fs}px sans-serif`;
        ctx.fillStyle = "#f8fafc";
        ctx.textAlign = "center";
        ctx.textBaseline = "middle";
        ctx.fillText(node.name, node.x, node.y + r + fs * 0.8);
      }
    },
    [selected]
  );

  // ── Legend ────────────────────────────────────────────────────────────────
  const Legend = () => (
    <div
      style={{
        position: "absolute",
        bottom: 12,
        left: 12,
        background: "rgba(15,23,42,0.75)",
        borderRadius: 8,
        padding: "8px 12px",
        display: "flex",
        flexDirection: "column",
        gap: 4,
      }}
    >
      {Object.entries(TYPE_COLOR).map(([type, color]) => (
        <div
          key={type}
          style={{ display: "flex", alignItems: "center", gap: 6 }}
        >
          <div
            style={{
              width: 10,
              height: 10,
              borderRadius: "50%",
              background: color,
            }}
          />
          <span style={{ fontSize: 11, color: "#cbd5e1" }}>{type}</span>
        </div>
      ))}
    </div>
  );

  // ── Tooltip ───────────────────────────────────────────────────────────────
  const Tooltip = () =>
    selected ? (
      <div
        style={{
          position: "absolute",
          top: 12,
          right: 12,
          background: "rgba(15,23,42,0.85)",
          borderRadius: 8,
          padding: "10px 14px",
          maxWidth: 280,
        }}
      >
        <div
          style={{
            fontSize: 11,
            color: TYPE_COLOR[selected.type] ?? DEFAULT_COLOR,
          }}
        >
          {selected.type}
        </div>
        <div style={{ fontSize: 13, color: "#f1f5f9" }}>
          {selected.name}
        </div>
        <div style={{ fontSize: 10, color: "#64748b" }}>
          {selected.id}
        </div>
      </div>
    ) : null;

  // ── Render ────────────────────────────────────────────────────────────────
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
      {/* Header */}
      <div
        style={{
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
        }}
      >
        <div>
          <div style={{ fontSize: 15, fontWeight: 500 }}>
            {repoFullName || "No repo selected"}
          </div>

          {graph && (
            <div style={{ fontSize: 12, color: "#94a3b8" }}>
              {graph.nodes.length} nodes · {graph.links.length} edges
            </div>
          )}
        </div>

        <button
          onClick={startIndex}
          disabled={status === "pending" || !repoFullName}
          style={{
            padding: "6px 14px",
            borderRadius: 6,
            border: "none",
            background: !repoFullName
              ? "#334155"
              : status === "pending"
              ? "#334155"
              : "#3b82f6",
            color: "#fff",
            cursor: !repoFullName || status === "pending"
              ? "not-allowed"
              : "pointer",
          }}
        >
          {status === "pending"
            ? "Indexing..."
            : !repoFullName
            ? "Select a repo"
            : "Index repo"}
        </button>
      </div>

      {/* Status */}
      {status === "idle" && (
        <div style={{ fontSize: 13, color: "#94a3b8" }}>
          Click index to build graph
        </div>
      )}

      {status === "pending" && (
        <div style={{ fontSize: 13, color: "#94a3b8" }}>
          Processing repository...
        </div>
      )}

      {status === "failed" && (
        <div style={{ fontSize: 13, color: "#ef4444" }}>
          {error}
        </div>
      )}

      {/* Graph */}
      {status === "done" && graph && (
        <div
          ref={wrapRef}
          style={{
            position: "relative",
            width: "100%",
            height: 560,
            background: "#0f172a",
            borderRadius: 10,
            overflow: "hidden",
          }}
        >
          <ForceGraph2D
            ref={fgRef}
            width={dims.w}
            height={dims.h}
            graphData={graph}
            nodeCanvasObject={paintNode}
            onNodeHover={setSelected}
            onNodeClick={(node) => {
              fgRef.current?.centerAt(node.x, node.y, 400);
              fgRef.current?.zoom(4, 400);
            }}
            linkColor={(link) => {
              const opacity =
                EDGE_OPACITY[link.edgeType] ?? 0.3;
              return `rgba(148,163,184,${opacity})`;
            }}
            linkWidth={0.5}
            cooldownTicks={120}
          />

          <Legend />
          <Tooltip />
        </div>
      )}
    </div>
  );
}

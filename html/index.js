const nodes = [
    { id: "api-gateway",  x: 0.50, y: 0.12, state: "healthy" },
    { id: "payment-svc",  x: 0.18, y: 0.28, state: "failing" },
    { id: "order-svc",    x: 0.38, y: 0.28, state: "healthy" },
    { id: "user-svc",     x: 0.65, y: 0.28, state: "healthy" },
    { id: "search-svc",   x: 0.82, y: 0.28, state: "healthy" },
    { id: "db-pool",      x: 0.12, y: 0.55, state: "root"    },
    { id: "auth-svc",     x: 0.30, y: 0.55, state: "healthy" },
    { id: "inventory",    x: 0.44, y: 0.55, state: "healthy" },
    { id: "notify-svc",   x: 0.56, y: 0.55, state: "healthy" },
    { id: "rate-limiter", x: 0.66, y: 0.55, state: "healthy" },
    { id: "load-bal",     x: 0.76, y: 0.55, state: "healthy" },
    { id: "profile-sv",   x: 0.60, y: 0.72, state: "healthy" },
    { id: "session-svc",  x: 0.70, y: 0.72, state: "healthy" },
    { id: "elastic",      x: 0.84, y: 0.55, state: "healthy" },
    { id: "postgres",     x: 0.08, y: 0.80, state: "root"    },
    { id: "redis",        x: 0.20, y: 0.80, state: "healthy" },
    { id: "email-svc",    x: 0.38, y: 0.80, state: "healthy" },
    { id: "sms-svc",      x: 0.50, y: 0.80, state: "healthy" },
    { id: "cdn-svc",      x: 0.60, y: 0.80, state: "healthy" },
    { id: "cache-svc",    x: 0.72, y: 0.80, state: "healthy" },
  ];

  const edges = [
    ["api-gateway","payment-svc"],["api-gateway","order-svc"],
    ["api-gateway","user-svc"],["api-gateway","search-svc"],
    ["payment-svc","db-pool"],["payment-svc","auth-svc"],
    ["order-svc","inventory"],["order-svc","notify-svc"],["order-svc","cdn-svc"],
    ["user-svc","rate-limiter"],["user-svc","load-bal"],
    ["user-svc","profile-sv"],["user-svc","session-svc"],
    ["search-svc","elastic"],["db-pool","postgres"],["db-pool","redis"],
    ["notify-svc","email-svc"],["notify-svc","sms-svc"],
    ["load-bal","cache-svc"],
  ];

  const STATE = {
    failing: { fill:"#fee2e2", stroke:"#ef4444", text:"#991b1b" },
    root:    { fill:"#fee2e2", stroke:"#f97316", text:"#9a3412" },
    healthy: { fill:"#f1f5f9", stroke:"#cbd5e1", text:"#475569" },
  };

  function draw() {
    const container = document.getElementById("graph");
    if (!container) return;
    const W = container.clientWidth || 800;
    const H = container.clientHeight || 220;
    const PAD = { x: 20, y: 16 };
    const ns = "http://www.w3.org/2000/svg";
    const svg = document.createElementNS(ns, "svg");
    svg.setAttribute("viewBox", `0 0 ${W} ${H}`);

    const pos = {};
    nodes.forEach(n => {
      pos[n.id] = {
        x: PAD.x + n.x * (W - PAD.x * 2),
        y: PAD.y + n.y * (H - PAD.y * 2),
      };
    });

    edges.forEach(([a, b]) => {
      const p1 = pos[a], p2 = pos[b];
      if (!p1 || !p2) return;
      const line = document.createElementNS(ns, "line");
      line.setAttribute("x1", p1.x); line.setAttribute("y1", p1.y);
      line.setAttribute("x2", p2.x); line.setAttribute("y2", p2.y);
      line.setAttribute("stroke", "#e2e8f0");
      line.setAttribute("stroke-width", "1");
      svg.appendChild(line);
    });

    [["payment-svc","db-pool"],["db-pool","postgres"]].forEach(([a, b]) => {
      const p1 = pos[a], p2 = pos[b];
      const line = document.createElementNS(ns, "line");
      line.setAttribute("x1", p1.x); line.setAttribute("y1", p1.y);
      line.setAttribute("x2", p2.x); line.setAttribute("y2", p2.y);
      line.setAttribute("stroke", "#f97316");
      line.setAttribute("stroke-width", "1.5");
      line.setAttribute("stroke-dasharray", "4 2");
      svg.appendChild(line);
    });

    nodes.forEach(n => {
      const { x, y } = pos[n.id];
      const s = STATE[n.state];
      const w = Math.max(n.id.length * 6.2 + 16, 48);
      const h = 20;
      const rect = document.createElementNS(ns, "rect");
      rect.setAttribute("x", x - w/2); rect.setAttribute("y", y - h/2);
      rect.setAttribute("width", w);    rect.setAttribute("height", h);
      rect.setAttribute("rx", "4");
      rect.setAttribute("fill", s.fill);
      rect.setAttribute("stroke", s.stroke);
      rect.setAttribute("stroke-width", n.state !== "healthy" ? "1.5" : "1");
      svg.appendChild(rect);

      const text = document.createElementNS(ns, "text");
      text.setAttribute("x", x); text.setAttribute("y", y + 4);
      text.setAttribute("text-anchor", "middle");
      text.setAttribute("font-size", "10");
      text.setAttribute("font-family", "monospace");
      text.setAttribute("fill", s.text);
      text.textContent = n.id;
      svg.appendChild(text);
    });

    container.innerHTML = "";
    container.appendChild(svg);
  }

  draw();
  window.addEventListener("resize", draw);
(() => {
  pdfjsLib.GlobalWorkerOptions.workerSrc =
    "https://cdnjs.cloudflare.com/ajax/libs/pdf.js/3.11.174/pdf.worker.min.js";

  const role = new URLSearchParams(location.search).get("view") === "presentation" ? "presentation" : "presenter";
  document.body.classList.add(role);
  document.title = role === "presentation" ? "Presentation" : "Presenter view";

  const $ = (id) => document.getElementById(id);
  const bc = new BroadcastChannel("typst-present");

  let pdf = null, version = null, notes = [], durations = [], page = 1, blank = false;
  let t0 = null, pausedElapsed = null;
  let slideT0 = null;
  let actuals = []; // actuals[i] = seconds spent on slide i+1, last time it was left
  let knownGen = null, browsePath = null;
  let zoom = 1, panX = 0, panY = 0; // zoom centered via panX/panY, both in [0,1] fractions of slide size

  const MIN_ZOOM = 1, MAX_ZOOM = 6;
  function clampPan() {
    const half = 0.5 / zoom;
    panX = Math.max(-0.5 + half, Math.min(0.5 - half, panX));
    panY = Math.max(-0.5 + half, Math.min(0.5 - half, panY));
  }
  function applyZoom(canvas) {
    if (zoom === 1) {
      canvas.style.transform = "translate(-50%, -50%)";
    } else {
      const tx = -panX * canvas.clientWidth * zoom;
      const ty = -panY * canvas.clientHeight * zoom;
      canvas.style.transform =
        `translate(-50%, -50%) translate(${tx}px, ${ty}px) scale(${zoom})`;
    }
  }

  // ---------- rendering ----------
  async function draw(canvas, container, n) {
    const token = (canvas._token = (canvas._token || 0) + 1);
    if (!pdf || n < 1 || n > pdf.numPages) {
      canvas.style.display = "none";
      return;
    }
    const pg = await pdf.getPage(n);
    if (token !== canvas._token) return;
    const base = pg.getViewport({ scale: 1 });
    const r = container.getBoundingClientRect();
    if (!r.width || !r.height) return;
    const s = Math.min(r.width / base.width, r.height / base.height);
    const dpr = window.devicePixelRatio || 1;
    const vp = pg.getViewport({ scale: s * dpr });
    const off = document.createElement("canvas");   // render off-screen => no flicker
    off.width = Math.floor(vp.width);
    off.height = Math.floor(vp.height);
    await pg.render({ canvasContext: off.getContext("2d"), viewport: vp }).promise;
    if (token !== canvas._token) return;
    canvas.width = off.width;
    canvas.height = off.height;
    canvas.style.width = off.width / dpr + "px";
    canvas.style.height = off.height / dpr + "px";
    canvas.style.display = "block";
    canvas.getContext("2d").drawImage(off, 0, 0);
  }

  function refresh() {
    if (role === "presentation") {
      $("aCanvas").style.visibility = blank ? "hidden" : "visible";
      draw($("aCanvas"), $("aStage"), page).then(() => applyZoom($("aCanvas")));
    } else {
      draw($("cCanvas"), $("cur"), page).then(() => applyZoom($("cCanvas")));
      draw($("nCanvas"), $("nxt"), page + 1);
      $("notes").textContent = notes[page - 1] || "";
      $("counter").textContent = pdf ? `${page} / ${pdf.numPages}` : "– / –";
      $("btnBlank").style.borderColor = blank ? "var(--acc)" : "";
      $("btnZoomReset").textContent = `Zoom ${Math.round(zoom * 100)}%`;
      $("cur").classList.toggle("zoomable", zoom === 1);
      $("cur").classList.toggle("zoomed", zoom !== 1);
    }
  }

  // ---------- navigation + sync ----------
  function broadcastState() {
    bc.postMessage({ type: "state", page, blank, zoom, panX, panY });
  }
  function go(n, remote = false) {
    if (!pdf) return;
    n = Math.max(1, Math.min(pdf.numPages, n));
    if (n === page) return;
    if (role === "presenter" && !remote && slideT0 !== null) {
      actuals[page - 1] = Math.round((Date.now() - slideT0) / 1000);
    }
    page = n;
    if (!remote) {
      zoom = 1; panX = 0; panY = 0; // reset zoom on slide change
      broadcastState();
      if (role === "presenter" && t0 === null && pausedElapsed === null) t0 = Date.now();
      if (role === "presenter") slideT0 = Date.now();
    }
    refresh();
    if (role === "presenter") refreshHistory();
  }
  function setBlank(v, remote = false) {
    blank = v;
    if (!remote) broadcastState();
    refresh();
  }
  function setZoom(z, px, py, remote = false) {
    zoom = Math.max(MIN_ZOOM, Math.min(MAX_ZOOM, z));
    panX = px; panY = py;
    clampPan();
    if (!remote) broadcastState();
    refresh();
  }
  bc.onmessage = (e) => {
    const m = e.data;
    if (m.type === "state") {
      blank = m.blank;
      zoom = m.zoom || 1; panX = m.panX || 0; panY = m.panY || 0;
      if (pdf) { page = Math.max(1, Math.min(pdf.numPages, m.page)); } else { page = m.page; }
      refresh();
    } else if (m.type === "hello") {
      broadcastState();
    }
  };

  // ---------- loading / live reload ----------
  async function poll() {
    try {
      const st = await (await fetch("/status", { cache: "no-store" })).json();

      if (role === "presenter" && st.file === "") {
        if (knownGen !== null) { knownGen = st.gen; }
        openPicker();
        $("err").style.display = "none";
        $("msg").style.display = "none";
        setTimeout(poll, 700);
        return;
      }

      if (knownGen !== null && st.gen !== knownGen) {
        page = 1; t0 = null; pausedElapsed = null; slideT0 = null; blank = false; zoom = 1; panX = 0; panY = 0;
        actuals = [];
        $("timer").textContent = "00:00";
        updatePauseButton();
        broadcastState();
        refresh();
        closePicker();
        if (role === "presenter") refreshHistory();
      }
      knownGen = st.gen;

      const err = $("err");
      err.style.display = st.error ? "block" : "none";
      err.textContent = st.error;
      $("msg").style.display = st.has_pdf ? "none" : "flex";
      if (st.has_pdf && st.version !== version) {
        const doc = await pdfjsLib.getDocument({ url: "/deck.pdf?v=" + st.version }).promise;
        const nd = await (await fetch("/notes.json?v=" + st.version, { cache: "no-store" })).json();
        const td = await (await fetch("/times.json?v=" + st.version, { cache: "no-store" })).json();
        pdf = doc; notes = nd.notes || []; durations = td.durations || []; version = st.version;
        page = Math.max(1, Math.min(pdf.numPages, page));
        if (role === "presenter" && slideT0 === null) slideT0 = Date.now();
        refresh();
      }
    } catch (e) { /* server restarting */ }
    setTimeout(poll, 700);
  }

  // ---------- input ----------
  function toggleFullscreen() {
    if (document.fullscreenElement) document.exitFullscreen();
    else document.documentElement.requestFullscreen().catch(() => {});
  }
  function openPresentation() {
    window.open(location.pathname + "?view=presentation", "typst-present-audience", "popup,width=1280,height=720");
  }

  // ---------- file picker ----------
  function openPicker() {
    $("picker").style.display = "flex";
    browseTo(browsePath);
  }
  function closePicker() {
    $("picker").style.display = "none";
  }
  async function browseTo(path) {
    const pickerErr = $("pickerErr");
    pickerErr.style.display = "none";
    try {
      const url = path ? "/browse?path=" + encodeURIComponent(path) : "/browse";
      const res = await fetch(url, { cache: "no-store" });
      const data = await res.json();
      if (!res.ok) { pickerErr.textContent = data.error || "failed to list directory"; pickerErr.style.display = "block"; return; }
      browsePath = data.path;
      $("pickerPath").textContent = data.path;
      const list = $("pickerList");
      list.innerHTML = "";
      if (data.parent) {
        const up = document.createElement("div");
        up.className = "row up";
        up.textContent = "⬆ ..";
        up.onclick = () => browseTo(data.parent);
        list.appendChild(up);
      }
      for (const e of data.entries) {
        const row = document.createElement("div");
        row.className = "row";
        row.textContent = (e.is_dir ? "📁 " : "📄 ") + e.name;
        const full = data.path.replace(/\/$/, "") + "/" + e.name;
        if (e.is_dir) row.onclick = () => browseTo(full);
        else if (e.selectable) row.onclick = () => openFile(full);
        else continue;
        list.appendChild(row);
      }
    } catch (e) {
      pickerErr.textContent = "failed to list directory";
      pickerErr.style.display = "block";
    }
  }
  async function openFile(path) {
    const pickerErr = $("pickerErr");
    try {
      const res = await fetch("/open", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ path }),
      });
      const data = await res.json();
      if (!data.ok) { pickerErr.textContent = data.error || "failed to open file"; pickerErr.style.display = "block"; return; }
      closePicker();
    } catch (e) {
      pickerErr.textContent = "failed to open file";
      pickerErr.style.display = "block";
    }
  }
  const NEXT = ["ArrowRight", "ArrowDown", "PageDown", " ", "Enter"];
  const PREV = ["ArrowLeft", "ArrowUp", "PageUp", "Backspace"];
  addEventListener("keydown", (e) => {
    if (e.ctrlKey || e.metaKey || e.altKey) return;
    const k = e.key;
    if (NEXT.includes(k)) go(page + 1);
    else if (PREV.includes(k)) go(page - 1);
    else if (k === "Home") go(1);
    else if (k === "End") go(pdf ? pdf.numPages : 1);
    else if (k === "f" || k === "F") toggleFullscreen();
    else if (k === "b" || k === "B" || k === ".") setBlank(!blank);
    else if (k === "p" || k === "P") { if (role === "presenter") openPresentation(); }
    else if ((k === "+" || k === "=") && role === "presenter") setZoom(zoom * 1.4, panX, panY);
    else if (k === "-" && role === "presenter") setZoom(zoom / 1.4, panX, panY);
    else if (k === "0" && role === "presenter") setZoom(1, 0, 0);
    else return;
    e.preventDefault();
  });
  if (role === "presentation") {
    addEventListener("click", () => go(page + 1));
    addEventListener("contextmenu", (e) => { e.preventDefault(); go(page - 1); });
  }

  let rt;
  addEventListener("resize", () => { clearTimeout(rt); rt = setTimeout(refresh, 80); });

  if (role === "presenter") {
    $("btnOpen").onclick = openPresentation;
    $("btnFull").onclick = toggleFullscreen;
    $("btnBlank").onclick = () => setBlank(!blank);
    $("btnFile").onclick = openPicker;
    $("btnZoomReset").onclick = () => setZoom(1, 0, 0);
    $("btnHistory").onclick = () => { $("history").style.display = "flex"; refreshHistory(); };
    $("historyClose").onclick = () => { $("history").style.display = "none"; };
    $("history").addEventListener("click", (e) => { if (e.target.id === "history") $("history").style.display = "none"; });
    function fmtDur(s) {
      if (s == null) return "–";
      const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60);
      return (h ? h + ":" : "") + pad(m) + ":" + pad(s % 60);
    }
    function refreshHistory() {
      if ($("history").style.display !== "flex") return;
      const body = $("historyBody");
      body.innerHTML = "";
      const n = pdf ? pdf.numPages : 0;
      for (let i = 0; i < n; i++) {
        const tr = document.createElement("tr");
        if (i + 1 === page) tr.classList.add("cur");
        const budget = durations[i] || 0;
        const actual = actuals[i];
        const td = (txt) => { const c = document.createElement("td"); c.textContent = txt; return c; };
        tr.appendChild(td(String(i + 1)));
        tr.appendChild(td(budget ? fmtDur(budget) : "–"));
        tr.appendChild(td(fmtDur(actual)));
        if (actual != null && budget > 0) {
          const over = actual > budget;
          tr.lastChild.classList.add(over ? "over" : "under");
        }
        body.appendChild(tr);
      }
    }
    function updatePauseButton() {
      $("btnTimerPause").textContent = pausedElapsed !== null ? "▶" : "⏸";
    }
    $("btnTimerPause").onclick = () => {
      if (pausedElapsed !== null) { // resume
        t0 = Date.now() - pausedElapsed;
        pausedElapsed = null;
      } else if (t0 !== null) { // pause
        pausedElapsed = Date.now() - t0;
        t0 = null;
      }
      updatePauseButton();
    };
    $("btnTimerReset").onclick = () => {
      if (pausedElapsed !== null) pausedElapsed = 0;
      else if (t0 !== null) t0 = Date.now();
      $("timer").textContent = "00:00";
    };

    // ---------- zoom (wheel) + pan (drag) on the current-slide preview ----------
    const cur = $("cur");
    cur.classList.add("zoomable");
    cur.addEventListener("wheel", (e) => {
      e.preventDefault();
      const r = $("cCanvas").getBoundingClientRect();
      // cursor position as a fraction of the (unscaled) slide, in pan-space
      const fx = (e.clientX - r.left) / r.width / zoom - 0.5 / zoom;
      const fy = (e.clientY - r.top) / r.height / zoom - 0.5 / zoom;
      const cx = panX + fx / zoom, cy = panY + fy / zoom;
      const factor = Math.exp(-e.deltaY * 0.0015);
      const nz = Math.max(MIN_ZOOM, Math.min(MAX_ZOOM, zoom * factor));
      // keep the point under the cursor fixed while zooming
      const npx = cx - fx / nz, npy = cy - fy / nz;
      setZoom(nz, npx, npy);
    }, { passive: false });

    let dragging = false, dragStart = null;
    cur.addEventListener("mousedown", (e) => {
      if (zoom === 1) return;
      dragging = true;
      cur.classList.add("panning");
      dragStart = { x: e.clientX, y: e.clientY, panX, panY };
    });
    addEventListener("mousemove", (e) => {
      if (!dragging) return;
      const r = $("cCanvas").getBoundingClientRect();
      const dx = (e.clientX - dragStart.x) / r.width / zoom;
      const dy = (e.clientY - dragStart.y) / r.height / zoom;
      setZoom(zoom, dragStart.panX - dx, dragStart.panY - dy);
    });
    addEventListener("mouseup", () => {
      if (dragging) { dragging = false; cur.classList.remove("panning"); }
    });
    cur.addEventListener("dblclick", () => setZoom(1, 0, 0));
    let size = 22;
    const applySize = () => document.documentElement.style.setProperty("--notes-size", size + "px");
    $("btnNotesPlus").onclick = () => { size = Math.min(48, size + 2); applySize(); };
    $("btnNotesMinus").onclick = () => { size = Math.max(12, size - 2); applySize(); };
    const pad = (n) => String(n).padStart(2, "0");
    function colorForRatio(ratio) {
      // 0 -> green (120deg), 0.75 -> yellow (60deg), >=1 -> red (0deg)
      let hue;
      if (ratio <= 0.75) hue = 120 - (ratio / 0.75) * 60;
      else hue = 60 - (Math.min(ratio, 1) - 0.75) / 0.25 * 60;
      return `hsl(${Math.max(0, Math.min(120, hue))}, 70%, 35%)`;
    }
    setInterval(() => {
      const d = new Date();
      $("clock").textContent = `${pad(d.getHours())}:${pad(d.getMinutes())}`;
      if (t0 !== null) {
        const s = Math.floor((Date.now() - t0) / 1000);
        const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60);
        $("timer").textContent = (h ? h + ":" : "") + pad(m) + ":" + pad(s % 60);
      }
      const slideTimer = $("slideTimer");
      if (slideT0 !== null) {
        const es = Math.floor((Date.now() - slideT0) / 1000);
        const h = Math.floor(es / 3600), m = Math.floor((es % 3600) / 60);
        slideTimer.textContent = (h ? h + ":" : "") + pad(m) + ":" + pad(es % 60);
        const limit = durations[page - 1] || 0;
        if (limit > 0 && es >= limit) {
          slideTimer.classList.add("over");
          slideTimer.style.backgroundColor = "";
        } else if (limit > 0) {
          slideTimer.classList.remove("over");
          slideTimer.style.backgroundColor = colorForRatio(es / limit);
        } else {
          slideTimer.classList.remove("over");
          slideTimer.style.backgroundColor = "";
        }
      }
    }, 500);
  }

  if ("serviceWorker" in navigator) navigator.serviceWorker.register("/sw.js").catch(() => {});
  poll();
  bc.postMessage({ type: "hello" });
})();

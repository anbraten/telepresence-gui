document.addEventListener('alpine:init', () => {
  Alpine.store('tp', {

    // ── State ────────────────────────────────────────────────────
    connected:  false,
    context:    '',
    namespace:  '',
    currentNs:  localStorage.getItem('tp-gui:ns') || '',
    namespaces: [],
    workloads:  [],
    loaded:     false,
    tab:        'workloads',
    connecting: false,  // kept for SSE compat
    connectingNs: '',
    nsFilter:     '',
    workloadFilter: '',
    quitting:   false,
    sseRetry:   1000,

    modal: {
      open:       false,
      workload:   null,
      localPort:  '',
      remotePort: '',
      envFile:    '',
      mountPath:  '',
      submitting: false,
    },

    logs:   [],
    toasts: [],

    // ── Computed ─────────────────────────────────────────────────
    get services()  { return this.workloads.filter(w => !w.intercepted); },
    get intercepts(){ return this.workloads.filter(w =>  w.intercepted); },
    get filteredNamespaces() {
      const q = this.nsFilter.toLowerCase();
      if (!q) return this.namespaces;
      return this.namespaces.filter(ns => ns.toLowerCase().includes(q));
    },
    get filteredWorkloads() {
      const q = this.workloadFilter.toLowerCase();
      const list = q
        ? this.workloads.filter(w => w.name.toLowerCase().includes(q) || (w.namespace||'').toLowerCase().includes(q))
        : this.workloads;
      // intercepted rows float to top
      return [...list].sort((a, b) => (b.intercepted ? 1 : 0) - (a.intercepted ? 1 : 0));
    },
    get statusLabel() {
      if (!this.connected) return 'No cluster';
      return (this.context || 'Connected') + (this.namespace ? ' · ' + this.namespace : '');
    },

    // ── Lifecycle (auto-called by Alpine) ─────────────────────────
    init() {
      this.connectSSE();
      this.loadNamespaces();
    },

    // ── Namespace ─────────────────────────────────────────────────
    onNamespaceChange() {
      localStorage.setItem('tp-gui:ns', this.currentNs);
      this.fetchWorkloads();
    },

    async loadNamespaces() {
      try {
        const r  = await fetch('/api/namespaces');
        if (!r.ok) return;
        const ns = await r.json();
        this.namespaces = ns;
        const saved = localStorage.getItem('tp-gui:ns');
        if (saved && ns.includes(saved)) {
          this.currentNs = saved;
        } else if (!this.currentNs && ns.length > 0) {
          this.currentNs = ns[0];
        }
      } catch(_) {}
    },

    // ── Workloads ─────────────────────────────────────────────────
    async fetchWorkloads() {
      try {
        const r = await fetch(`/api/workloads?namespace=${encodeURIComponent(this.currentNs)}`);
        if (!r.ok) return;
        this.workloads = await r.json();
        this.loaded = true;
      } catch(_) {}
    },

    // ── SSE ───────────────────────────────────────────────────────
    connectSSE() {
      const es = new EventSource('/events');
      es.addEventListener('status', e => {
        const s = JSON.parse(e.data);
        this.connected = s.connected;
        this.context   = s.context   || '';
        this.namespace = s.namespace || '';
        if (s.connected && this.namespaces.length === 0) this.loadNamespaces();
      });
      es.addEventListener('workloads', e => {
        this.workloads = JSON.parse(e.data);
        this.loaded = true;
      });
      es.onmessage = e => { if (e.data) this.addLog(e.data); };
      es.onerror   = () => {
        es.close();
        this.connected = false;
        setTimeout(() => this.connectSSE(), Math.min(this.sseRetry *= 1.5, 10000));
      };
      es.onopen = () => {
        this.sseRetry = 1000;
        this.addLog('✓ connected to tp-gui server', 'ok');
        this.addToast('tp-gui ready', 'ok');
      };
    },

    // ── Modal ─────────────────────────────────────────────────────
    openModal(w) {
      Object.assign(this.modal, {
        workload: w, open: true,
        localPort: '', remotePort: '', envFile: '', mountPath: '', submitting: false,
      });
    },
    closeModal() {
      this.modal.open = false;
      this.modal.workload = null;
    },

    async submitIntercept() {
      const w = this.modal.workload;
      if (!w || this.modal.submitting) return;
      this.modal.submitting = true;
      const payload = {
        workload:   w.name,
        namespace:  w.namespace || this.currentNs || 'default',
        localPort:  this.modal.localPort,
        remotePort: this.modal.remotePort,
        envFile:    this.modal.envFile,
        mountPath:  this.modal.mountPath,
      };
      try {
        const r    = await fetch('/api/intercept', { method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify(payload) });
        const data = await r.json();
        if (!r.ok) throw new Error(data.error || 'intercept failed');
        this.addToast(`Intercept started: ${w.name}`, 'ok');
        this.addLog(`✓ intercept started: ${w.name} (local:${payload.localPort||'auto'})`, 'ok');
        this.closeModal();
      } catch(e) {
        this.addToast(e.message, 'err');
        this.addLog(`✗ intercept error: ${e.message}`, 'err');
      } finally {
        this.modal.submitting = false;
      }
    },

    // ── API ───────────────────────────────────────────────────────
    async apiLeave(name) {
      try {
        const r    = await fetch('/api/leave', { method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({name}) });
        const data = await r.json();
        if (!r.ok) throw new Error(data.error || 'leave failed');
        this.addToast(`Stopped: ${name}`, 'ok');
        this.addLog(`✓ intercept stopped: ${name}`, 'ok');
      } catch(e) {
        this.addToast(e.message, 'err');
        this.addLog(`✗ leave error: ${e.message}`, 'err');
      }
    },

    async apiConnect(ns) {
      ns = ns || this.currentNs;
      this.currentNs = ns;
      this.connectingNs = ns;
      this.addLog(`→ connecting (namespace: ${ns || 'default'})…`);
      try {
        const r    = await fetch(`/api/connect?namespace=${encodeURIComponent(ns)}`, {method:'POST'});
        const data = await r.json();
        if (!r.ok) throw new Error(data.error || 'connect failed');
        this.addToast('Connected to cluster', 'ok');
        this.addLog('✓ connected', 'ok');
      } catch(e) {
        this.addToast(e.message, 'err');
        this.addLog(`✗ connect: ${e.message}`, 'err');
      } finally {
        this.connectingNs = '';
      }
    },

    async apiQuit() {
      this.quitting = true;
      this.addLog('→ disconnecting…');
      try {
        const r    = await fetch('/api/quit', {method:'POST'});
        const data = await r.json();
        if (!r.ok) throw new Error(data.error || 'quit failed');
        this.addToast('Disconnected', 'info');
        this.addLog('✓ disconnected');
      } catch(e) {
        this.addToast(e.message, 'err');
        this.addLog(`✗ quit: ${e.message}`, 'err');
      } finally {
        this.quitting = false;
      }
    },

    // ── Logs ──────────────────────────────────────────────────────
    addLog(msg, type = '') {
      const ts = new Date().toLocaleTimeString('en-GB', {hour12:false});
      this.logs.push({ts, msg, type});
      if (this.logs.length > 300) this.logs.shift();
      setTimeout(() => {
        const el = document.getElementById('log-panel');
        if (el) el.scrollTop = el.scrollHeight;
      }, 0);
    },
    clearLogs() { this.logs = []; },

    // ── Toasts ────────────────────────────────────────────────────
    addToast(msg, type = 'info') {
      const id = Date.now() + Math.random();
      this.toasts.push({id, msg, type});
      setTimeout(() => { this.toasts = this.toasts.filter(t => t.id !== id); }, 3200);
    },
  });
});


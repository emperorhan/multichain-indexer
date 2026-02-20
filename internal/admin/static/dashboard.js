(function () {
    'use strict';

    const POLL_INTERVAL = 5000;
    const PAGE_SIZE = 50;

    let currentTab = 'overview';
    let eventsOffset = 0;
    let eventsTotal = 0;
    let pollTimer = null;

    // --- Utilities ---

    function formatAmount(raw, decimals) {
        if (!raw || raw === '0') return '0';
        const neg = raw.startsWith('-');
        const abs = neg ? raw.slice(1) : raw;
        if (decimals <= 0) return (neg ? '-' : '') + addCommas(abs);
        const padded = abs.padStart(decimals + 1, '0');
        const intPart = padded.slice(0, padded.length - decimals) || '0';
        const fracPart = padded.slice(padded.length - decimals).replace(/0+$/, '');
        const formatted = fracPart ? intPart + '.' + fracPart : intPart;
        return (neg ? '-' : '') + addCommas(formatted);
    }

    function addCommas(s) {
        const parts = s.split('.');
        parts[0] = parts[0].replace(/\B(?=(\d{3})+(?!\d))/g, ',');
        return parts.join('.');
    }

    function relativeTime(isoStr) {
        if (!isoStr) return '-';
        const diff = (Date.now() - new Date(isoStr).getTime()) / 1000;
        if (diff < 5) return 'just now';
        if (diff < 60) return Math.floor(diff) + 's ago';
        if (diff < 3600) return Math.floor(diff / 60) + 'm ago';
        if (diff < 86400) return Math.floor(diff / 3600) + 'h ago';
        return Math.floor(diff / 86400) + 'd ago';
    }

    function escapeHtml(s) {
        const d = document.createElement('div');
        d.textContent = s;
        return d.innerHTML;
    }

    function truncate(s, n) {
        if (!s) return '';
        return s.length > n ? s.slice(0, n) + '...' : s;
    }

    function activityClass(type) {
        const t = (type || '').toUpperCase();
        if (t === 'DEPOSIT' || t === 'CLAIM_REWARD') return 'activity-deposit';
        if (t === 'WITHDRAWAL') return 'activity-withdrawal';
        if (t.includes('FEE')) return 'activity-fee';
        return '';
    }

    function deltaClass(raw) {
        if (!raw) return '';
        return raw.startsWith('-') ? 'delta-negative' : 'delta-positive';
    }

    function lagBadge(lag) {
        if (lag <= 2) return '<span class="badge badge-ok">SYNCED</span>';
        if (lag <= 20) return '<span class="badge badge-warn">LAG ' + lag + '</span>';
        return '<span class="badge badge-err">LAG ' + lag + '</span>';
    }

    async function apiFetch(path) {
        const resp = await fetch(path);
        if (!resp.ok) throw new Error('API error: ' + resp.status);
        return resp.json();
    }

    // --- Tab routing ---

    function initTabs() {
        document.querySelectorAll('.tab').forEach(function (el) {
            el.addEventListener('click', function (e) {
                e.preventDefault();
                switchTab(el.dataset.tab);
            });
        });

        const hash = location.hash.replace('#', '') || 'overview';
        switchTab(hash);
    }

    function switchTab(name) {
        currentTab = name;
        location.hash = name;

        document.querySelectorAll('.tab').forEach(function (el) {
            el.classList.toggle('active', el.dataset.tab === name);
        });
        document.querySelectorAll('.tab-content').forEach(function (el) {
            el.classList.toggle('active', el.id === 'tab-' + name);
        });

        eventsOffset = 0;
        pollNow();
    }

    // --- Overview ---

    async function loadOverview() {
        try {
            const data = await apiFetch('/admin/v1/dashboard/overview');
            document.getElementById('server-time').textContent = new Date(data.server_time).toLocaleTimeString();
            document.getElementById('stat-pipelines').textContent = data.pipelines.length;
            document.getElementById('stat-addresses').textContent = data.total_watched_addresses;

            const container = document.getElementById('pipeline-cards');
            if (data.pipelines.length === 0) {
                container.innerHTML = '<div class="empty-state">No active pipelines</div>';
                return;
            }

            container.innerHTML = data.pipelines.map(function (p) {
                return '<div class="pipeline-card">' +
                    '<h3><span>' + escapeHtml(p.chain) + ' / ' + escapeHtml(p.network) + '</span>' + lagBadge(p.lag) + '</h3>' +
                    '<div class="meta">' +
                    '<div><span>Head</span><span>' + p.head_sequence + '</span></div>' +
                    '<div><span>Ingested</span><span>' + p.ingested_sequence + '</span></div>' +
                    '<div><span>Lag</span><span>' + p.lag + '</span></div>' +
                    '<div><span>Heartbeat</span><span>' + relativeTime(p.last_heartbeat_at) + '</span></div>' +
                    '</div></div>';
            }).join('');
        } catch (err) {
            console.error('overview error:', err);
        }
    }

    // --- Balances ---

    function getBalanceParams() {
        var chain = document.getElementById('bal-chain').value;
        var network = document.getElementById('bal-network').value;
        return { chain: chain, network: network };
    }

    async function loadBalances() {
        var p = getBalanceParams();
        var container = document.getElementById('balances-content');
        if (!p.chain || !p.network) {
            container.innerHTML = '<div class="empty-state">Select chain and network</div>';
            return;
        }

        try {
            var data = await apiFetch('/admin/v1/dashboard/balances?chain=' + p.chain + '&network=' + p.network);
            var addrs = data.addresses || [];

            if (addrs.length === 0) {
                container.innerHTML = '<div class="empty-state">No watched addresses found</div>';
                return;
            }

            container.innerHTML = addrs.map(function (a) {
                var label = a.label ? ' (' + escapeHtml(a.label) + ')' : '';
                var wallet = a.wallet_id ? '<span class="addr-meta">Wallet: ' + escapeHtml(a.wallet_id) + '</span>' : '';

                var balRows = '';
                if (a.balances.length === 0) {
                    balRows = '<tr><td colspan="4" style="text-align:center;color:var(--text-secondary)">No balances</td></tr>';
                } else {
                    balRows = a.balances.map(function (b) {
                        return '<tr>' +
                            '<td>' + escapeHtml(b.token_symbol) + '<br><small style="color:var(--text-secondary)">' + escapeHtml(b.token_name) + '</small></td>' +
                            '<td class="mono">' + formatAmount(b.amount, b.decimals) + '</td>' +
                            '<td>' + (b.balance_type || 'liquid') + '</td>' +
                            '<td>' + relativeTime(b.updated_at) + '</td></tr>';
                    }).join('');
                }

                return '<div class="addr-group">' +
                    '<div class="addr-header" onclick="this.parentElement.querySelector(\'table\').style.display=this.parentElement.querySelector(\'table\').style.display===\'none\'?\'table\':\'none\'">' +
                    '<span class="addr-title mono">' + escapeHtml(truncate(a.address, 20)) + label + '</span>' + wallet +
                    '</div>' +
                    '<table><thead><tr><th>Token</th><th>Amount</th><th>Type</th><th>Updated</th></tr></thead><tbody>' +
                    balRows + '</tbody></table></div>';
            }).join('');
        } catch (err) {
            console.error('balances error:', err);
            container.innerHTML = '<div class="empty-state">Error loading balances</div>';
        }
    }

    // --- Events ---

    function getEventParams() {
        return {
            chain: document.getElementById('evt-chain').value,
            network: document.getElementById('evt-network').value,
            address: document.getElementById('evt-address').value.trim()
        };
    }

    async function loadEvents() {
        var p = getEventParams();
        var container = document.getElementById('events-content');
        var pagination = document.getElementById('events-pagination');

        if (!p.chain || !p.network) {
            container.innerHTML = '<div class="empty-state">Select chain and network</div>';
            pagination.innerHTML = '';
            return;
        }

        try {
            var url = '/admin/v1/dashboard/events?chain=' + p.chain + '&network=' + p.network +
                '&limit=' + PAGE_SIZE + '&offset=' + eventsOffset;
            if (p.address) url += '&address=' + encodeURIComponent(p.address);

            var data = await apiFetch(url);
            eventsTotal = data.total || 0;
            var events = data.events || [];

            if (events.length === 0) {
                container.innerHTML = '<div class="empty-state">No events found</div>';
                pagination.innerHTML = '';
                return;
            }

            container.innerHTML = '<table><thead><tr>' +
                '<th>Time</th><th>Type</th><th>Address</th><th>Counterparty</th>' +
                '<th>Token</th><th>Delta</th><th>Block</th><th>Finality</th>' +
                '</tr></thead><tbody>' +
                events.map(function (e) {
                    return '<tr>' +
                        '<td title="' + escapeHtml(e.created_at) + '">' + relativeTime(e.block_time || e.created_at) + '</td>' +
                        '<td class="' + activityClass(e.activity_type) + '">' + escapeHtml(e.activity_type) + '</td>' +
                        '<td class="mono truncate" title="' + escapeHtml(e.address) + '">' + escapeHtml(truncate(e.address, 12)) + '</td>' +
                        '<td class="mono truncate" title="' + escapeHtml(e.counterparty_address) + '">' + escapeHtml(truncate(e.counterparty_address, 12)) + '</td>' +
                        '<td>' + escapeHtml(e.token_symbol) + '</td>' +
                        '<td class="mono ' + deltaClass(e.delta) + '">' + formatAmount(e.delta, e.decimals) + '</td>' +
                        '<td class="mono">' + e.block_cursor + '</td>' +
                        '<td>' + escapeHtml(e.finality_state) + '</td>' +
                        '</tr>';
                }).join('') +
                '</tbody></table>';

            // Pagination
            var totalPages = Math.ceil(eventsTotal / PAGE_SIZE);
            var currentPage = Math.floor(eventsOffset / PAGE_SIZE) + 1;
            pagination.innerHTML =
                '<button id="evt-prev" ' + (eventsOffset <= 0 ? 'disabled' : '') + '>Prev</button>' +
                '<span>' + currentPage + ' / ' + totalPages + ' (' + eventsTotal + ' total)</span>' +
                '<button id="evt-next" ' + (eventsOffset + PAGE_SIZE >= eventsTotal ? 'disabled' : '') + '>Next</button>';

            var prevBtn = document.getElementById('evt-prev');
            var nextBtn = document.getElementById('evt-next');
            if (prevBtn) prevBtn.onclick = function () { eventsOffset = Math.max(0, eventsOffset - PAGE_SIZE); loadEvents(); };
            if (nextBtn) nextBtn.onclick = function () { eventsOffset += PAGE_SIZE; loadEvents(); };
        } catch (err) {
            console.error('events error:', err);
            container.innerHTML = '<div class="empty-state">Error loading events</div>';
            pagination.innerHTML = '';
        }
    }

    // --- Polling ---

    function pollNow() {
        if (document.hidden) return;
        switch (currentTab) {
            case 'overview': loadOverview(); break;
            case 'balances': loadBalances(); break;
            case 'events': loadEvents(); break;
        }
    }

    function startPolling() {
        pollNow();
        pollTimer = setInterval(pollNow, POLL_INTERVAL);
    }

    document.addEventListener('visibilitychange', function () {
        if (document.hidden) {
            if (pollTimer) { clearInterval(pollTimer); pollTimer = null; }
            document.getElementById('poll-status').textContent = 'PAUSED';
            document.getElementById('poll-status').className = 'badge badge-warn';
        } else {
            document.getElementById('poll-status').textContent = 'LIVE';
            document.getElementById('poll-status').className = 'badge badge-ok';
            startPolling();
        }
    });

    // --- Init ---

    function initControls() {
        ['bal-chain', 'bal-network'].forEach(function (id) {
            document.getElementById(id).addEventListener('change', function () { loadBalances(); });
        });
        ['evt-chain', 'evt-network'].forEach(function (id) {
            document.getElementById(id).addEventListener('change', function () { eventsOffset = 0; loadEvents(); });
        });

        var debounceTimer;
        document.getElementById('evt-address').addEventListener('input', function () {
            clearTimeout(debounceTimer);
            debounceTimer = setTimeout(function () { eventsOffset = 0; loadEvents(); }, 400);
        });
    }

    initTabs();
    initControls();
    startPolling();
})();

const root = document.querySelector('#app');
const toast = document.querySelector('#toast');
const upgradeNotice = document.querySelector('#upgrade-notice');
const upgradeDismiss = document.querySelector('[data-dismiss-upgrade]');
const upgradeStorageKey = 'qday:v1.0.0-upgrade-dismissed';

let statusCache = null;
let renderedFingerprint = '';
let routeVersion = 0;
let refreshRunning = false;
let toastTimer;

function upgradeDismissed() {
  try { return localStorage.getItem(upgradeStorageKey) === '1'; }
  catch { return false; }
}

function dismissUpgradeNotice() {
  try { localStorage.setItem(upgradeStorageKey, '1'); }
  catch (_) { }
  upgradeNotice.hidden = true;
  document.body.classList.remove('upgrade-notice-open');
}

upgradeDismiss.addEventListener('click', dismissUpgradeNotice);
if (!upgradeDismissed()) {
  upgradeNotice.hidden = false;
  document.body.classList.add('upgrade-notice-open');
  requestAnimationFrame(() => upgradeDismiss.focus({preventScroll:true}));
}

const e = value => String(value ?? '').replace(/[&<>'"]/g, character => ({
  '&':'&amp;', '<':'&lt;', '>':'&gt;', "'":'&#39;', '"':'&quot;'
})[character]);

const commas = value => {
  const [whole, fraction] = String(value ?? '0').split('.');
  return whole.replace(/\B(?=(\d{3})+(?!\d))/g, ',') + (fraction ? `.${fraction}` : '');
};

const short = (value, left = 10, right = 8) => value ? `${value.slice(0, left)}…${value.slice(-right)}` : '—';
const exactTime = value => value ? new Date(value).toLocaleString(undefined, {dateStyle:'medium', timeStyle:'medium'}) : 'Pending';

function relativeTime(value) {
  if (!value) return 'Pending';
  const seconds = Math.max(0, Math.floor((Date.now() - new Date(value).getTime()) / 1000));
  if (seconds < 60) return `${seconds}s ago`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  return `${Math.floor(seconds / 86400)}d ago`;
}

async function api(path) {
  const response = await fetch(path, {headers:{Accept:'application/json'}, cache:'no-store'});
  const body = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(body.error || `HTTP ${response.status}`);
  return body;
}

function paint(token, markup) {
  if (token !== routeVersion) return false;
  root.innerHTML = markup;
  return true;
}

function showToast(message) {
  toast.textContent = message;
  toast.classList.add('show');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => toast.classList.remove('show'), 1800);
}

function copyButton(value) {
  return `<button class="copy-button" type="button" data-copy="${e(value)}">Copy</button>`;
}

function roundedCoins(value, places = 4) {
  const raw = String(value ?? '0');
  const match = raw.match(/^(\d+)(?:\.(\d+))?$/);
  if (!match) return commas(raw);
  const fraction = match[2] || '';
  if (fraction.length <= places) {
    const trimmed = fraction.replace(/0+$/, '');
    return commas(`${match[1]}${trimmed ? `.${trimmed}` : ''}`);
  }
  const scale = 10n ** BigInt(places);
  let scaled = BigInt(match[1]) * scale + BigInt(fraction.slice(0, places).padEnd(places, '0'));
  if (fraction[places] >= '5') scaled += 1n;
  if (scaled === 0n && (BigInt(match[1]) !== 0n || /[1-9]/.test(fraction))) return `<0.${'0'.repeat(places - 1)}1`;
  const whole = scaled / scale;
  const roundedFraction = String(scaled % scale).padStart(places, '0').replace(/0+$/, '');
  return commas(`${whole}${roundedFraction ? `.${roundedFraction}` : ''}`);
}

function amount(value) {
  return `${e(roundedCoins(value?.qday || '0'))} <span class="unit">QDAY</span>`;
}

function compactCoins(value) {
  const number = Number(value ?? 0);
  if (!Number.isFinite(number)) return commas(value);
  const units = [
    [1e12, 'T'],
    [1e9, 'B'],
    [1e6, 'M'],
    [1e3, 'K']
  ];
  for (const [divisor, suffix] of units) {
    if (Math.abs(number) >= divisor) {
      return (number / divisor).toFixed(2).replace(/\.00$/, '').replace(/(\.\d)0$/, '$1') + suffix;
    }
  }
  return Math.round(number).toLocaleString('en-US');
}

function compactWork(value) {
  try {
    const work = BigInt(value);
    const units = [
      [10n ** 24n, 'Y'],
      [10n ** 21n, 'Z'],
      [10n ** 18n, 'E'],
      [10n ** 15n, 'P'],
      [10n ** 12n, 'T'],
      [10n ** 9n, 'G'],
      [10n ** 6n, 'M'],
      [10n ** 3n, 'K']
    ];
    for (const [divisor, suffix] of units) {
      if (work >= divisor) {
        const hundredths = (work * 100n + divisor / 2n) / divisor;
        const whole = hundredths / 100n;
        const fraction = String(hundredths % 100n).padStart(2, '0').replace(/0+$/, '');
        return `${whole}${fraction ? `.${fraction}` : ''}${suffix}`;
      }
    }
    return work.toString();
  } catch (_) {
    return String(value ?? '0');
  }
}

function statusFingerprint(status) {
  if (!status) return '';
  return [
    status.height,
    status.indexedHeight,
    status.connections,
    status.mempoolTransactions,
    status.qday.stage,
    status.qday.height,
    status.qday.proofPending,
    status.qday.proofTransaction,
    status.currentSupply?.atomic,
    status.circulatingSupply?.atomic
  ].join('|');
}

function qdayStageLabel(stage) {
  return stage === 'WAITING' ? 'UNBROKEN' : stage;
}

function transactionKindLabel(kind) {
  return kind === 'QDAY PROOF' ? 'PQ DAY PROOF' : kind;
}

function breadcrumbs(items) {
  return `<nav class="breadcrumbs" aria-label="Breadcrumb">${items.map((item, index) => {
    const content = item.href ? `<a class="route-link" href="${e(item.href)}">${e(item.label)}</a>` : `<span>${e(item.label)}</span>`;
    return `${index ? '<i>/</i>' : ''}${content}`;
  }).join('')}</nav>`;
}

function pageHeader(title, subtitle, trail, backHref, backLabel) {
  return `<section class="page-header">
    ${breadcrumbs(trail)}
    <div class="page-header-row"><div><h1>${e(title)}</h1>${subtitle ? `<p>${e(subtitle)}</p>` : ''}</div>${backHref ? `<a class="button secondary route-link" href="${e(backHref)}">← ${e(backLabel)}</a>` : ''}</div>
  </section>`;
}

function searchPanel() {
  return `<section class="search-panel">
    <form data-search>
      <label for="explorer-search">Search the blockchain</label>
      <div><input id="explorer-search" name="q" autocomplete="off" spellcheck="false" placeholder="Block height, block ID, transaction ID or qday1 address"><button type="submit">Search</button></div>
      <p>Enter an exact block height, hash, transaction ID or address.</p>
    </form>
  </section>`;
}

function metric(label, value, note = '', href = '', title = '') {
  const content = `<span>${e(label)}</span><strong>${e(value)}</strong>${note ? `<small>${e(note)}</small>` : ''}`;
  const titleAttribute = title ? ` title="${e(title)}"` : '';
  return href ? `<a class="metric route-link" href="${e(href)}"${titleAttribute}>${content}</a>` : `<div class="metric"${titleAttribute}>${content}</div>`;
}

function overviewPoint(label, value, note = '', href = '', title = '') {
  const content = `<small>${e(label)}</small><strong>${e(value)}</strong>${note ? `<em>${e(note)}</em>` : ''}`;
  const titleAttribute = title ? ` title="${e(title)}"` : '';
  return href
    ? `<a class="overview-point route-link" href="${e(href)}"${titleAttribute}>${content}</a>`
    : `<div class="overview-point"${titleAttribute}>${content}</div>`;
}

function chainMetric(status) {
  const seconds = Number(status.blockIntervalSeconds || 60);
  return `<div class="overview-panel chain-metric" aria-label="Chain metrics"><div class="overview-values">
    ${overviewPoint('Latest block', commas(status.height), exactTime(status.lastBlock), `/block/${status.height}`)}
    ${overviewPoint('Block time', `${commas(seconds)} SEC`, 'Consensus target')}
  </div></div>`;
}

function proofOfWorkMetric(status) {
  return `<div class="overview-panel pow-metric" aria-label="Proof of work metrics"><div class="overview-values">
    ${overviewPoint('Network hashrate', status.observedHashrate || status.estimatedHashrate)}
    ${overviewPoint('Difficulty', compactWork(status.difficulty), `${commas(status.difficulty)} expected hashes`, '', `Target ${status.target}`)}
  </div></div>`;
}

function supplyMetric(status) {
  const values = [
    ['Issued', status.issuedSupply, 'issued', false],
    ['Burned', status.burnedSupply, 'burned', false],
    ['Current supply', status.currentSupply, 'current', true]
  ];
  return `<div class="overview-panel supply-metric" aria-label="Supply metrics"><div class="supply-values">${values.map(([label, value, style, showUnit]) => `
    <div class="supply-point ${style}" title="${e(roundedCoins(value.qday))} QDAY">
      <small>${e(label)}</small><strong>${e(compactCoins(value.qday))}${showUnit ? ' QDAY' : ''}</strong>
    </div>`).join('')}</div></div>`;
}

function emissionMetric(status) {
  const total = Number(status.rewardBlocksTotal || 0);
  const remaining = Number(status.rewardBlocksRemaining || 0);
  return `<div class="overview-panel emission-metric" aria-label="Emission metrics"><div class="emission-values">
    <div class="emission-point" title="Maximum issued supply: ${e(roundedCoins(status.maximumIssuedSupply.qday))} QDAY. Confirmed burns do not change the issuance cap."><small>Max supply</small><strong>${e(compactCoins(status.maximumIssuedSupply.qday))}</strong></div>
    <div class="emission-point" title="Current base reward: ${e(roundedCoins(status.blockReward.qday))} QDAY"><small>Block reward</small><strong>${e(compactCoins(status.blockReward.qday))}</strong></div>
    <div class="emission-point" title="${e(commas(remaining))} reward blocks remain out of ${e(commas(total))}. Rewards end after block ${e(commas(total))}."><small>Reward blocks left</small><strong>${e(compactCoins(remaining))}</strong></div>
  </div></div>`;
}

function networkMetric(status) {
  const stateNote = status.qday.stage === 'WAITING' ? 'Canary unbroken' : `Block ${commas(status.qday.height)}`;
  return `<div class="overview-panel network-metric" aria-label="Network metrics"><div class="overview-values">
    ${overviewPoint('Connections', commas(status.connections), `${commas(status.mempoolTransactions)} in mempool`)}
    ${overviewPoint('PQ Day state', qdayStageLabel(status.qday.stage), stateNote, '/qday')}
  </div></div>`;
}

function tableEmpty(columns, message) {
  return `<tr><td class="table-empty" colspan="${columns}">${e(message)}</td></tr>`;
}

function blockTable(blocks) {
  const rows = blocks?.length ? blocks.map(block => `<tr data-href="/block/${e(block.height)}" tabindex="0">
    <td><a class="primary-link route-link" href="/block/${e(block.height)}">${commas(block.height)}</a></td>
    <td class="hash-cell"><a class="hash-link route-link" href="/block/${e(block.height)}" title="${e(block.id)}">${e(block.id)}</a></td>
    <td title="${e(exactTime(block.timestamp))}">${e(relativeTime(block.timestamp))}</td>
    <td>${commas(block.transactions)}</td>
    <td class="address-cell">${block.miner ? `<a class="hash-link route-link" href="/address/${e(block.miner)}" title="${e(block.miner)}">${e(block.miner)}</a>` : '—'}</td>
    <td class="numeric">${amount(block.reward)}</td>
  </tr>`).join('') : tableEmpty(6, 'No blocks found.');
  return `<div class="table-scroll"><table class="data-table blocks-table">
    <thead><tr><th>Height</th><th>Block ID</th><th>Age</th><th>Transactions</th><th>Miner</th><th class="numeric">Reward</th></tr></thead>
    <tbody>${rows}</tbody>
  </table></div>`;
}

function transactionTable(transactions) {
  const rows = transactions?.length ? transactions.map(tx => `<tr data-href="/transaction/${e(tx.id)}" tabindex="0">
    <td><span class="type-badge ${tx.mempool ? 'pending' : tx.kind === 'QDAY PROOF' ? 'qday' : tx.kind === 'BURN' ? 'burn' : ''}">${e(transactionKindLabel(tx.kind))}</span></td>
    <td class="hash-cell"><a class="hash-link route-link" href="/transaction/${e(tx.id)}" title="${e(tx.id)}">${e(tx.id)}</a></td>
    <td>${tx.mempool ? '<span class="status pending">Mempool</span>' : `<a class="primary-link route-link" href="/block/${e(tx.height)}">${commas(tx.height)}</a>`}</td>
    <td title="${e(exactTime(tx.timestamp))}">${e(relativeTime(tx.timestamp))}</td>
    <td class="address-cell">${tx.from ? `<a class="hash-link route-link" href="/address/${e(tx.from)}" title="${e(tx.from)}">${e(tx.from)}</a>` : '—'}</td>
    <td class="address-cell">${tx.kind === 'BURN' ? '<span class="burn-label">VOID</span>' : tx.to ? `<a class="hash-link route-link" href="/address/${e(tx.to)}" title="${e(tx.to)}">${e(tx.to)}</a>` : '—'}</td>
    <td class="numeric">${amount(tx.value)}</td>
    <td class="numeric">${amount(tx.fee)}</td>
  </tr>`).join('') : tableEmpty(8, 'No transactions found.');
  return `<div class="table-scroll"><table class="data-table transactions-table">
    <thead><tr><th>Type</th><th>Transaction ID</th><th>Block</th><th>Age</th><th>From</th><th>To</th><th class="numeric">Amount</th><th class="numeric">Fee</th></tr></thead>
    <tbody>${rows}</tbody>
  </table></div>`;
}

function eventTable(events) {
  const rows = events?.length ? events.map(event => `<tr data-href="/${e(event.target)}/${e(event.linkID)}" tabindex="0">
    <td class="event-type"><span class="type-badge ${event.kind === 'QDAY PROOF' ? 'qday' : event.kind === 'BURN' ? 'burn' : ''}">${e(transactionKindLabel(event.kind))}</span></td>
    <td class="hash-cell event-id"><a class="hash-link route-link" href="/${e(event.target)}/${e(event.linkID)}" title="${e(event.id)}">${e(event.id)}</a></td>
    <td class="event-block" data-label="Block"><a class="primary-link route-link" href="/block/${e(event.height)}">${commas(event.height)}</a></td>
    <td class="event-age" data-label="Age" title="${e(exactTime(event.timestamp))}">${e(relativeTime(event.timestamp))}</td>
    <td class="event-direction"><span class="direction ${event.direction.toLowerCase()}">${e(event.direction)}</span></td>
    <td class="numeric event-value" data-label="Value">${amount(event.value)}</td>
    <td class="numeric event-fee" data-label="Fee">${amount(event.fee)}</td>
  </tr>`).join('') : tableEmpty(7, 'No address history found.');
  return `<div class="table-scroll"><table class="data-table events-table">
    <thead><tr><th>Type</th><th>Event ID</th><th>Block</th><th>Age</th><th>Direction</th><th class="numeric">Value</th><th class="numeric">Fee</th></tr></thead>
    <tbody>${rows}</tbody>
  </table></div>`;
}

function shareOfSupply(value, supply) {
  const balance = Number(value?.qday || 0);
  const total = Number(supply?.qday || 0);
  if (!Number.isFinite(balance) || !Number.isFinite(total) || total <= 0) return '—';
  const share = balance / total * 100;
  const digits = share < .01 ? 4 : share < 1 ? 3 : 2;
  return `${share.toFixed(digits).replace(/\.0+$/, '')}%`;
}

function richListTable(entries, supply) {
  const rows = entries?.length ? entries.map(entry => `<tr data-href="/address/${e(entry.address)}" tabindex="0">
    <td><span class="rank-number">${commas(entry.rank)}</span></td>
    <td class="address-cell rich-address"><a class="hash-link route-link" href="/address/${e(entry.address)}" title="${e(entry.address)}">${e(entry.address)}</a></td>
    <td class="numeric rich-balance">${amount(entry.balance)}</td>
    <td class="numeric rich-share">${e(shareOfSupply(entry.balance, supply))}</td>
  </tr>`).join('') : tableEmpty(4, 'No funded addresses found.');
  return `<div class="table-scroll"><table class="data-table rich-list-table">
    <thead><tr><th>Rank</th><th>Address</th><th class="numeric">Balance</th><th class="numeric">Share of supply</th></tr></thead>
    <tbody>${rows}</tbody>
  </table></div>`;
}

function card(title, content, action = '') {
  return `<section class="card"><header class="card-header"><h2>${e(title)}</h2>${action}</header>${content}</section>`;
}

function pageHref(base, page, limit) {
  return page === 1 ? base : `${base}?offset=${(page - 1) * limit}`;
}

function visiblePages(current, total) {
  if (total <= 7) return Array.from({length:total}, (_, index) => index + 1);
  const pages = new Set([1, total, current - 1, current, current + 1]);
  if (current <= 4) [2, 3, 4, 5].forEach(page => pages.add(page));
  if (current >= total - 3) [total - 4, total - 3, total - 2, total - 1].forEach(page => pages.add(page));
  const sorted = [...pages].filter(page => page > 0 && page <= total).sort((a, b) => a - b);
  const result = [];
  sorted.forEach((page, index) => {
    if (index && page - sorted[index - 1] > 1) result.push(null);
    result.push(page);
  });
  return result;
}

function pagination({base, offset, limit, total, emptyLabel = 'No results'}) {
  const count = Math.max(0, Number(total) || 0);
  if (!count) return `<p class="pagination-summary pagination-empty">${e(emptyLabel)}</p>`;
  const pageSize = Math.max(1, Number(limit) || 1);
  const pageCount = Math.max(1, Math.ceil(count / pageSize));
  const current = Math.min(pageCount, Math.floor(Math.max(0, Number(offset) || 0) / pageSize) + 1);
  const start = (current - 1) * pageSize + 1;
  const end = Math.min(current * pageSize, count);
  const previous = current > 1 ? `<a class="pagination-control route-link" href="${e(pageHref(base, current - 1, pageSize))}" rel="prev"><span class="pagination-full">← Previous</span><span class="pagination-short">← Prev</span></a>` : '<span class="pagination-control disabled" aria-disabled="true"><span class="pagination-full">← Previous</span><span class="pagination-short">← Prev</span></span>';
  const next = current < pageCount ? `<a class="pagination-control route-link" href="${e(pageHref(base, current + 1, pageSize))}" rel="next"><span class="pagination-full">Next →</span><span class="pagination-short">Next →</span></a>` : '<span class="pagination-control disabled" aria-disabled="true"><span class="pagination-full">Next →</span><span class="pagination-short">Next →</span></span>';
  const pages = visiblePages(current, pageCount).map(page => {
    if (page === null) return '<span class="pagination-ellipsis" aria-hidden="true">…</span>';
    const edge = page === 1 || page === pageCount ? ' edge' : '';
    return page === current
      ? `<span class="pagination-page current${edge}" aria-current="page" aria-label="Page ${page}">${commas(page)}</span>`
      : `<a class="pagination-page${edge} route-link" href="${e(pageHref(base, page, pageSize))}" aria-label="Page ${page}">${commas(page)}</a>`;
  }).join('');
  return `<div class="pagination-wrap">
    <nav class="pagination" aria-label="Pagination">${previous}<div class="pagination-pages">${pages}</div>${next}</nav>
    <p class="pagination-summary">Showing ${commas(start)}–${commas(end)} of ${commas(count)}</p>
  </div>`;
}

function detailList(items) {
  return `<dl class="detail-list">${items.map(([label, value, raw = false]) => `<div><dt>${e(label)}</dt><dd>${raw ? value : e(value)}</dd></div>`).join('')}</dl>`;
}

function identifier(label, value) {
  return `<section class="identifier"><div><span>${e(label)}</span><code>${e(value)}</code></div>${copyButton(value)}</section>`;
}

function ioTable(entries) {
  const rows = entries?.length ? entries.map(item => `<tr>
    <td class="hash-cell">${item.burn ? '<span class="burn-label">VOID — PERMANENTLY BURNED</span>' : `<a class="hash-link route-link" href="/address/${e(item.address)}" title="${e(item.address)}">${e(item.address)}</a>`}</td>
    <td class="hash-cell"><span class="hash-text" title="${e(item.outputID)}">${e(item.outputID)}</span></td>
    <td class="numeric">${amount(item.value)}</td>
  </tr>`).join('') : tableEmpty(3, 'None');
  return `<div class="table-scroll"><table class="data-table io-table"><thead><tr><th>Address</th><th>Output ID</th><th class="numeric">Value</th></tr></thead><tbody>${rows}</tbody></table></div>`;
}

async function renderHome(token) {
  const [status, blocks, transactions] = await Promise.all([
    api('/api/status'),
    api('/api/blocks?limit=10'),
    api('/api/transactions/recent?limit=10')
  ]);
  if (token !== routeVersion) return;
  statusCache = status;
  updateBar(status);
  document.title = 'QDAY Explorer';
  paint(token, `
    <section class="overview-heading"><div><span class="section-label">QDAY MAINNET</span><h1>Blockchain explorer</h1><p>Blocks, transactions, addresses and consensus status.</p></div><span class="live-update"><i></i> Live updates</span></section>
    ${searchPanel()}
    <section class="overview-dashboard">
      ${chainMetric(status)}
      ${proofOfWorkMetric(status)}
      ${networkMetric(status)}
      ${supplyMetric(status)}
      ${emissionMetric(status)}
    </section>
    ${card('Latest blocks', blockTable(blocks.blocks), '<a class="card-action route-link" href="/blocks">View all blocks →</a>')}
    ${card('Latest transactions', transactionTable(transactions.transactions), '<a class="card-action route-link" href="/transactions">View all transactions →</a>')}
  `);
}

async function renderBlocks(token) {
  const requestedOffset = Math.max(0, Number(new URLSearchParams(location.search).get('offset')) || 0);
  const data = await api(`/api/blocks?limit=50&offset=${requestedOffset}`);
  if (token !== routeVersion) return;
  document.title = 'Blocks | QDAY Explorer';
  const total = Number(data.total ?? data.tip + 1);
  const offset = Number(data.offset || 0);
  const limit = Number(data.limit || 50);
  paint(token, `
    ${pageHeader('Blocks', 'Canonical QDAY mainnet blocks, newest first.', [{label:'Overview',href:'/'},{label:'Blocks'}], '/', 'Overview')}
    ${card('Block list', blockTable(data.blocks), `<span class="card-meta">${commas(total)} total</span>`)}
    ${pagination({base:'/blocks', offset, limit, total, emptyLabel:'No blocks'})}
  `);
}

async function renderTransactions(token) {
  const requestedOffset = Math.max(0, Number(new URLSearchParams(location.search).get('offset')) || 0);
  const data = await api(`/api/transactions/recent?limit=50&offset=${requestedOffset}`);
  if (token !== routeVersion) return;
  document.title = 'Transactions | QDAY Explorer';
  const offset = Number(data.offset || 0);
  const limit = Number(data.limit || 50);
  const total = Number(data.total || 0);
  paint(token, `
    ${pageHeader('Transactions', 'Confirmed and mempool transactions, newest first.', [{label:'Overview',href:'/'},{label:'Transactions'}], '/', 'Overview')}
    ${card('Transaction list', transactionTable(data.transactions), `<span class="card-meta">${data.mempool || 0} in mempool</span>`)}
    ${pagination({base:'/transactions', offset, limit, total})}
  `);
}

async function renderBlock(id, token) {
  const [block, status] = await Promise.all([api(`/api/blocks/${encodeURIComponent(id)}`), api('/api/status')]);
  if (token !== routeVersion) return;
  statusCache = status;
  updateBar(status);
  document.title = `Block ${commas(block.height)} | QDAY Explorer`;
  const blockNavigation = `<nav class="record-navigation">
    ${block.height > 0 ? `<a class="route-link" href="/block/${block.height - 1}">← Block ${commas(block.height - 1)}</a>` : '<span></span>'}
    <a class="route-link" href="/blocks">All blocks</a>
    ${block.height < status.height ? `<a class="route-link" href="/block/${block.height + 1}">Block ${commas(block.height + 1)} →</a>` : '<span></span>'}
  </nav>`;
  paint(token, `
    ${pageHeader(`Block ${commas(block.height)}`, `${commas(block.confirmations)} confirmation${block.confirmations === 1 ? '' : 's'}`, [{label:'Overview',href:'/'},{label:'Blocks',href:'/blocks'},{label:`Block ${commas(block.height)}`}], '/blocks', 'Back to blocks')}
    ${identifier('Block ID', block.id)}
    ${card('Overview', detailList([
      ['Timestamp', exactTime(block.timestamp)],
      ['Confirmations', commas(block.confirmations)],
      ['Miner', block.miner ? `<a class="hash-link route-link" href="/address/${e(block.miner)}">${e(block.miner)}</a>` : 'Genesis', Boolean(block.miner)],
      ['Reward', `${roundedCoins(block.reward.qday)} QDAY`],
      ['Base reward', `${roundedCoins(block.baseReward.qday)} QDAY`],
      ['Fees', `${roundedCoins(block.fees.qday)} QDAY`],
      ['Transactions', commas(block.transactions.length)],
      ['Miner markers', commas(block.minerMarkerCount)],
      ['Difficulty', commas(block.difficulty)],
      ['Nonce', block.nonce],
      ['Parent block', block.height ? `<a class="hash-link route-link" href="/block/${e(block.parentID)}">${e(block.parentID)}</a>` : '—', block.height > 0],
      ['Target', `<code>${e(block.target)}</code>`, true],
      ['Commitment', `<code>${e(block.commitment)}</code>`, true],
      ['PQ Day state', qdayStageLabel(block.qday.stage)]
    ]))}
    ${card('Transactions', transactionTable(block.transactions), `<span class="card-meta">${commas(block.transactions.length)}</span>`)}
    ${blockNavigation}
  `);
}

async function renderTransaction(id, token) {
  const [transaction, status] = await Promise.all([api(`/api/transactions/${encodeURIComponent(id)}`), api('/api/status')]);
  if (token !== routeVersion) return;
  statusCache = status;
  updateBar(status);
  document.title = `Transaction ${short(transaction.id)} | QDAY Explorer`;
  const blockValue = transaction.mempool ? '<span class="status pending">Mempool</span>' : `<a class="primary-link route-link" href="/block/${e(transaction.blockID)}">Block ${commas(transaction.height)}</a>`;
  const overview = [
    ['Status', transaction.mempool ? 'Mempool' : 'Confirmed'],
    ['Block', blockValue, true],
    ['Timestamp', transaction.mempool ? 'Pending' : exactTime(transaction.timestamp)],
    ['Confirmations', commas(transaction.confirmations)],
    ['Type', transactionKindLabel(transaction.kind)],
    ['Input total', `${roundedCoins(transaction.inputTotal.qday)} QDAY`],
    ['Output total', `${roundedCoins(transaction.outputTotal.qday)} QDAY`],
    ['Fee', `${roundedCoins(transaction.fee.qday)} QDAY`],
    ['DEFEND nonce', transaction.defendNonce]
  ];
  if (transaction.burned?.atomic !== '0') overview.splice(7, 0, ['Burned', `${roundedCoins(transaction.burned.qday)} QDAY`]);
  paint(token, `
    ${pageHeader('Transaction', transaction.mempool ? 'Unconfirmed' : `${commas(transaction.confirmations)} confirmation${transaction.confirmations === 1 ? '' : 's'}`, [{label:'Overview',href:'/'},{label:'Transactions',href:'/transactions'},{label:short(transaction.id)}], '/transactions', 'Back to transactions')}
    ${identifier('Transaction ID', transaction.id)}
    ${transaction.qdayProof ? '<div class="notice">This transaction contains a valid PQ Day canary proof.</div>' : ''}
    ${card('Overview', detailList(overview))}
    <section class="split-grid">${card('Inputs', ioTable(transaction.inputs), `<span class="card-meta">${commas(transaction.inputs.length)}</span>`)}${card('Outputs', ioTable(transaction.outputs), `<span class="card-meta">${commas(transaction.outputs.length)}</span>`)}</section>
    <nav class="record-navigation"><span></span><a class="route-link" href="/transactions">All transactions</a><span></span></nav>
  `);
}

async function renderAddress(value, token) {
  const requestedOffset = Math.max(0, Number(new URLSearchParams(location.search).get('offset')) || 0);
  const [data, status] = await Promise.all([
    api(`/api/addresses/${encodeURIComponent(value)}?limit=50&offset=${requestedOffset}`),
    api('/api/status')
  ]);
  if (token !== routeVersion) return;
  statusCache = status;
  updateBar(status);
  document.title = `Address ${short(data.address)} | QDAY Explorer`;
  const offset = Number(data.offset || 0);
  const limit = Number(data.limit || 50);
  const total = Number(data.total || 0);
  paint(token, `
    ${pageHeader('Address', `Indexed at block ${commas(data.indexedHeight)}`, [{label:'Overview',href:'/'},{label:'Address'},{label:short(data.address)}], '/', 'Overview')}
    ${identifier('QDAY address', data.address)}
    <section class="metrics-grid address-metrics">
      ${metric('Spendable balance', `${roundedCoins(data.balance.qday)} QDAY`)}
      ${metric('Immature balance', `${roundedCoins(data.immature.qday)} QDAY`)}
      ${metric('Unspent outputs', commas(data.liveOutputs))}
      ${metric('Shielded outputs', commas(data.shielded))}
      ${metric('Decaying outputs', commas(data.decaying))}
      ${metric('Expired outputs', commas(data.expired))}
    </section>
    ${card('Address history', eventTable(data.history), `<span class="card-meta">${commas(total)} events</span>`)}
    ${pagination({base:`/address/${data.address}`, offset, limit, total, emptyLabel:'No address history'})}
  `);
}

function premineStat(label, value, className = '', href = '') {
  const content = `<span>${e(label)}</span><strong>${amount(value)}</strong>`;
  return href
    ? `<a class="premine-stat ${e(className)} route-link" href="${e(href)}">${content}<small>View address →</small></a>`
    : `<div class="premine-stat ${e(className)}">${content}</div>`;
}

async function renderPremine(token) {
  const requestedOffset = Math.max(0, Number(new URLSearchParams(location.search).get('offset')) || 0);
  const [data, status] = await Promise.all([
    api(`/api/premine?limit=50&offset=${requestedOffset}`),
    api('/api/status')
  ]);
  if (token !== routeVersion) return;
  statusCache = status;
  updateBar(status);
  document.title = 'Premine | QDAY Explorer';
  const offset = Number(data.offset || 0);
  const limit = Number(data.limit || 50);
  const total = Number(data.total || 0);
  const indexState = data.synced ? `CHAIN VERIFIED · BLOCK ${commas(data.indexedHeight)}` : `INDEXING · BLOCK ${commas(data.indexedHeight)}`;
  paint(token, `
    ${breadcrumbs([{label:'Overview',href:'/'},{label:'Premine'}])}
    <section class="premine-hero">
      <div class="premine-hero-copy">
        <h1>DEV WALLET</h1>
        <p>no trust me bro. the address is public. the balance is live. every coin that moves leaves a scar.</p>
      </div>
      <div class="premine-identity">
        <span class="premine-sync"><i></i>${e(indexState)}</span>
        <a class="premine-address route-link" href="/address/${e(data.address)}">
          <span>PREMINE ADDRESS</span>
          <code>${e(data.address)}</code>
          <b>OPEN ADDRESS →</b>
        </a>
      </div>
    </section>
    <section class="premine-stats" aria-label="Premine balances">
      ${premineStat('Premine', data.premine)}
      ${premineStat('Burned from premine', data.burnedFromPremine, 'burned')}
      ${premineStat('Left from premine', data.leftFromPremine, 'left')}
      ${premineStat('Dev wallet balance', data.devWalletBalance, 'wallet', `/address/${data.address}`)}
    </section>
    <section class="card premine-activity">
      <header class="card-header premine-activity-header">
        <div><h2>EVERY MOVE.</h2><p>burns, sends, incoming junk, whatever happens here.</p></div>
        <span class="card-meta">${commas(data.confirmedBurnTransactions)} confirmed burn${data.confirmedBurnTransactions === 1 ? '' : 's'}</span>
      </header>
      ${eventTable(data.history)}
    </section>
    ${pagination({base:'/premine', offset, limit, total, emptyLabel:'No activity'})}
  `);
}

async function renderRichList(token) {
  const [data, status] = await Promise.all([api('/api/rich-list'), api('/api/status')]);
  if (token !== routeVersion) return;
  statusCache = status;
  updateBar(status);
  document.title = 'Rich List | QDAY Explorer';
  paint(token, `
    ${pageHeader('Rich list', `Top ${commas(data.limit || 20)} confirmed spendable balances at block ${commas(data.indexedHeight)}.`, [{label:'Overview',href:'/'},{label:'Rich list'}], '/', 'Overview')}
    ${card('Top addresses', richListTable(data.entries, status.currentSupply), `<span class="card-meta">${commas(data.addresses)} funded address${data.addresses === 1 ? '' : 'es'}</span>`)}
    <section class="information-card rich-list-note"><h2>Balance scope</h2><p>Confirmed mature outputs only. Mempool transactions, immature block rewards and burned outputs are excluded.</p></section>
  `);
}

function qdaySummary(status) {
  const qday = status.qday;
  if (qday.stage === 'ACTIVE') return `PQ Day began at block ${commas(qday.height)}.`;
  if (qday.stage === 'COUNTDOWN') return `Valid proof confirmed. ${commas(qday.blocksRemaining)} blocks remain before PQ Day.`;
  if (qday.proofPending) return 'A valid canary proof is currently in the mempool.';
  return 'No valid canary proof is present in the mempool or confirmed chain.';
}

async function renderQday(token) {
  const status = await api('/api/status');
  if (token !== routeVersion) return;
  statusCache = status;
  updateBar(status);
  document.title = 'PQ Day Status | QDAY Explorer';
  const proof = status.qday.proofPending || status.qday.proofTransaction;
  paint(token, `
    ${pageHeader('PQ Day status', qdaySummary(status), [{label:'Overview',href:'/'},{label:'PQ Day status'}], '/', 'Overview')}
    <section class="state-card"><div><span>Consensus state</span><strong>${e(qdayStageLabel(status.qday.stage))}</strong></div><div><span>Accepted proofs</span><strong>${commas(status.qday.acceptedProofs)}</strong></div><div><span>Mempool proof</span><strong>${status.qday.proofPending ? 'YES' : 'NO'}</strong></div></section>
    ${identifier('Fixed Edwards25519 canary', status.qday.canary)}
    ${card('Consensus parameters', detailList([
      ['Challenge', status.qday.challenge],
      ['Witness format', status.qday.witness],
      ['Proof fee', `${roundedCoins(status.qday.proofFee.qday)} QDAY`],
      ['PQ Day fuse', `${commas(status.qday.activationDelay)} blocks`],
      ['PQ Day height', status.qday.height ? commas(status.qday.height) : 'Not set'],
      ['Denomination multiplier', `×${commas(status.qday.denominationMultiplier)}`],
      ['Shield period', `${commas(status.qday.shieldBlocks)} blocks`],
      ['Decay period', `${commas(status.qday.decayBlocks)} blocks`],
      ['DEFEND work', `${commas(status.qday.defendBits)} bits`],
      ['Proof transaction', proof ? `<a class="hash-link route-link" href="/transaction/${e(proof)}">${e(proof)}</a>` : 'None', Boolean(proof)]
    ]))}
    <section class="information-card"><h2>Proof visibility</h2><p>Invalid solutions are rejected before mempool admission and are not relayed. The network can only report an accepted proof.</p></section>
  `);
}

function renderError(error, token) {
  document.title = 'Not found | QDAY Explorer';
  paint(token, `<section class="error-page"><span>404</span><h1>Not found</h1><p>${e(error.message || error)}</p><a class="button route-link" href="/">Back to overview</a></section>`);
}

function updateNavigation() {
  const first = location.pathname.split('/').filter(Boolean)[0] || 'overview';
  const section = first === 'block' ? 'blocks' : first === 'transaction' ? 'transactions' : first === 'death-watch' ? 'qday' : first;
  document.querySelectorAll('[data-nav]').forEach(link => {
    const active = link.dataset.nav === section;
    link.classList.toggle('active', active);
    if (active) link.setAttribute('aria-current', 'page');
    else link.removeAttribute('aria-current');
  });
}

async function route(options = {}) {
  const live = options.live === true;
  const token = ++routeVersion;
  const scrollPosition = window.scrollY;
  const parts = location.pathname.split('/').filter(Boolean);
  updateNavigation();
  if (!live) paint(token, '<section class="loading-page"><div class="loader"></div><p>Loading explorer data…</p></section>');
  try {
    if (!parts.length) await renderHome(token);
    else if (parts[0] === 'blocks' && parts.length === 1) await renderBlocks(token);
    else if (parts[0] === 'transactions' && parts.length === 1) await renderTransactions(token);
    else if (parts[0] === 'block' && parts[1]) await renderBlock(parts.slice(1).join('/'), token);
    else if (parts[0] === 'transaction' && parts[1]) await renderTransaction(parts.slice(1).join('/'), token);
    else if (parts[0] === 'address' && parts[1]) await renderAddress(parts.slice(1).join('/'), token);
    else if (parts[0] === 'premine' && parts.length === 1) await renderPremine(token);
    else if ((parts[0] === 'qday' || parts[0] === 'death-watch') && parts.length === 1) await renderQday(token);
    else if (parts[0] === 'rich-list' && parts.length === 1) await renderRichList(token);
    else throw new Error('The requested explorer page does not exist.');
    if (token !== routeVersion) return;
    renderedFingerprint = statusFingerprint(statusCache);
  } catch (error) {
    if (!live && token === routeVersion) renderError(error, token);
  }
  if (token !== routeVersion) return;
  if (live) requestAnimationFrame(() => token === routeVersion && scrollTo(0, scrollPosition));
  else scrollTo({top:0, behavior:'instant'});
}

function navigate(href) {
  history.pushState({}, '', href);
  route();
}

function updateBar(status) {
  if (!status) return;
  document.querySelector('#network-pill').textContent = status.network.replace('qday-', '').toUpperCase();
  const pill = document.querySelector('.live-pill');
  pill.classList.toggle('connecting', !status.synced);
  pill.title = status.synced ? `Synced at block ${commas(status.height)}` : `Indexing ${commas(status.indexedHeight)} of ${commas(status.height)}`;
}

async function submitSearch(form) {
  const query = new FormData(form).get('q')?.trim();
  if (!query) return;
  const button = form.querySelector('button');
  button.disabled = true;
  try {
    const result = await api(`/api/search?q=${encodeURIComponent(query)}`);
    navigate(result.path);
  } catch (error) {
    showToast(error.message || 'No result found');
  } finally {
    button.disabled = false;
  }
}

document.addEventListener('click', event => {
  const internalLink = event.target.closest('a.route-link');
  if (internalLink && internalLink.origin === location.origin) {
    event.preventDefault();
    navigate(internalLink.getAttribute('href'));
    return;
  }
  const copy = event.target.closest('[data-copy]');
  if (copy) {
    navigator.clipboard.writeText(copy.dataset.copy).then(() => showToast('Copied'));
    return;
  }
  const row = event.target.closest('tr[data-href]');
  if (row) navigate(row.dataset.href);
});

document.addEventListener('keydown', event => {
  const row = event.target.closest('tr[data-href]');
  if (row && (event.key === 'Enter' || event.key === ' ')) {
    event.preventDefault();
    navigate(row.dataset.href);
  }
});

document.addEventListener('submit', event => {
  const form = event.target.closest('[data-search]');
  if (!form) return;
  event.preventDefault();
  submitSearch(form);
});

window.addEventListener('popstate', route);

async function refreshStatus() {
  if (refreshRunning) return;
  refreshRunning = true;
  try {
    const status = await api('/api/status');
    statusCache = status;
    updateBar(status);
    const active = document.activeElement;
    const editing = active && active.matches('input, textarea, select');
    if (!document.hidden && !editing && statusFingerprint(status) !== renderedFingerprint) await route({live:true});
  } catch (_) {
  } finally {
    refreshRunning = false;
  }
}

route().then(refreshStatus);
setInterval(refreshStatus, 4000);
document.addEventListener('visibilitychange', () => {
  if (!document.hidden) refreshStatus();
});

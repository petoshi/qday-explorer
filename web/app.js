const root = document.querySelector('#app');
const toast = document.querySelector('#toast');

let statusCache = null;
let renderedFingerprint = '';
let routeVersion = 0;
let refreshRunning = false;
let toastTimer;

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

function amount(value) {
  return `${commas(value?.qday || '0')} <span class="unit">QDAY</span>`;
}

function statusFingerprint(status) {
  if (!status) return '';
  return [
    status.height,
    status.indexedHeight,
    status.mempoolTransactions,
    status.qday.stage,
    status.qday.height,
    status.qday.proofPending,
    status.qday.proofTransaction
  ].join('|');
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

function metric(label, value, note = '', href = '') {
  const content = `<span>${e(label)}</span><strong>${e(value)}</strong>${note ? `<small>${e(note)}</small>` : ''}`;
  return href ? `<a class="metric route-link" href="${e(href)}">${content}</a>` : `<div class="metric">${content}</div>`;
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
    <td><span class="type-badge ${tx.mempool ? 'pending' : tx.kind === 'QDAY PROOF' ? 'qday' : ''}">${e(tx.kind)}</span></td>
    <td class="hash-cell"><a class="hash-link route-link" href="/transaction/${e(tx.id)}" title="${e(tx.id)}">${e(tx.id)}</a></td>
    <td>${tx.mempool ? '<span class="status pending">Mempool</span>' : `<a class="primary-link route-link" href="/block/${e(tx.height)}">${commas(tx.height)}</a>`}</td>
    <td title="${e(exactTime(tx.timestamp))}">${e(relativeTime(tx.timestamp))}</td>
    <td class="address-cell">${tx.from ? `<a class="hash-link route-link" href="/address/${e(tx.from)}" title="${e(tx.from)}">${e(tx.from)}</a>` : '—'}</td>
    <td class="address-cell">${tx.to ? `<a class="hash-link route-link" href="/address/${e(tx.to)}" title="${e(tx.to)}">${e(tx.to)}</a>` : '—'}</td>
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
    <td><span class="type-badge ${event.kind === 'QDAY PROOF' ? 'qday' : ''}">${e(event.kind)}</span></td>
    <td class="hash-cell"><a class="hash-link route-link" href="/${e(event.target)}/${e(event.linkID)}" title="${e(event.id)}">${e(event.id)}</a></td>
    <td><a class="primary-link route-link" href="/block/${e(event.height)}">${commas(event.height)}</a></td>
    <td title="${e(exactTime(event.timestamp))}">${e(relativeTime(event.timestamp))}</td>
    <td><span class="direction ${event.direction.toLowerCase()}">${e(event.direction)}</span></td>
    <td class="numeric">${amount(event.value)}</td>
  </tr>`).join('') : tableEmpty(6, 'No address history found.');
  return `<div class="table-scroll"><table class="data-table events-table">
    <thead><tr><th>Type</th><th>Event ID</th><th>Block</th><th>Age</th><th>Direction</th><th class="numeric">Amount</th></tr></thead>
    <tbody>${rows}</tbody>
  </table></div>`;
}

function card(title, content, action = '') {
  return `<section class="card"><header class="card-header"><h2>${e(title)}</h2>${action}</header>${content}</section>`;
}

function pagination({newer, older, label}) {
  return `<nav class="pagination" aria-label="Pagination">
    ${newer ? `<a class="button secondary route-link" href="${e(newer)}">← Previous</a>` : '<span></span>'}
    <span>${e(label)}</span>
    ${older ? `<a class="button secondary route-link" href="${e(older)}">Next →</a>` : '<span></span>'}
  </nav>`;
}

function detailList(items) {
  return `<dl class="detail-list">${items.map(([label, value, raw = false]) => `<div><dt>${e(label)}</dt><dd>${raw ? value : e(value)}</dd></div>`).join('')}</dl>`;
}

function identifier(label, value) {
  return `<section class="identifier"><div><span>${e(label)}</span><code>${e(value)}</code></div>${copyButton(value)}</section>`;
}

function ioTable(entries) {
  const rows = entries?.length ? entries.map(item => `<tr>
    <td class="hash-cell"><a class="hash-link route-link" href="/address/${e(item.address)}" title="${e(item.address)}">${e(item.address)}</a></td>
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
    <section class="metrics-grid">
      ${metric('Latest block', commas(status.height), exactTime(status.lastBlock), `/block/${status.height}`)}
      ${metric('Network hashrate', status.estimatedHashrate, 'BLAKE2b-256 estimate')}
      ${metric('Difficulty', commas(status.difficulty), `Target ${short(status.target, 8, 8)}`)}
      ${metric('Gross supply', `${commas(status.grossSupply.qday)} QDAY`, `Reward ${commas(status.blockReward.qday)} QDAY`)}
      ${metric('Connected peers', commas(status.peers), `${commas(status.mempoolTransactions)} mempool transactions`)}
      ${metric('QDAY state', status.qday.stage, status.qday.stage === 'WAITING' ? 'Canary proof not confirmed' : `Activation height ${commas(status.qday.height)}`, '/qday')}
    </section>
    ${card('Latest blocks', blockTable(blocks.blocks), '<a class="card-action route-link" href="/blocks">View all blocks →</a>')}
    ${card('Latest transactions', transactionTable(transactions.transactions), '<a class="card-action route-link" href="/transactions">View all transactions →</a>')}
  `);
}

async function renderBlocks(token) {
  const offset = Math.max(0, Number(new URLSearchParams(location.search).get('offset')) || 0);
  const data = await api(`/api/blocks?limit=50&offset=${offset}`);
  if (token !== routeVersion) return;
  document.title = 'Blocks | QDAY Explorer';
  const total = data.tip + 1;
  const start = data.blocks.length ? offset + 1 : 0;
  const end = offset + data.blocks.length;
  paint(token, `
    ${pageHeader('Blocks', 'Canonical QDAY mainnet blocks, newest first.', [{label:'Overview',href:'/'},{label:'Blocks'}], '/', 'Overview')}
    ${card('Block list', blockTable(data.blocks), `<span class="card-meta">${commas(total)} total</span>`)}
    ${pagination({
      newer: offset ? `/blocks?offset=${Math.max(0, offset - 50)}` : '',
      older: end < total ? `/blocks?offset=${offset + 50}` : '',
      label: `${commas(start)}–${commas(end)} of ${commas(total)}`
    })}
  `);
}

async function renderTransactions(token) {
  const offset = Math.max(0, Number(new URLSearchParams(location.search).get('offset')) || 0);
  const data = await api(`/api/transactions/recent?limit=50&offset=${offset}`);
  if (token !== routeVersion) return;
  document.title = 'Transactions | QDAY Explorer';
  const start = data.transactions.length ? offset + 1 : 0;
  const end = offset + data.transactions.length;
  paint(token, `
    ${pageHeader('Transactions', 'Confirmed and mempool transactions, newest first.', [{label:'Overview',href:'/'},{label:'Transactions'}], '/', 'Overview')}
    ${card('Transaction list', transactionTable(data.transactions), `<span class="card-meta">${data.mempool || 0} in mempool</span>`)}
    ${pagination({
      newer: offset ? `/transactions?offset=${Math.max(0, offset - 50)}` : '',
      older: data.hasMore ? `/transactions?offset=${offset + 50}` : '',
      label: data.transactions.length ? `${commas(start)}–${commas(end)}` : 'No results'
    })}
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
      ['Reward', `${commas(block.reward.qday)} QDAY`],
      ['Base reward', `${commas(block.baseReward.qday)} QDAY`],
      ['Fees', `${commas(block.fees.qday)} QDAY`],
      ['Transactions', commas(block.transactions.length)],
      ['Miner markers', commas(block.minerMarkerCount)],
      ['Difficulty', commas(block.difficulty)],
      ['Nonce', block.nonce],
      ['Parent block', block.height ? `<a class="hash-link route-link" href="/block/${e(block.parentID)}">${e(block.parentID)}</a>` : '—', block.height > 0],
      ['Target', `<code>${e(block.target)}</code>`, true],
      ['Commitment', `<code>${e(block.commitment)}</code>`, true],
      ['QDAY state', block.qday.stage]
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
  paint(token, `
    ${pageHeader('Transaction', transaction.mempool ? 'Unconfirmed' : `${commas(transaction.confirmations)} confirmation${transaction.confirmations === 1 ? '' : 's'}`, [{label:'Overview',href:'/'},{label:'Transactions',href:'/transactions'},{label:short(transaction.id)}], '/transactions', 'Back to transactions')}
    ${identifier('Transaction ID', transaction.id)}
    ${transaction.qdayProof ? '<div class="notice">This transaction contains a valid QDAY canary proof.</div>' : ''}
    ${card('Overview', detailList([
      ['Status', transaction.mempool ? 'Mempool' : 'Confirmed'],
      ['Block', blockValue, true],
      ['Timestamp', transaction.mempool ? 'Pending' : exactTime(transaction.timestamp)],
      ['Confirmations', commas(transaction.confirmations)],
      ['Type', transaction.kind],
      ['Input total', `${commas(transaction.inputTotal.qday)} QDAY`],
      ['Output total', `${commas(transaction.outputTotal.qday)} QDAY`],
      ['Fee', `${commas(transaction.fee.qday)} QDAY`],
      ['DEFEND nonce', transaction.defendNonce]
    ]))}
    <section class="split-grid">${card('Inputs', ioTable(transaction.inputs), `<span class="card-meta">${commas(transaction.inputs.length)}</span>`)}${card('Outputs', ioTable(transaction.outputs), `<span class="card-meta">${commas(transaction.outputs.length)}</span>`)}</section>
    <nav class="record-navigation"><span></span><a class="route-link" href="/transactions">All transactions</a><span></span></nav>
  `);
}

async function renderAddress(value, token) {
  const offset = Math.max(0, Number(new URLSearchParams(location.search).get('offset')) || 0);
  const [data, status] = await Promise.all([
    api(`/api/addresses/${encodeURIComponent(value)}?limit=50&offset=${offset}`),
    api('/api/status')
  ]);
  if (token !== routeVersion) return;
  statusCache = status;
  updateBar(status);
  document.title = `Address ${short(data.address)} | QDAY Explorer`;
  const start = data.history.length ? offset + 1 : 0;
  const end = offset + data.history.length;
  paint(token, `
    ${pageHeader('Address', `Indexed at block ${commas(data.indexedHeight)}`, [{label:'Overview',href:'/'},{label:'Address'},{label:short(data.address)}], '/', 'Overview')}
    ${identifier('QDAY address', data.address)}
    <section class="metrics-grid address-metrics">
      ${metric('Spendable balance', `${commas(data.balance.qday)} QDAY`)}
      ${metric('Immature balance', `${commas(data.immature.qday)} QDAY`)}
      ${metric('Unspent outputs', commas(data.liveOutputs))}
      ${metric('Shielded outputs', commas(data.shielded))}
      ${metric('Decaying outputs', commas(data.decaying))}
      ${metric('Expired outputs', commas(data.expired))}
    </section>
    ${card('Address history', eventTable(data.history), `<span class="card-meta">Page ${commas(Math.floor(offset / 50) + 1)}</span>`)}
    ${pagination({
      newer: offset ? `/address/${e(data.address)}?offset=${Math.max(0, offset - 50)}` : '',
      older: data.hasMore ? `/address/${e(data.address)}?offset=${offset + 50}` : '',
      label: data.history.length ? `${commas(start)}–${commas(end)}` : 'No results'
    })}
  `);
}

function qdaySummary(status) {
  const qday = status.qday;
  if (qday.stage === 'ACTIVE') return `Active since block ${commas(qday.height)}.`;
  if (qday.stage === 'COUNTDOWN') return `Valid proof confirmed. ${commas(qday.blocksRemaining)} blocks remain before activation.`;
  if (qday.proofPending) return 'A valid canary proof is currently in the mempool.';
  return 'No valid canary proof is present in the mempool or confirmed chain.';
}

async function renderQday(token) {
  const status = await api('/api/status');
  if (token !== routeVersion) return;
  statusCache = status;
  updateBar(status);
  document.title = 'QDAY Status | QDAY Explorer';
  const proof = status.qday.proofPending || status.qday.proofTransaction;
  paint(token, `
    ${pageHeader('QDAY status', qdaySummary(status), [{label:'Overview',href:'/'},{label:'QDAY status'}], '/', 'Overview')}
    <section class="state-card"><div><span>Consensus state</span><strong>${e(status.qday.stage)}</strong></div><div><span>Accepted proofs</span><strong>${commas(status.qday.acceptedProofs)}</strong></div><div><span>Mempool proof</span><strong>${status.qday.proofPending ? 'YES' : 'NO'}</strong></div></section>
    ${identifier('Fixed Edwards25519 canary', status.qday.canary)}
    ${card('Consensus parameters', detailList([
      ['Challenge', status.qday.challenge],
      ['Witness format', status.qday.witness],
      ['Proof fee', `${commas(status.qday.proofFee.qday)} QDAY`],
      ['Activation delay', `${commas(status.qday.activationDelay)} blocks`],
      ['Activation height', status.qday.height ? commas(status.qday.height) : 'Not set'],
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
    else if ((parts[0] === 'qday' || parts[0] === 'death-watch') && parts.length === 1) await renderQday(token);
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

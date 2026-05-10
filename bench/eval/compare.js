#!/usr/bin/env node
'use strict';

/**
 * compare.js — parse promptfoo results JSON and print a token savings report.
 *
 * Usage:
 *   node compare.js results-fetch.json [results-git.json ...]
 *
 * Each file is the output of:
 *   promptfoo eval -c <config>.yaml --output <file>.json
 */

const fs   = require('fs');
const path = require('path');

const files = process.argv.slice(2);
if (files.length === 0) {
  console.error('Usage: node compare.js results-fetch.json [results-git.json ...]');
  process.exit(1);
}

// ── load and parse ────────────────────────────────────────────────────────────

const allResults = [];
for (const file of files) {
  let raw;
  try { raw = JSON.parse(fs.readFileSync(file, 'utf8')); }
  catch (err) { console.error(`Cannot read ${file}: ${err.message}`); continue; }

  // promptfoo output format: { results: { results: [...] }, ... }
  // or just { results: [...] } depending on version
  const rows = raw?.results?.results ?? raw?.results ?? [];
  if (!Array.isArray(rows)) {
    console.error(`Unexpected format in ${file}`);
    continue;
  }
  for (const row of rows) {
    allResults.push({ file: path.basename(file, '.json'), row });
  }
}

if (allResults.length === 0) {
  console.error('No results found.');
  process.exit(1);
}

// ── group by (description, file) then split by provider label ─────────────────

// key: "file::description"
const groups = new Map();

for (const { file, row } of allResults) {
  const desc     = row.testCase?.description || row.description || row.prompt?.raw?.slice(0, 60) || '(unknown)';
  const label    = row.provider?.label || row.provider?.id || 'unknown';
  const key      = `${file}::${desc}`;

  if (!groups.has(key)) groups.set(key, { file, desc, providers: new Map() });
  const g = groups.get(key);

  const tokens  = row.response?.tokenUsage || {};
  const meta    = row.response?.metadata   || {};
  const pass    = row.gradingResult?.pass  ?? null;
  const score   = row.gradingResult?.score ?? null;

  g.providers.set(label, {
    label,
    total:     tokens.total      || 0,
    input:     tokens.prompt     || 0,
    output:    tokens.completion || 0,
    turns:     meta.turns        || null,
    toolCalls: meta.toolCallCount|| null,
    pass,
    score,
    error:     row.error         || null,
  });
}

// ── report ────────────────────────────────────────────────────────────────────

const sep   = '─'.repeat(100);
const sep2  = '─'.repeat(100);

console.log('\n' + sep);
console.log('MCP-CONTEXT-GUARD — TOKEN SAVINGS REPORT');
console.log(sep);

for (const [, g] of groups) {
  const directLabel  = [...g.providers.keys()].find(l => l.endsWith('/direct'));
  const proxiedLabel = [...g.providers.keys()].find(l => l.endsWith('/proxied'));
  const direct  = directLabel  ? g.providers.get(directLabel)  : null;
  const proxied = proxiedLabel ? g.providers.get(proxiedLabel) : null;

  console.log(`\n${g.file} / ${g.desc}`);
  console.log(sep2);

  if (direct && proxied) {
    printComparison(direct, proxied);
  } else {
    for (const [, p] of g.providers) printProviderRow(p);
  }
}

// ── aggregate summary ─────────────────────────────────────────────────────────

console.log('\n' + sep);
console.log('AGGREGATE (all tests, cached pairs only)');
console.log(sep);

let sumDirect = 0, sumProxied = 0, pairs = 0;
const passCounts = { direct: 0, proxied: 0, total: 0 };

for (const [, g] of groups) {
  const directLabel  = [...g.providers.keys()].find(l => l.endsWith('/direct'));
  const proxiedLabel = [...g.providers.keys()].find(l => l.endsWith('/proxied'));
  const direct  = directLabel  ? g.providers.get(directLabel)  : null;
  const proxied = proxiedLabel ? g.providers.get(proxiedLabel) : null;
  if (!direct || !proxied) continue;
  if (direct.total === 0 || proxied.total === 0) continue;

  sumDirect  += direct.total;
  sumProxied += proxied.total;
  pairs++;
  passCounts.total++;
  if (direct.pass)  passCounts.direct++;
  if (proxied.pass) passCounts.proxied++;
}

if (pairs > 0) {
  const saved   = sumDirect - sumProxied;
  const pct     = (saved / sumDirect * 100).toFixed(1);
  console.log(`  Test pairs compared  : ${pairs}`);
  console.log(`  Total tokens (direct) : ${sumDirect.toLocaleString()}`);
  console.log(`  Total tokens (proxied): ${sumProxied.toLocaleString()}`);
  console.log(`  Tokens saved          : ${saved.toLocaleString()} (${pct}%)`);
  console.log(`  Pass rate (direct)    : ${passCounts.direct}/${passCounts.total}`);
  console.log(`  Pass rate (proxied)   : ${passCounts.proxied}/${passCounts.total}`);
} else {
  console.log('  No paired direct/proxied results found.');
  console.log('  Make sure provider labels end with /direct and /proxied.');
}
console.log('');

// ── helpers ───────────────────────────────────────────────────────────────────

function printComparison(direct, proxied) {
  const saved = direct.total - proxied.total;
  const pct   = direct.total > 0 ? (saved / direct.total * 100).toFixed(1) : '—';
  const passD = formatPass(direct.pass,  direct.score);
  const passP = formatPass(proxied.pass, proxied.score);

  const row = (label, d, p, extra = '') =>
    console.log(`  ${label.padEnd(30)}  ${d.padStart(12)}  ${p.padStart(12)}${extra}`);

  row('', 'direct', 'proxied', '      savings');
  row('total tokens', fmt(direct.total),  fmt(proxied.total),  `  ${(pct + '%').padStart(10)}`);
  row('input tokens', fmt(direct.input),  fmt(proxied.input));
  row('output tokens', fmt(direct.output), fmt(proxied.output));
  if (direct.turns !== null)
    row('turns (tool rounds)', String(direct.turns ?? '—'), String(proxied.turns ?? '—'));
  row('pass / score', passD, passP);
  if (direct.error)  console.log(`  direct error : ${direct.error}`);
  if (proxied.error) console.log(`  proxied error: ${proxied.error}`);
}

function printProviderRow(p) {
  console.log(`  ${p.label}: total=${fmt(p.total)} in=${fmt(p.input)} out=${fmt(p.output)} pass=${formatPass(p.pass, p.score)}`);
}

function fmt(n) {
  if (n == null || n === 0) return '—';
  return Number(n).toLocaleString();
}

function formatPass(pass, score) {
  if (pass === null) return '—';
  const s = score != null ? ` (${(score * 100).toFixed(0)}%)` : '';
  return pass ? `✓${s}` : `✗${s}`;
}

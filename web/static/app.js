'use strict';

const S = {
	summary: null,
	files: new Map(),
	commits: '',
	tab: 'files',
	view: { type: 'overview' },
	scopeView: { type: 'overview' },
	scope: 'section',
	sel: null,
	filter: '',
	hideDone: loadHideDone(),
	idx: null,
};

const $ = (id) => document.getElementById(id);

function h(tag, attrs, ...kids) {
	const el = document.createElement(tag);
	for (const [k, v] of Object.entries(attrs || {})) {
		if (v === undefined || v === null || v === false) continue;
		if (k === 'class') el.className = v;
		else if (k === 'text') el.textContent = v;
		else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
		else if (k === 'style') el.style.cssText = v;
		else el.setAttribute(k, v === true ? '' : v);
	}
	for (const kid of kids.flat()) {
		if (kid === undefined || kid === null || kid === false) continue;
		el.append(kid instanceof Node ? kid : document.createTextNode(String(kid)));
	}
	return el;
}

async function api(method, path, body) {
	const opt = { method, headers: {} };
	if (method !== 'GET') {
		opt.headers['X-Sedi'] = '1';
		if (body !== undefined) {
			opt.headers['Content-Type'] = 'application/json';
			opt.body = JSON.stringify(body);
		}
	}
	const res = await fetch(path, opt);
	const text = await res.text();
	if (!res.ok) throw new Error(text.trim() || res.statusText);
	return text ? JSON.parse(text) : null;
}

function toast(msg, ms = 3500) {
	const t = $('toast');
	t.textContent = msg;
	t.hidden = false;
	clearTimeout(toast.timer);
	toast.timer = setTimeout(() => { t.hidden = true; }, ms);
}

async function guarded(fn) {
	try {
		await fn();
	} catch (e) {
		toast('失敗しました: ' + e.message, 8000);
	}
}

async function copyText(text) {
	try {
		await navigator.clipboard.writeText(text);
		return true;
	} catch {
		const ta = h('textarea', { style: 'position:fixed;left:-9999px;top:0' });
		ta.value = text;
		document.body.append(ta);
		ta.select();
		const ok = document.execCommand('copy');
		ta.remove();
		if (ok) return true;
		$('copybox-text').value = text;
		$('copybox').showModal();
		$('copybox-text').select();
		return false;
	}
}

const KIND = { instruction: '指示', question: '質問', reconsider: '再考', other: 'その他' };
const STATE = { unsent: '未送付', sent: '送付済み', replied: '返事あり', closed: '閉じる' };
const STATUS = { added: '追加', deleted: '削除', modified: '変更', renamed: '名前の変更', copied: 'コピー' };

// ---- 読み込み ----

async function load() {
	const sum = await api('GET', '/api/review');
	const commits = sum.review.baseCommit + '..' + sum.review.targetCommit + '@' + sum.review.updatedAt;
	if (commits !== S.commits) {
		S.files.clear();
		S.commits = commits;
	}
	S.summary = sum;
	buildIndex();
	render();
}

function buildIndex() {
	const sum = S.summary;
	const idx = { file: new Map(), hunk: new Map(), section: new Map(), threads: new Map(), orphans: [] };
	for (const f of sum.files) {
		idx.file.set(f.path, f);
		for (const hk of f.hunks || []) idx.hunk.set(hk.id, { file: f, hunk: hk });
	}
	for (const s of sum.sections) idx.section.set(s.key, s);
	for (const t of sum.threads) {
		if (!t.found) idx.orphans.push(t);
		const key = t.place ? 'l:' + t.place.hunk + ':' + t.place.end : anchorKey(t.anchor);
		if (!idx.threads.has(key)) idx.threads.set(key, []);
		idx.threads.get(key).push(t);
	}
	S.idx = idx;
}

function anchorKey(a) {
	switch (a.type) {
		case 'review': return 'r';
		case 'section': return 's:' + a.section;
		case 'file': return 'f:' + a.path;
		case 'hunk': return 'h:' + a.hunk;
		case 'line': return 'l:' + a.hunk + ':' + (a.end || 0);
		case 'lines': return 'L:' + a.path + ':' + a.from;
	}
	return '?';
}

function threadsAt(key) {
	return S.idx.threads.get(key) || [];
}

async function loadFile(path) {
	if (S.files.has(path)) return S.files.get(path);
	const f = await api('GET', '/api/file?path=' + encodeURIComponent(path));
	S.files.set(path, f);
	return f;
}

// ---- 描画 ----

function render() {
	const y = window.scrollY;
	renderTop();
	renderNav();
	renderMain();
	window.scrollTo(0, y);
}

function short(c) { return (c || '').slice(0, 10); }

function renderTop() {
	const r = S.summary.review;
	const op = r.spec.threeDot ? '...' : ' ';
	$('range').textContent = `${r.spec.base}${op}${r.spec.target}  (${short(r.baseCommit)}..${short(r.targetCommit)})`;
	const p = S.summary.presence;
	const chip = $('presence');
	chip.textContent = p.attached ? 'エージェント: 待ち受け中' : 'エージェント: 待ち受けなし';
	chip.classList.toggle('on', !!p.attached);
	$('btn-send-all').hidden = !p.attached;
}

function navItem(active, check, name, count, onclick, indent = 0) {
	return h('div', {
		class: 'nav-item' + (active ? ' active' : ''), onclick, style: indent ? `padding-left:${12 + indent * 14}px` : null,
	}, h('span', { class: 'check', text: check ? '✓' : '' }), h('span', { class: 'name', text: name, title: name }), count !== undefined ? h('span', { class: 'count', text: count }) : null);
}

function renderNav() {
	renderSectionNav();
	renderListNav();
}

function renderSectionNav() {
	const sum = S.summary;
	const nav = $('nav-sections');
	nav.replaceChildren();
	const secDone = sum.sections.filter((s) => s.marked).length;
	const fileDone = sum.files.filter((f) => f.marked).length;
	$('progress').textContent = `確認済み  セクション ${secDone}/${sum.sections.length}  ・  ファイル ${fileDone}/${sum.files.length}`;
	const sv = S.scopeView;
	nav.append(navItem(sv.type === 'overview', false, '全体', threadsAt('r').length ? `メッセージ ${threadsAt('r').length}` : undefined, () => go({ type: 'overview' })));
	for (const s of sum.sections) {
		nav.append(navItem(sv.type === 'section' && sv.key === s.key, s.marked, s.name, s.hunks.length + s.ranges.length, () => go({ type: 'section', key: s.key }), s.depth));
	}
	const un = sum.unassigned.hunks.length + sum.unassigned.ranges.length + sum.unassigned.files.length;
	if (un > 0 || sum.sections.length === 0) {
		nav.append(navItem(sv.type === 'unassigned', false, '未分類', un, () => go({ type: 'unassigned' })));
	}
}

// scopedPaths は「選択中のセクションのみ」で出すファイル。全体ならすべて。
function scopedPaths() {
	const sum = S.summary;
	const sv = S.scopeView;
	if (S.scope === 'all' || sv.type === 'overview') return null;
	if (sv.type === 'section') {
		const s = S.idx.section.get(sv.key);
		return new Set(s ? s.files : []);
	}
	const paths = new Set(sum.unassigned.files);
	for (const id of sum.unassigned.hunks) paths.add(S.idx.hunk.get(id).file.path);
	for (const r of sum.unassigned.ranges) paths.add(S.idx.hunk.get(r.hunk).file.path);
	return paths;
}

function renderListNav() {
	const sum = S.summary;
	const nav = $('nav');
	nav.replaceChildren();
	for (const b of document.querySelectorAll('.tabs button')) b.classList.toggle('active', b.dataset.tab === S.tab);
	$('file-tools').hidden = S.tab !== 'files';
	if (S.tab === 'files') {
		const q = S.filter.toLowerCase();
		const scoped = scopedPaths();
		let n = 0;
		for (const f of sum.files) {
			if (scoped && !scoped.has(f.path)) continue;
			if (q && !f.path.toLowerCase().includes(q)) continue;
			n++;
			nav.append(navItem(S.view.type === 'file' && S.view.path === f.path, f.marked, f.path, f.binary ? 'bin' : `+${f.adds} -${f.dels}`, () => go({ type: 'file', path: f.path })));
		}
		if (n === 0) nav.append(h('div', { class: 'placeholder', text: '該当するファイルはありません' }));
	} else {
		const shown = sum.threads.filter(visible);
		if (shown.length === 0) nav.append(h('div', { class: 'placeholder', text: sum.threads.length ? '隠しているものだけです' : 'メッセージはまだありません' }));
		for (const t of shown) {
			nav.append(navItem(false, false, `[${KIND[t.kind]}・${STATE[t.state]}] ${t.where}`, t.found ? undefined : '付け先なし', () => focusThread(t)));
		}
	}
}

function go(view) {
	S.view = view;
	if (view.type !== 'file') S.scopeView = view;
	S.sel = null;
	updateSelbar();
	render();
	window.scrollTo(0, 0);
}

function focusThread(t) {
	const a = t.anchor;
	let view = { type: 'overview' };
	if (t.found) {
		if (a.type === 'section') view = { type: 'section', key: a.section };
		else if (a.type !== 'review') view = { type: 'file', path: a.path };
	}
	go(view);
	setTimeout(() => {
		const el = document.querySelector(`[data-thread="${t.id}"]`);
		if (el) el.scrollIntoView({ block: 'center' });
	}, 300);
}

let observer = null;

function renderMain() {
	const main = $('main');
	main.replaceChildren();
	if (observer) observer.disconnect();
	observer = new IntersectionObserver((entries) => {
		for (const e of entries) {
			if (!e.isIntersecting) continue;
			observer.unobserve(e.target);
			e.target._load();
		}
	}, { rootMargin: '800px' });

	const v = S.view;
	if (v.type === 'overview') return renderOverview(main);
	if (v.type === 'section') {
		const s = S.idx.section.get(v.key);
		if (!s) return main.append(h('div', { class: 'placeholder', text: 'セクションが見つかりません' }));
		main.append(sectionCard(s));
		return appendFiles(main, s.files, viewMap(s.hunks, s.ranges));
	}
	if (v.type === 'unassigned') {
		const un = S.summary.unassigned;
		main.append(h('div', { class: 'card section-card' }, h('div', { class: 'card-body' },
			h('h2', { text: '未分類' }), h('div', { class: 'muted', text: 'どのセクションにも入っていない hunk と、その残りの行、バイナリのファイル' }))));
		const only = viewMap(un.hunks, un.ranges);
		const paths = new Set(un.files);
		for (const id of only.keys()) paths.add(S.idx.hunk.get(id).file.path);
		return appendFiles(main, S.summary.files.filter((f) => paths.has(f.path)).map((f) => f.path), only);
	}
	if (v.type === 'file') main.append(fileBlock(v.path, null));
}

// viewMap は、出す hunk と、その中で出す範囲（丸ごとなら null）の対応を作る。
function viewMap(wholeIds, ranges) {
	const m = new Map();
	for (const id of wholeIds) m.set(id, null);
	for (const r of ranges || []) {
		if (m.has(r.hunk) && m.get(r.hunk) === null) continue;
		if (!m.has(r.hunk)) m.set(r.hunk, []);
		m.get(r.hunk).push([r.start, r.end]);
	}
	return m;
}

// rangeLabel は hunk の中の範囲を、ファイルの行番号で表す。
function rangeLabel(hk, a, b) {
	const nums = hk.lines.slice(a, b + 1).map((l) => l.n).filter(Boolean);
	if (nums.length) return `変更後 ${nums[0]}〜${nums[nums.length - 1]} 行`;
	const old = hk.lines.slice(a, b + 1).map((l) => l.o).filter(Boolean);
	return old.length ? `変更前 ${old[0]}〜${old[old.length - 1]} 行` : `${a}〜${b}`;
}

const PAGE = 200;

// appendFiles はファイルを PAGE 件ずつ出す。数万ファイルの差分で一度に描かないため。
function appendFiles(main, paths, only) {
	const shown = Math.max(PAGE, S.view.shown || 0);
	for (const path of paths.slice(0, shown)) main.append(fileBlock(path, only));
	if (paths.length > shown) {
		main.append(h('div', { class: 'placeholder' },
			`${shown} / ${paths.length} ファイルを表示中 `,
			h('button', { text: `次の ${Math.min(PAGE, paths.length - shown)} ファイル`, onclick: () => { S.view.shown = shown + PAGE; render(); } })));
	}
}

function renderOverview(main) {
	const sum = S.summary;
	const r = sum.review;
	const hunks = sum.files.reduce((n, f) => n + f.hunks.length, 0);
	main.append(h('div', { class: 'card section-card' },
		h('div', { class: 'card-head' }, h('strong', { text: '全体' }), h('span', { class: 'spacer' }),
			h('button', { text: '全体にメッセージ', onclick: () => compose({ type: 'review' }, '全体') }),
			h('button', {
				text: 'セクションを頼む',
				title: 'エージェントにセクションを作る（作り直す）よう頼むメッセージを書く',
				onclick: () => compose({ type: 'review' }, '全体', 'instruction',
					sum.sections.length ? 'セクションを作り直してください。' : 'この差分を、意味・機能・依存関係でセクションに分けてください。'),
			})),
		h('div', { class: 'card-body' },
			h('div', {}, `${r.spec.repo}`),
			h('div', { class: 'muted' }, `レビュー ID ${r.id} ・ ${sum.files.length} ファイル ・ ${hunks} hunk ・ セクション ${sum.sections.length}`),
			sum.threads.some((t) => t.state === 'closed') ? h('div', { class: 'thread-actions' }, h('button', {
				class: 'small', text: '閉じたスレッドを消す',
				onclick: () => guarded(async () => {
					const n = sum.threads.filter((t) => t.state === 'closed').length;
					if (!confirm(`閉じたスレッド ${n} 件を消しますか？ 元に戻せません。`)) return;
					const res = await api('POST', '/api/threads/delete', { states: ['closed'] });
					toast(`${res.deleted} 件を消しました`);
					await load();
				}),
			})) : null,
			threadList(threadsAt('r')))));
	if (S.idx.orphans.length) {
		main.append(h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('strong', { text: '付け先なしのメッセージ' })),
			h('div', { class: 'card-body' }, threadList(S.idx.orphans, true))));
	}
	if (sum.sections.length === 0) {
		main.append(h('div', { class: 'placeholder', text: 'セクションはまだありません。「セクションを頼む」でエージェントに頼むか、ファイルの一覧から読めます。' }));
	}
}

function sectionCard(s) {
	return h('div', { class: 'card section-card' },
		h('div', { class: 'card-head' },
			h('h2', { text: s.name }), h('span', { class: 'spacer' }),
			markBox('section', s.key, s.marked),
			h('button', { text: 'メッセージ', onclick: () => compose({ type: 'section', section: s.key }, 'セクション ' + s.key) })),
		h('div', { class: 'card-body' },
			s.intent ? h('div', { class: 'intent', text: s.intent }) : null,
			s.checks && s.checks.length ? h('div', {}, h('div', { class: 'muted', text: '確かめる点' }), h('ul', {}, s.checks.map((c) => h('li', { text: c })))) : null,
			h('div', { class: 'muted', text: `${s.files.length} ファイル ・ ${s.hunks.length} hunk` + (s.ranges.length ? ` ・ hunk の一部 ${s.ranges.length} か所` : '') }),
			threadList(threadsAt('s:' + s.key))));
}

function markBox(kind, key, marked) {
	const box = h('input', {
		type: 'checkbox', checked: marked,
		onchange: (e) => guarded(async () => {
			await api('POST', '/api/mark', { kind, key, on: e.target.checked });
			await load();
		}),
	});
	return h('label', { class: 'mark' }, box, '確認済み');
}

function fileBlock(path, only) {
	const f = S.idx.file.get(path);
	const head = h('div', { class: 'card-head' },
		h('span', { class: 'badge ' + f.status, text: STATUS[f.status] || f.status }),
		f.binary ? h('span', { class: 'badge', text: 'バイナリ' }) : null,
		h('span', { class: 'path', text: f.oldPath ? `${f.oldPath} → ${f.path}` : f.path }),
		f.binary ? null : h('span', { class: 'muted', text: `+${f.adds} -${f.dels}` }),
		h('span', { class: 'spacer' }),
		only && !f.binary ? h('button', { class: 'link', text: 'ファイル全体', onclick: () => go({ type: 'file', path }) }) : null,
		markBox('file', path, f.marked),
		h('button', { text: 'メッセージ', onclick: () => compose({ type: 'file', path }, 'ファイル ' + path) }));
	const body = h('div', {});
	const card = h('div', { class: 'card', id: 'file-' + path }, head, h('div', { class: 'card-body', style: 'padding:0 12px' }, threadList(threadsAt('f:' + path))), body);
	if (f.image) body.append(imagePair(f));
	if (f.audio) body.append(audioPair(f));
	if (f.binary) {
		if (!f.image && !f.audio) body.append(h('div', { class: 'placeholder', text: 'バイナリのファイル（中身は表示しません）' }));
		return card;
	}
	const hunks = f.hunks.filter((hk) => !only || only.has(hk.id));
	const rangesOf = (id) => (only ? only.get(id) : null);
	const placeholder = h('div', { class: 'placeholder', text: `${hunks.length} hunk を読み込み中…`, style: `min-height:${Math.min(2000, 40 + hunks.reduce((n, hk) => n + hk.oldCount + hk.adds, 0) * 18)}px` });
	body.append(placeholder);
	placeholder._load = () => guarded(async () => {
		const detail = await loadFile(path);
		const byId = new Map(detail.hunks.map((hk) => [hk.id, hk]));
		placeholder.replaceWith(...hunks.map((hk) => hunkBlock(f, byId.get(hk.id), hk, rangesOf(hk.id))));
	});
	if (S.files.has(path)) placeholder._load();
	else observer.observe(placeholder);
	return card;
}

function imagePair(f) {
	const fig = (label, id) => id
		? h('figure', {}, h('img', { src: `/api/blob?path=${encodeURIComponent(f.path)}&id=${id}`, alt: label, loading: 'lazy' }), h('figcaption', { text: label }))
		: h('figure', {}, h('figcaption', { text: label + '：なし' }));
	return h('div', { class: 'images' }, fig('変更前', f.oldBlob), fig('変更後', f.newBlob));
}

// audioPair は変更前と変更後の音声を並べる。押すまで読み込まない。
function audioPair(f) {
	const player = (label, id) => {
		if (!id) return h('figure', {}, h('figcaption', { text: label + '：なし' }));
		const audio = h('audio', { controls: true, preload: 'none', src: `/api/blob?path=${encodeURIComponent(f.path)}&id=${id}` });
		audio.addEventListener('error', () => audio.replaceWith(h('span', { class: 'muted', text: 'このブラウザでは再生できない形式です' })));
		return h('figure', {}, audio, h('figcaption', { text: label }));
	};
	return h('div', { class: 'images audios' }, player('変更前', f.oldBlob), player('変更後', f.newBlob));
}

// ---- 色分け（同梱の Prism） ----

const LANG_BY_EXT = {
	cs: 'csharp', go: 'go', js: 'javascript', mjs: 'javascript', ts: 'javascript',
	css: 'css', uss: 'css', html: 'markup', htm: 'markup', xml: 'markup', uxml: 'markup', svg: 'markup',
	json: 'json', asmdef: 'json', asmref: 'json',
	yaml: 'yaml', yml: 'yaml', asset: 'yaml', prefab: 'yaml', unity: 'yaml', meta: 'yaml', mat: 'yaml', controller: 'yaml', anim: 'yaml',
	md: 'markdown', sh: 'bash', hlsl: 'hlsl', cginc: 'hlsl', shader: 'hlsl', compute: 'hlsl',
};
const HIGHLIGHT_MAX_LINES = 20000;

function langOf(path) {
	const lang = LANG_BY_EXT[path.split('.').pop().toLowerCase()];
	return lang && window.Prism && Prism.languages[lang] ? lang : null;
}

// splitHtmlLines は Prism の出力を行に分ける。行をまたぐ span は行ごとに閉じて開き直す。
function splitHtmlLines(html) {
	const lines = [];
	const open = [];
	let line = '';
	for (const m of html.matchAll(/<span[^>]*>|<\/span>|\n|[^<\n]+/g)) {
		const t = m[0];
		if (t === '\n') {
			lines.push(line + '</span>'.repeat(open.length));
			line = open.join('');
			continue;
		}
		if (t === '</span>') open.pop();
		else if (t.startsWith('<span')) open.push(t);
		line += t;
	}
	lines.push(line + '</span>'.repeat(open.length));
	return lines;
}

// highlightHunk は変更前と変更後をそれぞれまとめて色分けし、hunk の行ごとの HTML を返す。
function highlightHunk(path, hk) {
	const lang = langOf(path);
	if (!lang || hk.lines.length > HIGHLIGHT_MAX_LINES) return null;
	const side = (kinds) => hk.lines.filter((l) => kinds.includes(l.k)).map((l) => l.t.replace(/\r$/, '')).join('\n');
	const color = (text) => splitHtmlLines(Prism.highlight(text, Prism.languages[lang], lang));
	const oldLines = color(side(' -'));
	const newLines = color(side(' +'));
	let o = 0;
	let n = 0;
	return hk.lines.map((l) => {
		if (l.k === '-') return oldLines[o++];
		if (l.k === '+') return newLines[n++];
		if (l.k === ' ') {
			o++;
			return newLines[n++];
		}
		return '';
	});
}

// hunkBlock は hunk を表にする。ranges があれば、その範囲の行だけを出す。
function hunkBlock(f, hk, sumHunk, ranges) {
	const others = (sumHunk.sections || []).filter((k) => !(S.view.type === 'section' && S.view.key === k));
	const inRange = (i) => !ranges || ranges.some(([a, b]) => i >= a && i <= b);
	const head = h('div', { class: 'hunk-head' },
		h('span', { text: `h${hk.num}` }),
		h('span', { text: `@@ -${hk.oldStart},${hk.oldCount} +${hk.newStart},${hk.newCount} @@ ${hk.context || ''}` }),
		ranges ? h('span', { class: 'other', text: '一部: ' + ranges.map(([a, b]) => rangeLabel(hk, a, b)).join('、') }) : null,
		others.length ? h('span', { class: 'other muted', text: '他のセクション: ' + others.join('、') }) : null,
		h('span', { class: 'spacer', style: 'flex:1' }),
		h('button', { class: 'small', text: 'hunk にメッセージ', onclick: () => compose({ type: 'hunk', hunk: hk.id }, `${f.path} h${hk.num}`) }));
	const table = h('table', { class: 'code' });
	const colored = highlightHunk(f.path, hk);
	let skipped = 0;
	const flushSkipped = () => {
		if (skipped > 0) table.append(h('tr', { class: 'note' }, h('td', { class: 'num' }), h('td', { class: 'num' }), h('td', { class: 'text', text: `⋯ ここに出さない ${skipped} 行（範囲の外）` })));
		skipped = 0;
	};
	hk.lines.forEach((l, i) => {
		if (!inRange(i)) {
			skipped++;
			return;
		}
		flushSkipped();
		const cls = l.k === '+' ? 'add' : l.k === '-' ? 'del' : l.k === '\\' ? 'note' : '';
		const num = (side, n) => h('td', {
			class: 'num', text: n || '', 'data-side': side,
			onmousedown: (e) => { if (e.shiftKey) e.preventDefault(); },
			onclick: (e) => selectLine(hk, i, side, e.shiftKey),
		});
		let td;
		if (l.k === '\\') {
			td = h('td', { class: 'text', text: l.t.trim() });
		} else if (colored) {
			const code = document.createElement('span');
			code.innerHTML = colored[i];
			td = h('td', { class: 'text' }, l.k, code);
		} else {
			td = h('td', { class: 'text', text: l.k + l.t.replace(/\r$/, '') });
		}
		table.append(h('tr', { class: cls, 'data-hunk': hk.id, 'data-idx': i }, num('old', l.o), num('new', l.n), td));
		const ts = threadsAt('l:' + hk.id + ':' + i).filter(visible);
		if (ts.length) table.append(h('tr', { class: 'threads-row' }, h('td', { colspan: 3 }, threadList(ts))));
	});
	flushSkipped();
	return h('div', { class: 'hunk', id: 'hunk-' + hk.id }, head, h('div', { style: 'padding:0 12px' }, threadList(threadsAt('h:' + hk.id))), table);
}

// ---- 行の選択 ----

function selectLine(hk, i, side, extend) {
	const l = hk.lines[i];
	if (l.k === '\\') return;
	if (side === 'old' && !l.o) side = 'new';
	if (side === 'new' && !l.n) side = 'old';
	if (extend && S.sel && S.sel.hunk === hk.id) {
		S.sel.start = Math.min(S.sel.anchor, i);
		S.sel.end = Math.max(S.sel.anchor, i);
	} else {
		S.sel = { hunk: hk.id, side, start: i, end: i, anchor: i, path: S.idx.hunk.get(hk.id).file.path, num: hk.num };
	}
	for (const tr of document.querySelectorAll('tr.sel')) tr.classList.remove('sel');
	for (const tr of document.querySelectorAll(`tr[data-hunk="${hk.id}"]`)) {
		const n = Number(tr.dataset.idx);
		if (n >= S.sel.start && n <= S.sel.end) tr.classList.add('sel');
	}
	updateSelbar();
}

function updateSelbar() {
	let bar = document.querySelector('.selbar');
	if (!S.sel) {
		if (bar) bar.remove();
		return;
	}
	if (!bar) {
		bar = h('div', { class: 'selbar' });
		document.body.append(bar);
	}
	const sel = S.sel;
	const n = sel.end - sel.start + 1;
	const name = sel.path.split('/').pop();
	bar.replaceChildren(
		h('span', { title: sel.path, text: `${name} h${sel.num}・${n} 行（${sel.side === 'old' ? '変更前' : '変更後'}）・Shift+クリックで範囲` }),
		h('button', {
			class: 'primary', text: 'メッセージ',
			onclick: () => compose(lineAnchor(sel), `${sel.path} ${lineRange(sel)}`),
		}),
		h('button', { text: '解除', onclick: () => { S.sel = null; for (const tr of document.querySelectorAll('tr.sel')) tr.classList.remove('sel'); updateSelbar(); } }));
}

// lineAnchor は選んだ行を、ファイルの行番号の付け先にする。コミットで hunk が変わっても外れにくい。
function lineAnchor(sel) {
	const detail = S.files.get(sel.path);
	const hk = detail && detail.hunks.find((x) => x.id === sel.hunk);
	const nums = hk ? hk.lines.slice(sel.start, sel.end + 1).map((l) => (sel.side === 'old' ? l.o : l.n)).filter(Boolean) : [];
	if (!nums.length) return { type: 'line', hunk: sel.hunk, side: sel.side, start: sel.start, end: sel.end };
	return { type: 'lines', path: sel.path, side: sel.side, from: nums[0], to: nums[nums.length - 1] };
}

function lineRange(sel) {
	const detail = S.files.get(sel.path);
	const hk = detail && detail.hunks.find((x) => x.id === sel.hunk);
	const nums = hk ? hk.lines.slice(sel.start, sel.end + 1).map((l) => sel.side === 'old' ? l.o : l.n).filter(Boolean) : [];
	const side = sel.side === 'old' ? '変更前' : '変更後';
	if (!nums.length) return `h${sel.num} ${side}`;
	return `${side} ${nums[0]}〜${nums[nums.length - 1]} 行`;
}

// ---- メッセージ ----

let composing = null;

const KIND_NOTE = {
	instruction: '指示: エージェントは求められた変更をする。しない場合は理由を返す',
	question: '質問: エージェントは答えるだけで、コードは変えない',
	reconsider: '再考: エージェントは考え直した結果と代わりの案を返す。変えるかは返事を見て決める',
	other: 'その他: エージェントは本文から判断する',
};

function composerKind() {
	return document.querySelector('input[name="kind"]:checked').value;
}

function showKindNote() {
	$('composer-kind-note').textContent = KIND_NOTE[composerKind()];
}

for (const r of document.querySelectorAll('input[name="kind"]')) r.addEventListener('change', showKindNote);

function compose(anchor, where, kind = 'question', body = '') {
	composing = anchor;
	$('composer-where').textContent = '付け先: ' + where;
	document.querySelector(`input[name="kind"][value="${kind}"]`).checked = true;
	showKindNote();
	$('composer-body').value = body;
	$('composer').showModal();
	$('composer-body').focus();
}

$('composer').addEventListener('close', () => {
	if ($('composer').returnValue !== 'save' || !composing) return;
	const anchor = composing;
	composing = null;
	const body = $('composer-body').value;
	guarded(async () => {
		await api('POST', '/api/threads', { kind: composerKind(), anchor, body });
		if (anchor.type === 'line') {
			S.sel = null;
			updateSelbar();
		}
		await load();
	});
});

function threadList(threads, showWhere = false) {
	const shown = threads.filter(visible);
	if (!shown.length) return null;
	return h('div', {}, shown.map((t) => threadCard(t, showWhere)));
}

// visible は、隠す設定のときに閉じた・送付済みのスレッドを除く。
function visible(t) {
	return !S.hideDone || (t.state !== 'closed' && t.state !== 'sent');
}

function loadHideDone() {
	try {
		return localStorage.getItem('sedi-review.hideDone') === '1';
	} catch {
		return false;
	}
}

function threadCard(t, showWhere) {
	const attached = S.summary.presence.attached;
	const card = h('div', { class: `thread ${t.state}${t.found ? '' : ' orphan'}`, 'data-thread': t.id });
	const act = (label, fn, cls) => h('button', { class: 'small ' + (cls || ''), text: label, onclick: () => guarded(fn) });
	card.append(h('div', { class: 'thread-head' },
		h('span', { class: 'badge', text: KIND[t.kind] || t.kind }),
		h('span', { class: 'state-' + t.state, text: STATE[t.state] + (t.pending ? '（待ち受けに渡す予定）' : '') }),
		showWhere || !t.found ? h('span', { text: t.where + (t.found ? '' : '（付け先なし）') }) : null));
	for (const m of t.messages) {
		const msg = h('div', { class: 'msg ' + m.author },
			h('div', { class: 'who', text: (m.author === 'agent' ? 'エージェント' : '作業者') + '  ' + new Date(m.at).toLocaleString() + (m.author === 'worker' && !m.sent ? '  未送付' : '') }),
			h('div', { class: 'body', text: m.body }));
		if (m.author === 'worker' && !m.sent) {
			msg.append(h('div', { class: 'thread-actions' },
				h('button', { class: 'small', text: '編集', onclick: () => editMessage(msg, t, m) }),
				act('取り消し', async () => {
					if (!confirm('このメッセージを取り消しますか？')) return;
					await api('DELETE', `/api/threads/${t.id}/messages/${m.id}`);
					await load();
				})));
		}
		card.append(msg);
	}
	const actions = h('div', { class: 'thread-actions' },
		h('button', { class: 'small', text: '返信', onclick: () => replyBox(card, t) }),
		act('コピー', async () => {
			const res = await api('POST', `/api/threads/${t.id}/copy`);
			if (await copyText(res.text)) toast('貼るための文をコピーしました');
			await load();
		}),
		attached && t.state !== 'closed' ? act('送る', async () => {
			await api('POST', `/api/threads/${t.id}/send`);
			toast('待ち受けに渡しました');
			await load();
		}) : null,
		t.state !== 'closed' ? act('閉じる', async () => {
			await api('POST', `/api/threads/${t.id}/close`);
			await load();
		}) : null,
		act('消す', async () => {
			if (!confirm('このスレッドを消しますか？ 元に戻せません。')) return;
			await api('DELETE', `/api/threads/${t.id}`);
			await load();
		}),
		t.state !== 'closed' ? h('label', { class: 'mark small-label', title: 'エージェントが対応を終えたら、このスレッドを閉じてよいことにする' },
			h('input', {
				type: 'checkbox', checked: !!t.agentMayClose,
				onchange: (e) => guarded(async () => {
					await api('POST', `/api/threads/${t.id}/agent-may-close`, { on: e.target.checked });
					await load();
				}),
			}), 'エージェントが閉じてよい') : null);
	card.append(actions);
	return card;
}

function replyBox(card, t) {
	if (card.querySelector('textarea')) return;
	const ta = h('textarea', { rows: 4, placeholder: '返信' });
	const box = h('div', {}, ta, h('div', { class: 'thread-actions' },
		h('button', { class: 'small primary', text: '保存（未送付）', onclick: () => guarded(async () => {
			await api('POST', `/api/threads/${t.id}/messages`, { body: ta.value });
			await load();
		}) }),
		h('button', { class: 'small', text: 'やめる', onclick: () => box.remove() })));
	card.append(box);
	ta.focus();
}

function editMessage(msgEl, t, m) {
	const ta = h('textarea', { rows: 4 });
	ta.value = m.body;
	msgEl.querySelector('.body').replaceWith(ta);
	const done = h('div', { class: 'thread-actions' },
		h('button', { class: 'small primary', text: '保存', onclick: () => guarded(async () => {
			await api('PATCH', `/api/threads/${t.id}/messages/${m.id}`, { body: ta.value });
			await load();
		}) }),
		h('button', { class: 'small', text: 'やめる', onclick: () => render() }));
	msgEl.querySelector('.thread-actions').replaceWith(done);
	ta.focus();
}

// ---- 上の操作 ----

$('btn-refresh').addEventListener('click', () => guarded(async () => {
	await api('POST', '/api/refresh');
	await load();
	toast('差分を取り直しました');
}));

$('btn-export').addEventListener('click', () => guarded(async () => {
	if (!S.summary.threads.length) return toast('メッセージがありません');
	const res = await api('POST', '/api/export');
	if (await copyText(res.text)) toast('書き出して、貼るための文をコピーしました\n' + res.path, 6000);
	await load();
}));

$('toggle-hide-done').checked = S.hideDone;
$('toggle-hide-done').addEventListener('change', (e) => {
	S.hideDone = e.target.checked;
	try {
		localStorage.setItem('sedi-review.hideDone', S.hideDone ? '1' : '0');
	} catch {}
	buildIndex();
	render();
});

$('btn-send-all').addEventListener('click', () => guarded(async () => {
	const res = await api('POST', '/api/send-all');
	toast(`${res.queued} 件を待ち受けに渡しました`);
	await load();
}));

for (const b of document.querySelectorAll('.tabs button')) {
	b.addEventListener('click', () => { S.tab = b.dataset.tab; renderListNav(); });
}

$('filter').addEventListener('input', (e) => { S.filter = e.target.value; renderListNav(); });
$('file-scope').addEventListener('change', (e) => { S.scope = e.target.value; renderListNav(); });

document.addEventListener('keydown', (e) => {
	if (e.key === 'Escape' && S.sel) {
		S.sel = null;
		for (const tr of document.querySelectorAll('tr.sel')) tr.classList.remove('sel');
		updateSelbar();
	}
});

// ---- 起動 ----

let reloadTimer = null;
const events = new EventSource('/api/events');
events.onmessage = () => {
	clearTimeout(reloadTimer);
	reloadTimer = setTimeout(() => guarded(load), 150);
};

guarded(load);

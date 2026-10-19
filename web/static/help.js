'use strict';

// 許諾の文は同梱のファイルから読み、ページに写し間違いが入らないようにする。
for (const pre of document.querySelectorAll('pre.license')) {
	fetch(pre.dataset.src)
		.then((res) => (res.ok ? res.text() : Promise.reject(new Error(res.statusText))))
		.then((text) => { pre.textContent = text; })
		.catch((e) => { pre.textContent = `読み込めませんでした（${pre.dataset.src}）: ${e.message}`; });
}

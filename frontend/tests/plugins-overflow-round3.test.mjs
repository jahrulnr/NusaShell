import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

const root = new URL('../', import.meta.url);

// Extracts the body of a top-level `@media <query>` block by brace counting so
// assertions stay scoped to one breakpoint — a plain source regex would leak
// into the later (max-width: 430px) and prefers-reduced-motion queries.
function mediaBlock(css, query) {
  const at = css.indexOf(`@media ${query}`);
  assert.notEqual(at, -1, `plugins.css must keep the @media ${query} seam`);
  const open = css.indexOf('{', at);
  let depth = 0;
  for (let i = open; i < css.length; i += 1) {
    if (css[i] === '{') depth += 1;
    else if (css[i] === '}') {
      depth -= 1;
      if (depth === 0) return css.slice(open + 1, i);
    }
  }
  assert.fail(`@media ${query} block never closes`);
}

test('Plugins <=720px workspace releases its desktop flex share so the page is the natural scroller', async () => {
  const pluginsCss = await readFile(new URL('plugins.css', root), 'utf8');
  const mobile = mediaBlock(pluginsCss, '(max-width: 720px)');

  // The <=720px one-pane seam keeps .plugins-workspace on the desktop
  // `flex: 1 1 auto` + min-height: 0 share. Inside the bounded .plugins-page
  // column that clamps the workspace to the leftover height (~387px at
  // 375x602) while its overflow:visible content (a ~1181px list at
  // scrollHeight 1226) keeps painting below the box instead of stretching it.
  // flex:none -> 0 0 auto sizes the workspace to its content, so
  // .plugins-page (overflow-y:auto from the <=760px seam) becomes the single
  // natural scroller. Same seam .skills-workspace, .providers-workspace, and
  // .logs-workspace already release through.
  assert.match(mobile, /\.plugins-workspace\s*\{[^}]*flex:\s*none;/);

  // Both panes must offer natural height to that workspace: the index drops
  // its fixed tablet share, the list drops its internal scroll cage, and the
  // inspector stays unclipped — no nested scroll cage comes back.
  assert.match(mobile, /\.plugins-index\s*\{[^}]*flex:\s*none;[^}]*min-height:\s*0;/);
  assert.match(mobile, /\.plugins-list\s*\{[^}]*flex:\s*none;[^}]*overflow:\s*visible;/);
  assert.match(mobile, /\.plugins-inspector\s*\{[^}]*min-height:\s*0;[^}]*overflow:\s*visible;/);

  // One pane at a time stays armed on the same seam.
  assert.match(mobile, /\.plugins-page\[data-mobile-pane="list"\][^{]*\.plugins-inspector/);
  assert.match(mobile, /\.plugins-page\[data-mobile-pane="detail"\][^{]*\.plugins-index/);
});

test('Plugins overflow fix leaves the desktop split and the <=760px stacked seam intact', async () => {
  const pluginsCss = await readFile(new URL('plugins.css', root), 'utf8');

  // Desktop contract is untouched: the workspace keeps its bounded flex share
  // and hard clip, and the list keeps its internal scroller.
  assert.match(
    pluginsCss,
    /\.plugins-workspace\s*\{[^}]*grid-template-columns:\s*minmax\(300px, 360px\) minmax\(0, 1fr\);[^}]*flex:\s*1 1 auto;[^}]*overflow:\s*hidden;/,
  );
  assert.match(pluginsCss, /\.plugins-list\s*\{[^}]*flex:\s*1 1 auto;[^}]*overflow:\s*auto;/);

  // The <=760px seam the fix builds on: the page starts scrolling and the
  // workspace stacks its panes with visible overflow.
  const stacked = mediaBlock(pluginsCss, '(max-width: 760px)');
  assert.match(stacked, /\.plugins-page\s*\{[^}]*overflow-y:\s*auto;/);
  assert.match(stacked, /\.plugins-workspace\s*\{[^}]*display:\s*flex;[^}]*flex-direction:\s*column;[^}]*overflow:\s*visible;/);
});

test('Plugins markup keeps both panes inside the workspace that scrolls naturally', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const { document } = new JSDOM(html).window;
  const page = document.querySelector('#plugins-view');

  assert.ok(page?.classList.contains('plugins-page'), '#plugins-view is the bounded page the controller scrolls');
  const workspace = page.querySelector('.plugins-workspace');
  assert.ok(workspace, 'a single .plugins-workspace wraps both panes');
  assert.equal(workspace.querySelector('.plugins-index > .plugins-list'), page.querySelector('#plugins-list'));
  assert.ok(workspace.querySelector('.plugins-inspector'), 'the detail pane shares the workspace');
});

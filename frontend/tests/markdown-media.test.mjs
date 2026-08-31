import assert from 'node:assert/strict';
import { test } from 'node:test';

import { renderMarkdown } from '../js/markdown.js';

test('inline image syntax renders <img> tag', () => {
  const html = renderMarkdown('![cat](https://example.com/cat.png)');
  assert.match(html, /<img[^>]+src="https:\/\/example\.com\/cat\.png"/);
  assert.match(html, /alt="cat"/);
});

test('inline video syntax renders <video> tag by extension', () => {
  const html = renderMarkdown('![demo](https://example.com/demo.mp4)');
  assert.match(html, /<video[^>]+src="https:\/\/example\.com\/demo\.mp4"/);
  assert.doesNotMatch(html, /<img/);
});

test('file:// URL is proxied to /local-file?path=', () => {
  const html = renderMarkdown('![local](file:///tmp/screenshot.png)');
  assert.match(html, /src="\/local-file\?path=%2Ftmp%2Fscreenshot\.png"/);
  assert.doesNotMatch(html, /file:\/\//);
});

test('file:// video is proxied and rendered as <video>', () => {
  const html = renderMarkdown('![clip](file:///home/user/video.mp4)');
  assert.match(html, /<video[^>]+src="\/local-file\?path=/);
  assert.doesNotMatch(html, /<img/);
});

test('data: URL passes through unchanged for images', () => {
  const html = renderMarkdown('![pic](data:image/png;base64,iVBORw0KGgo=)');
  assert.match(html, /src="data:image\/png;base64,iVBORw0KGgo="/);
});

test('http:// URL passes through unchanged', () => {
  const html = renderMarkdown('![pic](http://localhost:8080/img.jpg)');
  assert.match(html, /src="http:\/\/localhost:8080\/img\.jpg"/);
});

test('video extensions: webm, ogg, mov, mkv all render as <video>', () => {
  for (const ext of ['webm', 'ogg', 'mov', 'mkv', 'avi', 'm4v']) {
    const html = renderMarkdown(`![v](https://example.com/clip.${ext})`);
    assert.match(html, /<video/, `extension .${ext} should render as <video>`);
  }
});

test('non-video extensions still render as <img>', () => {
  for (const ext of ['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg']) {
    const html = renderMarkdown(`![i](https://example.com/pic.${ext})`);
    assert.match(html, /<img/, `extension .${ext} should render as <img>`);
    assert.doesNotMatch(html, /<video/, `extension .${ext} should not render as <video>`);
  }
});

test('image in a paragraph renders inline with text', () => {
  const html = renderMarkdown('Here is a screenshot: ![shot](https://example.com/shot.png) done.');
  assert.match(html, /<img/);
  assert.match(html, /Here is a screenshot:/);
  assert.match(html, /done\./);
});

test('image with empty alt text works', () => {
  const html = renderMarkdown('![](https://example.com/photo.jpg)');
  assert.match(html, /<img[^>]+src="https:\/\/example\.com\/photo\.jpg"/);
  assert.match(html, /alt=""/);
});

test('regular link syntax is not affected by image syntax', () => {
  const html = renderMarkdown('[link](https://example.com)');
  assert.match(html, /<a[^>]+href="https:\/\/example\.com"/);
  assert.doesNotMatch(html, /<img/);
});

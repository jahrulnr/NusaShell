import { test } from 'node:test';
import assert from 'node:assert/strict';
import './env.mjs';
import { localTarget, mediaKind, renderImage, baseName } from '../lib/mdlinks.js';
import { markedUser } from '../lib/usermark.js';

test('local file destinations resolve to the path as written', () => {
  assert.equal(localTarget('/home/me/out/clip.mp4'), '/home/me/out/clip.mp4');
  assert.equal(localTarget('~/Pictures/a.png'), '~/Pictures/a.png');
  assert.equal(localTarget('./out/a.png'), './out/a.png');
  assert.equal(localTarget('out/render.mp4'), 'out/render.mp4');
  assert.equal(localTarget('src/main.go:42'), 'src/main.go:42');
  assert.equal(localTarget('file:///tmp/a%20b.png'), 'file:///tmp/a b.png');
  assert.equal(localTarget('out/my%20clip.mp4'), 'out/my clip.mp4');
  assert.equal(localTarget('C:\\Users\\me\\a.png'), 'C:\\Users\\me\\a.png');
});

test('an editor position is a line suffix, not a URL scheme', () => {
  assert.equal(localTarget('notes.txt:3'), 'notes.txt:3');
  assert.equal(localTarget('README.md:12:4'), 'README.md:12:4');
  assert.equal(mediaKind('notes.txt:3'), '');
  assert.equal(mediaKind('render.mp4:12'), 'video');
});

test('remote addresses and app routes are not local files', () => {
  for (const h of ['https://a.b/x.png', 'http://a.b', 'mailto:a@b.c', 'javascript:alert(1)', 'data:image/png;base64,AA',
    '//cdn.example/x.png', '#top', '?q=1', '/api/files?path=x', '/#/c/abc', '']) {
    assert.equal(localTarget(h), '', h);
  }
});

test('media kind follows the extension', () => {
  assert.equal(mediaKind('a/b.PNG'), 'image');
  assert.equal(mediaKind('render.mp4'), 'video');
  assert.equal(mediaKind('voice.m4a'), 'audio');
  assert.equal(mediaKind('notes.md'), '');
  assert.equal(mediaKind('noext'), '');
  assert.equal(baseName('/a/b/c.mp4'), 'c.mp4');
});

test('a local media image becomes an embed placeholder', () => {
  const html = renderImage({ href: 'out/scene 1.mp4', text: 'Scene 1' });
  assert.match(html, /class="md-media"/);
  assert.match(html, /data-media="out\/scene 1\.mp4"/);
  assert.match(html, /data-kind="video"/);
  assert.match(html, /title="Scene 1"/);
});

test('a remote image is a link and never an embed', () => {
  const html = renderImage({ href: 'https://example.com/a.png', text: 'logo' });
  assert.doesNotMatch(html, /md-media|<img/);
  assert.match(html, /<a href="https:\/\/example\.com\/a\.png">logo<\/a>/);
});

test('a local non-media image is a file link; an unusable one stays a label', () => {
  assert.match(renderImage({ href: '/tmp/report.pdf', text: '' }), /<a href="\/tmp\/report\.pdf">report\.pdf<\/a>/);
  assert.match(renderImage({ href: 'javascript:x', text: 'x"y' }), /\[image: x&quot;y\]/);
});

test('the owner profile renders a typed local image as an embed', () => {
  const html = markedUser.parse('look ![](~/Pictures/cat.jpg)');
  assert.match(html, /data-media="~\/Pictures\/cat\.jpg" data-kind="image"/);
});

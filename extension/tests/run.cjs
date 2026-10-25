// Node test runner for the extension's pure modules: node extension/tests/run.cjs
const assert = require('node:assert/strict');
const path = require('node:path');

const C = require(path.join(__dirname, '..', 'lib', 'classify.js'));
const N = require(path.join(__dirname, '..', 'lib', 'naming.js'));

let passed = 0;
function test(name, fn) {
  try {
    fn();
    passed++;
  } catch (e) {
    console.error(`FAIL ${name}\n  ${e.message}`);
    process.exitCode = 1;
  }
}

test('aryplus master playlist served as 206 octet-stream is HLS', () => {
  const r = C.classify({ url: 'https://vod.aryzap.com/x/adp.10.m3u8', resourceType: 'xmlhttprequest', statusCode: 206, mime: 'application/octet-stream', contentLength: '1200' });
  assert.equal(r.kind, 'hls');
});

test('HLS by MIME without extension', () => {
  const r = C.classify({ url: 'https://cdn/x/playlist?token=1', resourceType: 'xmlhttprequest', statusCode: 200, mime: 'application/vnd.apple.mpegurl' });
  assert.equal(r.kind, 'hls');
});

test('DASH by extension', () => {
  assert.equal(C.classify({ url: 'https://a/b.mpd', resourceType: 'xmlhttprequest', statusCode: 200, mime: 'text/xml' }).kind, 'dash');
});

test('TS segments are grouped, not listed', () => {
  assert.equal(C.classify({ url: 'https://vod.aryzap.com/x/10_5_1.ts', resourceType: 'xmlhttprequest', statusCode: 206, mime: 'video/mp2t', contentLength: '900000' }).kind, 'segment');
  assert.equal(C.classify({ url: 'https://a/seg-00001.m4s', resourceType: 'xmlhttprequest', statusCode: 200, mime: 'video/iso.segment' }).kind, 'segment');
});

test('progressive MP4 via Range is one file with total size', () => {
  const r = C.classify({ url: 'https://a/movie.mp4', resourceType: 'media', statusCode: 206, mime: 'video/mp4', contentRange: 'bytes 0-1023/73400320' });
  assert.equal(r.kind, 'file');
  assert.equal(r.media, 'video');
  assert.equal(r.size, 73400320);
});

test('tiny sound effects are ignored, media-type requests are not', () => {
  assert.equal(C.classify({ url: 'https://a/click.mp3', resourceType: 'xmlhttprequest', statusCode: 200, mime: 'audio/mpeg', contentLength: '4000' }), null);
  assert.equal(C.classify({ url: 'https://a/track', resourceType: 'media', statusCode: 200, mime: 'audio/mpeg', contentLength: '4000' }).kind, 'file');
});

test('attachments and archives are files, html is not', () => {
  const r = C.classify({ url: 'https://a/dl?id=9', resourceType: 'main_frame', statusCode: 200, mime: 'application/octet-stream', contentDisposition: 'attachment; filename="report final.pdf"', contentLength: '2000000' });
  assert.equal(r.kind, 'file');
  assert.equal(r.name, 'report final.pdf');
  // Google's JS CDN serves scripts as attachment; filename="f.txt" — page assets are never downloads.
  assert.equal(C.classify({ url: 'https://securepubads.g.doubleclick.net/tag/js/gpt.js', resourceType: 'script', statusCode: 200, mime: 'text/javascript', contentDisposition: 'attachment; filename="f.txt"', contentLength: '300000' }), null);
  assert.equal(C.classify({ url: 'https://a/x/f.txt', resourceType: 'xmlhttprequest', statusCode: 200, mime: 'text/plain', contentDisposition: 'attachment; filename="f.txt"', contentLength: '900000' }), null);
  assert.equal(C.classify({ url: 'https://a/setup.exe', resourceType: 'main_frame', statusCode: 200, mime: 'application/x-msdownload', contentLength: '9000000' }).kind, 'file');
  assert.equal(C.classify({ url: 'https://a/page', resourceType: 'main_frame', statusCode: 200, mime: 'text/html' }), null);
  assert.equal(C.classify({ url: 'https://a/x.torrent', resourceType: 'main_frame', statusCode: 200, mime: 'application/x-bittorrent' }).kind, 'torrent');
});

test('non-2xx responses are ignored', () => {
  assert.equal(C.classify({ url: 'https://a/b.m3u8', resourceType: 'xmlhttprequest', statusCode: 403, mime: 'application/vnd.apple.mpegurl' }), null);
});

test('content-disposition parsing', () => {
  assert.equal(C.parseDisposition(`attachment; filename*=UTF-8''na%C3%AFve%20file.zip`).filename, 'naïve file.zip');
  assert.equal(C.parseDisposition(`inline; filename=plain.mp4`).filename, 'plain.mp4');
  assert.equal(C.parseDisposition(`attachment`).attachment, true);
});

test('title cleaning strips site boilerplate', () => {
  assert.equal(N.cleanTitle('Dar-e-nijaat - Detail Page', 'https://aryplus.tv/series/x'), 'Dar-e-nijaat');
  assert.equal(N.cleanTitle('(2) Some Clip - YouTube', 'https://www.youtube.com/watch?v=1'), 'Some Clip');
  assert.equal(N.cleanTitle('Big Buck Bunny | Example', 'https://example.com/'), 'Big Buck Bunny');
});

test('pickTitle prefers structured metadata and rejects generic titles', () => {
  assert.equal(N.pickTitle({ og: 'Dar-E-Nijaat', title: 'Dar-e-nijaat - Detail Page' }, 'https://aryplus.tv/series/x'), 'Dar-E-Nijaat');
  assert.equal(N.pickTitle({ ld: 'Episode 12 – The Finale', og: 'Show' }, 'https://x.tv/'), 'Episode 12 - The Finale');
  assert.equal(N.pickTitle({ title: 'Watch Online' }, 'https://x.tv/'), '');
  // A site-wide slogan in og:title must lose to a specific document title.
  assert.equal(N.pickTitle({ og: 'Example – Your Streaming Guide', title: 'Some Show (2021) • Example – Your Streaming Guide' }, 'https://example.surf/tv/1'), 'Some Show (2021)');
  // A heading (h1 or h2) carrying season/episode markers beats the bare series name and the year-only title.
  assert.equal(N.pickTitle({ og: 'Example – Your Streaming Guide', h2: 'Some Show · S1 E1', title: 'Some Show (2021) • Example – Your Streaming Guide' }, 'https://example.surf/tv/1'), 'Some Show - S1 E1');
  assert.equal(N.pickTitle({ og: 'Some Show', h1: 'Some Show · S1 E1', title: 'Some Show (2021) • Example' }, 'https://example.surf/tv/1'), 'Some Show - S1 E1');
});

test('suggestName builds "Title.ext" per kind', () => {
  const meta = { og: 'Dar-E-Nijaat', title: 'Dar-e-nijaat - Detail Page' };
  assert.equal(N.suggestName({ kind: 'hls', url: 'https://vod/adp.10.m3u8' }, meta, 'https://aryplus.tv/series/x'), 'Dar-E-Nijaat.mp4');
  assert.equal(N.suggestName({ kind: 'file', media: 'audio', ext: 'mp3', url: 'https://a/track.mp3' }, meta, 'https://aryplus.tv/'), 'Dar-E-Nijaat.mp3');
  assert.equal(N.suggestName({ kind: 'file', media: 'other', ext: 'zip', url: 'https://a/b/archive%20v2.zip' }, meta, 'https://x/'), 'archive v2.zip');
  assert.equal(N.suggestName({ kind: 'file', filename: 'given.pdf', url: 'https://a/x' }, meta, 'https://x/'), 'given.pdf');
});

test('season/episode detection and naming', () => {
  assert.deepEqual(N.parseEpisode('Some Show - S1 E1'), { show: 'Some Show', season: 1, episode: 1 });
  assert.deepEqual(N.parseEpisode('Some.Show.S02E07.1080p'), { show: 'Some Show', season: 2, episode: 7 });
  assert.deepEqual(N.parseEpisode('Some Show 3x12 Finale'), { show: 'Some Show', season: 3, episode: 12 });
  assert.deepEqual(N.parseEpisode('Some Show (2021) Season 1 Episode 4'), { show: 'Some Show', season: 1, episode: 4 });
  assert.deepEqual(N.parseEpisode('Episode 9 - Some Show'), { show: 'Some Show', season: null, episode: 9 });
  assert.equal(N.parseEpisode('Just a Film (2020)'), null);
  assert.equal(N.episodeName('Some Show', 1, 1), 'Some Show - S01E01');
  assert.equal(N.episodeName('Some Show', null, 12), 'Some Show - E12');
  assert.deepEqual(N.seasonFolder('Some: Show', 2), ['Some Show', 'Season 02']);
});

test('sanitize removes illegal characters and caps length', () => {
  assert.equal(N.sanitize('a<b>:"c/d\\e|f?g*h.mp4'), 'abcdefgh.mp4');
  assert.ok(N.sanitize('x'.repeat(400)).length <= 150);
});

console.log(`${passed} extension tests passed`);

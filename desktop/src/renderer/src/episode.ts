// Season/episode detection shared in spirit with extension/lib/naming.js.
const PATTERNS: Array<{ re: RegExp; s: number | null; e: number }> = [
  { re: /\bS(\d{1,2})\s*[ .\-_]?\s*E(\d{1,3})\b/i, s: 1, e: 2 },
  { re: /\b(\d{1,2})x(\d{1,3})\b/i, s: 1, e: 2 },
  { re: /\bSeason\s*(\d{1,2})\b[^\d]{0,24}?\bEpisode\s*(\d{1,3})\b/i, s: 1, e: 2 },
  { re: /\bEpisode\s*(\d{1,3})\b/i, s: null, e: 1 },
  { re: /\bEp\.?\s*(\d{1,3})\b/i, s: null, e: 1 },
];

export interface Episode {
  show: string;
  season: number | null;
  episode: number;
}

export function parseEpisode(title: string): Episode | null {
  for (const p of PATTERNS) {
    const m = p.re.exec(title);
    if (!m) continue;
    let show = title.slice(0, m.index).replace(/[\s\-–—:·•|(\[.]+$/, '').trim();
    if (!show) show = title.slice(m.index + m[0].length).replace(/^[\s\-–—:·•|)\]]+/, '').trim();
    show = show.replace(/\s*\((?:19|20)\d{2}\)\s*$/, '').trim();
    if (!/\s/.test(show) && show.includes('.')) show = show.replace(/\./g, ' ').trim();
    return { show, season: p.s ? Number(m[p.s]) : null, episode: Number(m[p.e]) };
  }
  return null;
}

const pad2 = (n: number) => String(n).padStart(2, '0');
export const sanitize = (s: string) => s.replace(/[<>:"/\\|?*\u0000-\u001f]/g, '').replace(/\s+/g, ' ').trim() || 'download';

export function episodeName(show: string, season: number | null, episode: number): string {
  return `${sanitize(show || 'Episode')} - ${season != null ? `S${pad2(season)}E${pad2(episode)}` : `E${pad2(episode)}`}`;
}

export function seasonFolder(show: string, season: number | null): string[] {
  const parts = [sanitize(show || 'Show')];
  if (season != null) parts.push(`Season ${pad2(season)}`);
  return parts;
}

/**
 * The browsable index: every artifact, who made it, who last touched it,
 * with a working link to each.
 *
 * A link to this page opens every artifact it lists for as long as it
 * lives, so it is minted short-lived and the links on it expire with it.
 */

import type { Entry } from "./catalog";
import { mintLink } from "./token";

/** The scope an index link is signed for. Too short to be an artifact id,
 * so an index key can never open an artifact and an artifact key can never
 * open the index. */
export const INDEX_SCOPE = "__index__";

/**
 * The page is our own markup and runs no script, so its policy allows none:
 * a title that escaped escaping still could not execute. no-referrer keeps
 * the keys in its links from leaking to anything the page loads.
 */
export const PAGE_HEADERS: Record<string, string> = {
  "Content-Type": "text/html; charset=utf-8",
  "Content-Security-Policy": "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'",
  "Referrer-Policy": "no-referrer",
  "Cache-Control": "private, no-store",
  "X-Content-Type-Options": "nosniff",
};

const ESCAPES: Record<string, string> = { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" };

/** Titles and names are whatever a publisher sent, so everything is escaped. */
export function escapeHTML(value: string): string {
  return value.replace(/[&<>"']/g, (character) => ESCAPES[character]);
}

function when(iso: string): string {
  return iso ? `${iso.slice(0, 10)} ${iso.slice(11, 16)} UTC` : "";
}

function size(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

export async function renderIndex(
  entries: Entry[],
  secret: string,
  indexToken: string,
  expSeconds: number,
  by: string
): Promise<string> {
  const person = (email: string) =>
    email
      ? `<a href="/?k=${encodeURIComponent(indexToken)}&amp;by=${encodeURIComponent(email)}">${escapeHTML(email)}</a>`
      : `<span class="muted">unknown</span>`;

  const rows = await Promise.all(
    entries.map(async (entry) => {
      const link = `/a/${encodeURIComponent(entry.id)}?k=${encodeURIComponent(await mintLink(secret, entry.id, expSeconds))}`;
      const revised =
        entry.revisions > 1
          ? `${person(entry.updated_by)}<br><span class="muted">${escapeHTML(when(entry.updated_at))} · ${entry.revisions} versions</span>`
          : `<span class="muted">—</span>`;
      return `<tr>
  <td><a href="${link}">${escapeHTML(entry.title || entry.id)}</a><br><span class="muted">${escapeHTML(entry.content_type)} · ${size(entry.bytes)}</span></td>
  <td>${person(entry.created_by)}<br><span class="muted">${escapeHTML(when(entry.created_at))}</span></td>
  <td>${revised}</td>
</tr>`;
    })
  );

  const heading = by ? `Published by ${escapeHTML(by)}` : "Gate Inbox artifacts";
  const clear = by ? ` · <a href="/?k=${encodeURIComponent(indexToken)}">show everyone</a>` : "";
  const body = rows.length
    ? `<table><thead><tr><th>Artifact</th><th>Made by</th><th>Last changed</th></tr></thead><tbody>${rows.join("\n")}</tbody></table>`
    : `<p class="muted">Nothing published${by ? " by this person" : ""} yet.</p>`;

  return `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Gate Inbox artifacts</title>
<style>
:root { --fg: #1b1b1f; --muted: #6b6b76; --line: #e4e4ea; --bg: #fff; --link: #2451c7; }
@media (prefers-color-scheme: dark) { :root { --fg: #ececf1; --muted: #9a9aa6; --line: #2c2c34; --bg: #141418; --link: #8fb0ff; } }
body { margin: 0; padding: 24px 16px; background: var(--bg); color: var(--fg); font: 14px/1.5 system-ui, sans-serif; }
main { max-width: 960px; margin: 0 auto; }
h1 { font-size: 20px; margin: 0 0 4px; }
p.meta, .muted { color: var(--muted); }
table { width: 100%; border-collapse: collapse; margin-top: 16px; }
th, td { text-align: left; vertical-align: top; padding: 10px 8px; border-bottom: 1px solid var(--line); }
th { font-weight: 600; font-size: 12px; text-transform: uppercase; letter-spacing: .04em; color: var(--muted); }
a { color: var(--link); text-decoration: none; } a:hover { text-decoration: underline; }
td:first-child { word-break: break-word; }
</style></head>
<body><main>
<h1>${heading}</h1>
<p class="meta">${entries.length} shown${clear} · this page and its links expire ${escapeHTML(when(new Date(expSeconds * 1000).toISOString()))} — they open every artifact listed, so keep them inside the team</p>
${body}
</main></body></html>`;
}

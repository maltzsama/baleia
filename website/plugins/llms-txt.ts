import { mkdir, writeFile } from 'node:fs/promises';
import path from 'node:path';
import type { LoadedContent } from '@docusaurus/plugin-content-docs';
import type { Plugin } from '@docusaurus/types';

// Generates /llms.txt at build time from the docs the site actually renders, so
// the index can never drift from the pages: title, URL and description are the
// ones Docusaurus already computed (frontmatter, with its own fallback). The
// ADRs stay out, as they do in the sitemap.

const docsPluginName = 'docusaurus-plugin-content-docs';
const excludedPrefix = '/docs/adr';

// What a reader should open first.
const gettingStarted = ['/docs', '/docs/quick-start', '/docs/install', '/docs/concepts'];

// Contributor and internal notes: listed under "Optional", which the llms.txt
// format reserves for what a model can skip when its context is short.
const optionalPrefixes = ['/docs/development', '/docs/releasing', '/docs/audit'];

// Docs without a frontmatter description fall back to their first paragraph,
// which can be a whole page; an index entry stays one short line.
const maxDescription = 200;

// The sitemap and the canonical URLs carry no trailing slash.
const clean = (permalink: string) =>
  permalink.length > 1 ? permalink.replace(/\/$/, '') : permalink;

function short(text: string): string {
  const oneLine = text.replace(/\s+/g, ' ').trim();
  if (oneLine.length <= maxDescription) return oneLine;
  return `${oneLine.slice(0, oneLine.lastIndexOf(' ', maxDescription))}...`;
}

type Entry = { title: string; url: string; description: string; permalink: string };

function line({ title, url, description }: Entry): string {
  const text = short(description);
  return text ? `- [${title}](${url}): ${text}` : `- [${title}](${url})`;
}

function section(heading: string, entries: Entry[]): string {
  return entries.length > 0 ? `## ${heading}\n\n${entries.map(line).join('\n')}\n` : '';
}

export default function llmsTxtPlugin(): Plugin {
  return {
    name: 'baleia-llms-txt',
    async postBuild({ plugins, outDir, siteConfig }) {
      const docs = plugins.find((p) => p.name === docsPluginName)?.content as
        LoadedContent | undefined;
      const loaded = docs?.loadedVersions.flatMap((v) => v.docs) ?? [];
      const entries: Entry[] = loaded
        .filter((d) => !d.unlisted)
        .filter(
          (d) => d.permalink !== excludedPrefix && !d.permalink.startsWith(`${excludedPrefix}/`)
        )
        .map((d) => ({
          title: d.title,
          description: d.description ?? '',
          permalink: clean(d.permalink),
          url: new URL(clean(d.permalink), siteConfig.url).toString()
        }))
        .sort((a, b) => a.permalink.localeCompare(b.permalink));

      // An empty index would ship silently if the docs plugin were renamed or
      // reconfigured: fail the build instead.
      if (entries.length === 0) {
        throw new Error('llms.txt: no docs found; is the docs plugin loaded?');
      }

      const start = gettingStarted.flatMap((p) => entries.filter((e) => e.permalink === p));
      const isOptional = (e: Entry) =>
        optionalPrefixes.some((p) => e.permalink === p || e.permalink.startsWith(`${p}/`));
      const rest = entries.filter((e) => !gettingStarted.includes(e.permalink));
      const optional = rest.filter(isOptional);
      const guides = rest.filter((e) => !isOptional(e) && e.permalink.startsWith('/docs/guides/'));
      const reference = rest.filter(
        (e) => !isOptional(e) && !e.permalink.startsWith('/docs/guides/')
      );

      const body = [
        `# ${siteConfig.title}\n`,
        `> ${siteConfig.tagline}. Open source (Apache-2.0), runs on your own infrastructure.\n`,
        'Baleia is a control plane for SQL engines such as Trino: a SQL workbench, a data explorer over your catalogs, a semantic layer, saved content shared by team, and an MCP server for agents.\n',
        section('Get started', start),
        section('Guides', guides),
        section('Reference', reference),
        section('Optional', optional)
      ].join('\n');

      await mkdir(outDir, { recursive: true });
      await writeFile(path.join(outDir, 'llms.txt'), `${body.trimEnd()}\n`);
    }
  };
}

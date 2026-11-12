import type * as Preset from '@docusaurus/preset-classic';
import type { Config } from '@docusaurus/types';
import { themes as prismThemes } from 'prism-react-renderer';
import { readFileSync } from 'node:fs';
import githubLinks from './src/remark/github-links';
import llmsTxt from './plugins/llms-txt';

const repo = 'https://github.com/maltzsama/baleia';
const demo = 'https://demo.baleia.galileostd.io';

// The released version, from the release-please manifest that every release
// bumps (#513). It is the same value the app shows; reading it at build time
// keeps the site honest across releases.
const version = (
  JSON.parse(
    readFileSync(new URL('../.release-please-manifest.json', import.meta.url), 'utf8')
  ) as Record<string, string>
)['.'];

// Umami Cloud website id (a public identifier, not a secret). The docs site is
// the only surface that is measured; the product app carries no analytics. Set
// it as the UMAMI environment variable at build time (the docs-site workflow
// reads the UMAMI repository secret); without it no tracking script is added.
const umami = process.env.UMAMI;

// The site renders the repository's docs/ folder as is, under /docs, so the
// same Markdown reads well on GitHub and here. Links that leave docs/ go to
// GitHub. The landing page is src/pages/index.tsx.
const config: Config = {
  title: 'Baleia',
  tagline: 'A self-hosted SQL workspace for your data platform',
  favicon: 'img/favicon.ico',

  future: {
    v4: true
  },

  url: 'https://baleia.galileostd.io',
  baseUrl: '/',
  organizationName: 'maltzsama',
  projectName: 'baleia',
  trailingSlash: false,

  onBrokenLinks: 'throw',
  onBrokenAnchors: 'throw',
  markdown: {
    // .md files are CommonMark (as on GitHub); only .mdx would be MDX.
    format: 'detect',
    mermaid: true,
    hooks: { onBrokenMarkdownLinks: 'throw' }
  },

  i18n: { defaultLocale: 'en', locales: ['en'] },
  themes: ['@docusaurus/theme-mermaid'],

  // The released version, read above; the landing page renders it.
  customFields: { version },

  scripts: umami
    ? [{ src: 'https://cloud.umami.is/script.js', defer: true, 'data-website-id': umami }]
    : [],

  presets: [
    [
      'classic',
      {
        docs: {
          path: '../docs',
          routeBasePath: '/docs',
          sidebarPath: './sidebars.ts',
          exclude: ['adr/template.md'],
          showLastUpdateTime: true,
          showLastUpdateAuthor: false,
          editUrl: `${repo}/edit/main/`,
          beforeDefaultRemarkPlugins: [
            [githubLinks, { repo, docsDir: 'docs', githubOnly: ['adr/template.md'] }]
          ]
        },
        // Real git dates in <lastmod>; the ADRs stay out of the sitemap but
        // not behind a Disallow (a blocked URL cannot be de-indexed later).
        sitemap: { lastmod: 'date', ignorePatterns: ['/docs/adr/**'] },
        blog: false,
        theme: { customCss: './src/css/custom.css' }
      } satisfies Preset.Options
    ]
  ],

  // The docs were served at the site root until they moved under /docs: keep
  // every old address working.
  plugins: [
    llmsTxt,
    [
      '@docusaurus/plugin-client-redirects',
      {
        createRedirects(existingPath: string) {
          if (existingPath.startsWith('/docs/')) {
            return [existingPath.slice('/docs'.length)];
          }
          return undefined;
        }
      }
    ]
  ],

  themeConfig: {
    // Social card for og:image and twitter:image (1200x630).
    image: 'img/baleia-og.png',
    colorMode: { respectPrefersColorScheme: true },
    navbar: {
      title: 'Baleia',
      logo: { alt: 'Baleia', src: 'img/baleia.svg' },
      items: [
        { type: 'docSidebar', sidebarId: 'docs', position: 'left', label: 'Docs' },
        { to: '/docs/architecture', label: 'Architecture', position: 'left' },
        { href: `${repo}/releases`, label: 'Releases', position: 'right' },
        // One link to the repository: the GitHub mark and the live star count.
        // A raw anchor avoids swizzling the navbar for two elements, and the
        // badge is a shields.io image, not a third-party frame.
        {
          type: 'html',
          position: 'right',
          value:
            `<a class="navbar-github" href="${repo}" target="_blank" rel="noopener" aria-label="Baleia on GitHub" data-umami-event="github-star">` +
            `<svg class="navbar-github__mark" viewBox="0 0 16 16" width="20" height="20" aria-hidden="true"><path fill="currentColor" d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.012 8.012 0 0 0 16 8c0-4.42-3.58-8-8-8z"/></svg>` +
            `<img class="navbar-github__stars" src="https://img.shields.io/github/stars/maltzsama/baleia?style=flat" alt="GitHub stars" loading="lazy" referrerpolicy="no-referrer" />` +
            `</a>`
        }
      ]
    },
    footer: {
      style: 'dark',
      links: [
        {
          title: 'Docs',
          items: [
            { label: 'Quick start', to: '/docs/quick-start' },
            { label: 'Install on Kubernetes', to: '/docs/install' },
            { label: 'Live demo', href: demo },
            { label: 'Concepts', to: '/docs/concepts' },
            { label: 'Configuration', to: '/docs/configuration' }
          ]
        },
        {
          title: 'Features',
          items: [
            { label: 'SQL workbench', to: '/features/workbench' },
            { label: 'Data catalog', to: '/features/data-catalog' },
            { label: 'Semantic layer', to: '/features/semantic-layer' },
            { label: 'Lineage', to: '/features/lineage' },
            { label: 'MCP server', to: '/features/mcp' }
          ]
        },
        {
          title: 'Community',
          items: [
            { label: 'GitHub', href: repo },
            { label: 'Issues', href: `${repo}/issues` },
            { label: 'Credits', href: `${repo}/blob/main/CREDITS.md` }
          ]
        }
      ],
      copyright: `Copyright © ${new Date().getFullYear()} Baleia contributors. Apache License 2.0. Built with Docusaurus.`
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.dracula,
      additionalLanguages: ['bash', 'sql', 'yaml', 'json', 'go']
    }
  } satisfies Preset.ThemeConfig
};

export default config;

import type * as Preset from '@docusaurus/preset-classic';
import type { Config } from '@docusaurus/types';
import { themes as prismThemes } from 'prism-react-renderer';
import githubLinks from './src/remark/github-links';

const repo = 'https://github.com/maltzsama/baleia';
const demo = 'https://demo.baleia.galileostd.io';

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

  presets: [
    [
      'classic',
      {
        docs: {
          path: '../docs',
          routeBasePath: '/docs',
          sidebarPath: './sidebars.ts',
          exclude: ['adr/template.md'],
          editUrl: `${repo}/edit/main/`,
          beforeDefaultRemarkPlugins: [
            [githubLinks, { repo, docsDir: 'docs', githubOnly: ['adr/template.md'] }]
          ]
        },
        blog: false,
        theme: { customCss: './src/css/custom.css' }
      } satisfies Preset.Options
    ]
  ],

  // The docs were served at the site root until they moved under /docs: keep
  // every old address working.
  plugins: [
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
    colorMode: { respectPrefersColorScheme: true },
    navbar: {
      title: 'Baleia',
      logo: { alt: 'Baleia', src: 'img/baleia.svg' },
      items: [
        { type: 'docSidebar', sidebarId: 'docs', position: 'left', label: 'Docs' },
        { to: '/docs/adr', label: 'Decisions', position: 'left' },
        { href: demo, label: 'Live demo', position: 'right' },
        { href: `${repo}/releases`, label: 'Releases', position: 'right' },
        { href: repo, label: 'GitHub', position: 'right' }
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

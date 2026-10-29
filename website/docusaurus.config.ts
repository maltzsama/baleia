import type * as Preset from '@docusaurus/preset-classic';
import type { Config } from '@docusaurus/types';
import { themes as prismThemes } from 'prism-react-renderer';
import githubLinks from './src/remark/github-links';

const repo = 'https://github.com/maltzsama/baleia';

// The site renders the repository's docs/ folder as is, so the same Markdown
// reads well on GitHub and here. Links that leave docs/ go to GitHub.
const config: Config = {
  title: 'Baleia',
  tagline: 'A self-hosted SQL workspace for your data platform',
  favicon: 'img/favicon.ico',
  url: 'https://maltzsama.github.io',
  baseUrl: '/baleia/',
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
          routeBasePath: '/',
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
  themeConfig: {
    navbar: {
      title: 'Baleia',
      logo: { alt: 'Baleia logo', src: 'img/baleia.svg' },
      items: [
        { type: 'docSidebar', sidebarId: 'docs', position: 'left', label: 'Docs' },
        { to: '/adr', label: 'Decisions', position: 'left' },
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
            { label: 'Quick start', to: '/quick-start' },
            { label: 'Install on Kubernetes', to: '/install' },
            { label: 'Configuration', to: '/configuration' }
          ]
        },
        {
          title: 'Project',
          items: [
            { label: 'GitHub', href: repo },
            { label: 'Issues', href: `${repo}/issues` },
            { label: 'Credits', href: `${repo}/blob/main/CREDITS.md` }
          ]
        }
      ],
      copyright: 'Baleia is released under the Apache License 2.0.'
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.dracula,
      additionalLanguages: ['bash', 'sql', 'yaml', 'json', 'go']
    },
    colorMode: { respectPrefersColorScheme: true }
  } satisfies Preset.ThemeConfig
};

export default config;

import { useEffect, type ReactNode } from 'react';
import clsx from 'clsx';
import Link from '@docusaurus/Link';
import useBaseUrl from '@docusaurus/useBaseUrl';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import Head from '@docusaurus/Head';
import Layout from '@theme/Layout';
import Heading from '@theme/Heading';

import styles from './index.module.css';

const repo = 'https://github.com/maltzsama/baleia';

// What a search result and a link preview say about the product. The brand is
// appended by Docusaurus ("... | Baleia"), so the title leads with the keywords.
const pageTitle = 'Self-hosted SQL workspace for Trino and Iceberg';
const pageDescription =
  'Baleia is an open-source, self-hosted SQL workspace and control plane for Trino and Apache Iceberg: one workbench for every engine, every catalog wired, and saved queries, metrics and models shared with your team.';

function HomepageHeader() {
  const { siteConfig } = useDocusaurusContext();
  const version = String(siteConfig.customFields?.version ?? '');
  return (
    <header className={clsx('hero', styles.heroBanner)}>
      <div className="container">
        <img
          src={useBaseUrl('/img/baleia.svg')}
          alt=""
          className={styles.heroLogo}
          width={132}
          height={132}
        />
        <Heading as="h1" className="hero__title">
          {siteConfig.title}
        </Heading>
        <p className="hero__subtitle">{siteConfig.tagline}</p>
        {version && (
          <Link className={styles.version} href={`${repo}/releases/tag/v${version}`}>
            v{version}
          </Link>
        )}
        <p className={styles.lede}>
          Write and run SQL against Trino and your databases, browse every catalog in one place, and
          share queries, metrics and models with your team.
        </p>
        <div className={styles.buttons}>
          <Link
            className={clsx('button button--lg', styles.outlineButton)}
            to="/docs/quick-start"
            data-umami-event="get-started"
          >
            Get Started
          </Link>
          <Link
            className={clsx('button button--lg', styles.outlineButton)}
            href="https://demo.baleia.galileostd.io"
            data-umami-event="try-live-demo"
          >
            Try the live demo
          </Link>
          <Link className={clsx('button button--lg', styles.outlineButton)} href={repo}>
            Star on GitHub
          </Link>
        </div>
      </div>
    </header>
  );
}

const features = [
  {
    title: 'Every Engine, One Workbench',
    description:
      'Trino, plus PostgreSQL, MySQL/MariaDB, SQL Server, Oracle, ClickHouse, SQLite and Arrow Flight SQL directly — with autocomplete, scripts, history and exports.',
    to: '/docs/guides/workbench'
  },
  {
    title: 'Your Lakehouse, Wired',
    description:
      'Register Polaris, Nessie, Lakekeeper, Iceberg REST, Unity, AWS Glue or Hive Metastore, and Baleia creates and keeps the matching Trino catalog.',
    to: '/docs/connections'
  },
  {
    title: 'A Control Plane, Not a Proxy',
    description:
      'The engines keep the data. Baleia manages connections, access, saved content, monitoring and per-table Iceberg maintenance around them.',
    to: '/docs/architecture'
  },
  {
    title: 'Questions Without SQL',
    description:
      'Describe a table once as a model; anyone explores its dimensions and metrics, and Baleia compiles correct SQL for the engine.',
    to: '/docs/guides/explore'
  },
  {
    title: 'Content as Code',
    description:
      'Saved queries, metrics and models live in spaces with version history, and export to deterministic bundles you can keep in Git.',
    to: '/docs/guides/content'
  },
  {
    title: 'Access You Can Audit',
    description:
      'Workspaces, teams and roles, OIDC or LDAP sign-in, API keys, read-only agents over MCP, and a best-effort audit trail of operations.',
    to: '/docs/guides/administration'
  }
];

function Feature({ title, description, to }: { title: string; description: string; to: string }) {
  return (
    <div className={clsx('col col--4', styles.feature)}>
      <Link to={to} className={styles.featureLink}>
        <Heading as="h3">{title}</Heading>
        <p>{description}</p>
      </Link>
    </div>
  );
}

function HomepageFeatures() {
  return (
    <section className={styles.features}>
      <div className="container">
        <Heading as="h2" className={styles.srOnly}>
          What you can do with Baleia
        </Heading>
        <div className="row">
          {features.map((props) => (
            <Feature key={props.title} {...props} />
          ))}
        </div>
      </div>
    </section>
  );
}

// The introduction was the site root before it moved to /docs, so old
// bookmarks to its sections (e.g. /#where-to-start) land here: send them on.
const introAnchors = ['what-you-can-do', 'where-to-start'];

// Structured data for the product, so a result can show what Baleia is.
function structuredData(version: string, url: string) {
  return {
    '@context': 'https://schema.org',
    '@type': 'SoftwareApplication',
    name: 'Baleia',
    description: pageDescription,
    url,
    codeRepository: repo,
    sameAs: repo,
    applicationCategory: 'DeveloperApplication',
    operatingSystem: 'Linux, Kubernetes',
    license: 'https://www.apache.org/licenses/LICENSE-2.0',
    ...(version && { softwareVersion: version }),
    offers: { '@type': 'Offer', price: '0', priceCurrency: 'USD' }
  };
}

export default function Home(): ReactNode {
  const { siteConfig } = useDocusaurusContext();
  const docsUrl = useBaseUrl('/docs');
  useEffect(() => {
    const anchor = window.location.hash.slice(1);
    if (introAnchors.includes(anchor)) {
      window.location.replace(`${docsUrl}#${anchor}`);
    }
  }, [docsUrl]);
  return (
    <Layout title={pageTitle} description={pageDescription}>
      <Head>
        <script type="application/ld+json">
          {JSON.stringify(
            structuredData(String(siteConfig.customFields?.version ?? ''), siteConfig.url)
          )}
        </script>
      </Head>
      <HomepageHeader />
      <main>
        <HomepageFeatures />
      </main>
    </Layout>
  );
}

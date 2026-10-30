import type { ReactNode } from 'react';
import clsx from 'clsx';
import Link from '@docusaurus/Link';
import useBaseUrl from '@docusaurus/useBaseUrl';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import Layout from '@theme/Layout';
import Heading from '@theme/Heading';

import styles from './index.module.css';

function HomepageHeader() {
  const { siteConfig } = useDocusaurusContext();
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
        <p className={styles.lede}>
          Write and run SQL against Trino and your databases, browse every catalog in one
          place, and share queries, metrics and models with your team.
        </p>
        <div className={styles.buttons}>
          <Link className={clsx('button button--lg', styles.outlineButton)} to="/docs/quick-start">
            Get Started
          </Link>
          <iframe
            src="https://ghbtns.com/github-btn.html?user=maltzsama&repo=baleia&type=star&count=true&size=large"
            width="160"
            height="30"
            title="GitHub Stars"
            className={styles.githubButton}
          />
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
      'Workspaces, teams and roles, OIDC or LDAP sign-in, API keys, read-only agents over MCP, and an audit trail of every operation.',
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
        <div className="row">
          {features.map((props) => (
            <Feature key={props.title} {...props} />
          ))}
        </div>
      </div>
    </section>
  );
}

export default function Home(): ReactNode {
  const { siteConfig } = useDocusaurusContext();
  return (
    <Layout title={siteConfig.title} description={siteConfig.tagline}>
      <HomepageHeader />
      <main>
        <HomepageFeatures />
      </main>
    </Layout>
  );
}

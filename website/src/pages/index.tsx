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
const demo = 'https://demo.baleia.galileostd.io';

// What a search result and a link preview say about the product. The brand is
// appended by Docusaurus ("... | Baleia"), so the title leads with the keywords.
const pageTitle = 'Self-hosted SQL workspace for Trino and Iceberg';
const pageDescription =
  'Baleia is an open-source, self-hosted SQL workspace and control plane for Trino and Apache Iceberg: one workbench for every engine, every catalog wired, and saved queries, metrics and models shared with your team.';

// The engines and catalog managers a single Baleia deployment talks to. Names
// only: the site keeps its own type, and never draws another product's logo.
const engines = [
  'Trino',
  'Apache Iceberg',
  'Polaris',
  'Lakekeeper',
  'Nessie',
  'Unity Catalog',
  'AWS Glue',
  'Hive Metastore',
  'PostgreSQL',
  'ClickHouse',
  'SQL Server',
  'Oracle'
];

// The three moves that describe the product without a screenshot: connect the
// engines, model the data, then query and share it.
const steps = [
  {
    title: 'Connect what you run',
    body:
      'Register Trino, direct databases and lakehouse catalogs. Baleia creates and keeps the matching Trino catalog in sync.'
  },
  {
    title: 'Model and explore',
    body:
      'Document tables, define metrics and semantic models once, then explore dimensions and metrics without writing SQL.'
  },
  {
    title: 'Query and share',
    body:
      'Run SQL or Explore on your engines, keep results and history, and share versions with your team in spaces.'
  }
];

// The control plane's request path, as one readable flow.
const flow = [
  { label: 'Web app', note: 'SvelteKit over REST and WebSocket' },
  { label: 'Go API', note: 'Auth, workspaces and saved content' },
  { label: 'Temporal + worker', note: 'Every long-running job' },
  { label: 'Trino and direct engines', note: 'The engines keep the data' }
];

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

function HomepageHeader() {
  const { siteConfig } = useDocusaurusContext();
  const version = String(siteConfig.customFields?.version ?? '');
  return (
    <header className={styles.heroBanner}>
      <div className="container">
        <div className={styles.heroInner}>
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
          <div className={styles.release}>
            {version && (
              <Link className={styles.version} href={`${repo}/releases/tag/v${version}`}>
                v{version}
              </Link>
            )}
            <span className={styles.stage}>beta</span>
          </div>
          <p className={styles.lede}>
            Write and run SQL against Trino and your databases, browse every catalog, and share
            queries, metrics and models with your team — on your own infrastructure.
          </p>
          <div className={styles.buttons}>
            <Link
              className={clsx(styles.button, styles.primaryButton)}
              to="/docs/quick-start"
              data-umami-event="get-started"
            >
              Get started
            </Link>
            <Link
              className={clsx(styles.button, styles.outlineButton)}
              href={demo}
              data-umami-event="try-live-demo"
            >
              Try the live demo
            </Link>
            <Link
              className={clsx(styles.button, styles.ghostButton)}
              to="/docs/install"
              data-umami-event="install-kubernetes"
            >
              Install on Kubernetes <span aria-hidden="true">→</span>
            </Link>
          </div>
          <ul className={styles.heroFacts}>
            <li>Open source, Apache 2.0</li>
            <li>Self-hosted</li>
            <li>English and Brazilian Portuguese</li>
          </ul>
        </div>
      </div>
    </header>
  );
}

function Engines() {
  return (
    <section className={styles.engines} aria-label="Engines and catalogs">
      <div className="container">
        <p className={styles.enginesLabel}>
          One workspace over the engines and catalogs you already run
        </p>
        <ul className={styles.engineList}>
          {engines.map((engine) => (
            <li key={engine} className={styles.engineChip}>
              {engine}
            </li>
          ))}
        </ul>
      </div>
    </section>
  );
}

function HowItWorks() {
  return (
    <section className={styles.section} aria-labelledby="how-it-works">
      <div className="container">
        <div className={styles.sectionHead}>
          <span className={styles.kicker}>How it works</span>
          <Heading as="h2" id="how-it-works" className={styles.sectionTitle}>
            Connect, model, then query and share
          </Heading>
          <p className={styles.sectionLede}>
            Baleia sits around your engines instead of replacing them, so your BI tools and pipelines
            keep talking to the data where it already lives.
          </p>
        </div>
        <div className="row">
          {steps.map((step, index) => (
            <div className="col col--4" key={step.title}>
              <div className={styles.step}>
                <span className={styles.stepNum} aria-hidden="true">
                  {String(index + 1).padStart(2, '0')}
                </span>
                <Heading as="h3" className={styles.stepTitle}>
                  {step.title}
                </Heading>
                <p className={styles.stepBody}>{step.body}</p>
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

function Feature({
  title,
  description,
  to,
  index
}: {
  title: string;
  description: string;
  to: string;
  index: number;
}) {
  return (
    <div className={clsx('col col--4', styles.feature)}>
      <Link to={to} className={styles.featureLink}>
        <span className={styles.featureTop}>
          <span className={styles.featureIndex} aria-hidden="true">
            {String(index + 1).padStart(2, '0')}
          </span>
          <span className={styles.featureArrow} aria-hidden="true">
            ↗
          </span>
        </span>
        <Heading as="h3" className={styles.featureTitle}>
          {title}
        </Heading>
        <p className={styles.featureBody}>{description}</p>
      </Link>
    </div>
  );
}

function HomepageFeatures() {
  return (
    <section
      className={clsx(styles.section, styles.sectionTint)}
      aria-labelledby="home-features"
    >
      <div className="container">
        <div className={styles.sectionHead}>
          <span className={styles.kicker}>What you can do</span>
          <Heading as="h2" id="home-features" className={styles.sectionTitle}>
            Everything around the query, in one place
          </Heading>
        </div>
        <div className="row">
          {features.map((props, index) => (
            <Feature key={props.title} {...props} index={index} />
          ))}
        </div>
      </div>
    </section>
  );
}

function ControlPlane() {
  return (
    <section className={styles.controlPlane} aria-labelledby="control-plane">
      <div className="container">
        <div className="row">
          <div className="col col--6">
            <div className={styles.cpText}>
              <span className={styles.kicker}>A control plane</span>
              <Heading as="h2" id="control-plane" className={styles.cpTitle}>
                Not another query engine
              </Heading>
              <p className={styles.cpLede}>
                Baleia manages connections, catalogs and engines, runs your SQL on those engines and
                keeps the results. Your BI tools and pipelines keep talking to the engines directly.
              </p>
              <ul className={styles.cpList}>
                <li>Results are stored as Arrow objects and read back page by page.</li>
                <li>Workload management queues runs per engine and class.</li>
                <li>Prometheus, OpenTelemetry and an audit trail of security and data operations.</li>
              </ul>
            </div>
          </div>
          <div className="col col--6">
            <ol className={styles.flow} aria-label="How a query runs">
              {flow.map((item, index) => (
                <li className={styles.flowItem} key={item.label}>
                  <span className={styles.flowLabel}>{item.label}</span>
                  <span className={styles.flowNote}>{item.note}</span>
                  {index < flow.length - 1 && (
                    <span className={styles.flowArrow} aria-hidden="true">
                      ↓
                    </span>
                  )}
                </li>
              ))}
            </ol>
          </div>
        </div>
      </div>
    </section>
  );
}

function GetStarted() {
  return (
    <section className={styles.ctaSection} aria-labelledby="get-started">
      <div className="container">
        <div className={styles.ctaCard}>
          <span className={styles.kicker}>Get started</span>
          <Heading as="h2" id="get-started" className={styles.ctaTitle}>
            Run it in a few minutes
          </Heading>
          <p className={styles.ctaLede}>
            Docker with Compose and <code>make</code> brings up Baleia with Trino, Polaris, Temporal
            and object storage. Each release also publishes a Helm chart.
          </p>
          <pre className={styles.ctaCode}>
            <code>{'git clone https://github.com/maltzsama/'}<wbr />{'baleia.git\ncd baleia\nmake up'}</code>
          </pre>
          <div className={clsx(styles.buttons, styles.ctaButtons)}>
            <Link
              className={clsx(styles.button, styles.primaryButton)}
              to="/docs/quick-start"
              data-umami-event="get-started-bottom"
            >
              Read the quick start
            </Link>
            <Link
              className={clsx(styles.button, styles.secondaryButton)}
              to="/docs/install"
              data-umami-event="install-bottom"
            >
              Install on Kubernetes
            </Link>
            <Link
              className={clsx(styles.button, styles.ghostButton, styles.ghostDark)}
              href={demo}
              data-umami-event="demo-bottom"
            >
              Try the live demo <span aria-hidden="true">→</span>
            </Link>
          </div>
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
        <Engines />
        <HowItWorks />
        <HomepageFeatures />
        <ControlPlane />
        <GetStarted />
      </main>
    </Layout>
  );
}

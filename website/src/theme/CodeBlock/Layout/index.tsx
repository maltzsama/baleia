import React, { type ReactNode } from 'react';
import clsx from 'clsx';
import { useCodeBlockContext } from '@docusaurus/theme-common/internal';
import Container from '@theme/CodeBlock/Container';
import Title from '@theme/CodeBlock/Title';
import Content from '@theme/CodeBlock/Content';
import type { Props } from '@theme/CodeBlock/Layout';
import Buttons from '@theme/CodeBlock/Buttons';
import styles from './styles.module.css';

// A code block with a title (```bash title="…") gets a terminal-like header:
// three dots and the title.
export default function CodeBlockLayout({ className }: Props): ReactNode {
  const { metadata } = useCodeBlockContext();
  return (
    <Container as="div" className={clsx(className, metadata.className)}>
      {metadata.title && (
        <div className={styles.codeBlockHeader}>
          <div className={styles.dots}>
            <span className={styles.dot} />
            <span className={styles.dot} />
            <span className={styles.dot} />
          </div>
          <span className={styles.headerTitle}>
            <Title>{metadata.title}</Title>
          </span>
        </div>
      )}
      <div className={styles.codeBlockContent}>
        <Content />
        <Buttons />
      </div>
    </Container>
  );
}

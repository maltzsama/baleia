import path from 'node:path';

type Node = { type: string; url?: string; children?: Node[] };
type Options = { repo: string; docsDir: string; githubOnly?: string[] };
type VFile = { path?: string };

// githubLinks points relative links that leave the docs folder (../AGENTS.md,
// ../deploy/...), or reach a docs file the site excludes (githubOnly, relative
// to docs/), at the file on GitHub: the same links work as they are when the
// Markdown is read on GitHub.
export default function githubLinks({ repo, docsDir, githubOnly = [] }: Options) {
  return (tree: Node, file: VFile) => {
    const filePath = file.path;
    if (!filePath) return;
    const docsRoot = docsRootOf(filePath, docsDir);
    if (!docsRoot) return;
    const repoRoot = path.dirname(docsRoot);
    visit(tree, (node) => {
      if ((node.type !== 'link' && node.type !== 'definition') || !node.url) return;
      const url = node.url;
      if (/^[a-z][a-z0-9+.-]*:/i.test(url) || url.startsWith('#') || url.startsWith('/')) return;
      const [target, hash] = url.split('#');
      const abs = path.resolve(path.dirname(filePath), target);
      const inDocs = abs === docsRoot || abs.startsWith(docsRoot + path.sep);
      const excluded = githubOnly.includes(path.relative(docsRoot, abs).split(path.sep).join('/'));
      if (inDocs && !excluded) return;
      const rel = path.relative(repoRoot, abs).split(path.sep).join('/');
      node.url = `${repo}/blob/main/${rel}${hash ? `#${hash}` : ''}`;
    });
  };
}

function docsRootOf(filePath: string, docsDir: string): string | undefined {
  let dir = path.dirname(filePath);
  while (dir !== path.dirname(dir)) {
    if (path.basename(dir) === docsDir) return dir;
    dir = path.dirname(dir);
  }
  return undefined;
}

function visit(node: Node, fn: (n: Node) => void) {
  fn(node);
  for (const child of node.children ?? []) visit(child, fn);
}

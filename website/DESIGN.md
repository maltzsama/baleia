# DESIGN.md — Baleia documentation site

The visual language of the Baleia documentation site (`website/`): the Docusaurus
site that renders `docs/` and the landing page. The product app (`web/`) has its
own Svelte/Tailwind system; this file covers the site.

## Visual Theme & Atmosphere

Technical editorial with a playful brand. Serif headings (Fraunces), a neon
wordmark (Monoton) taken from the whale mark, square controls, terminal-like
code blocks and a faint dot grid behind the reading column. Dense where it helps
scanning (sidebars, tables), generous where it helps reading (prose). The mark
is a whale with a star and a small pet; the palette is the mark's.

## Color Palette & Roles

| Name | Light | Dark | Role |
|---|---|---|---|
| Brand purple | `#3a1061` | `#3a1061` | Hero and control-plane bands |
| Brand yellow | `#ffeb87` | `#ffeb87` | Primary CTA, hover accents, highlights |
| Brand pink | `#ff8f94` | `#ff8f94` | Accent glow, small highlights |
| Primary | `#6b2fa3` | `#c3a3f5` | Links, active nav, TOC marker, focus |
| Background | `#ffffff` | `#121212` | Page |
| Surface | `#ffffff` | `#1e1e1e` | Cards, navbar, tables |
| Muted | `color-mix(in srgb, text 55–72%, transparent)` | same | Secondary text, labels |
| Border | `color-mix(in srgb, text 14%, transparent)` | same | Rules, table borders |

Admonitions use Infima's functional colors (info, success, warning, danger).

## Typography Rules

- **Wordmark** (hero h1, navbar title): Monoton, weight 400, no tracking.
- **Content headings**: Fraunces, 400–600; `h2` carries a 1px rule.
- **Body**: system sans (Infima), 16px, line-height 1.65.
- **Code and labels**: monospace; uppercase with 0.08–0.12em tracking for
  kickers, table headers, the TOC label and pagination sublabels.
- **Prose measure**: 46rem on `.theme-doc-markdown` paragraphs, lists and
  quotes; tables and code keep the full column.
- Hero title `clamp(2.6rem, 5.5vw + 1rem, 5rem)`; subtitle
  `clamp(1.1rem, 1.2vw + 0.8rem, 1.35rem)`.

## Component Stylings

- **Buttons**: square (`border-radius: 0`), 2px border, bold. Primary = yellow
  fill with purple text; secondary = primary outline; ghost = underlined link.
  Hover lifts 1px (dropped under reduced motion).
- **Links**: inline links are always underlined (1px, 45% `currentColor`; 2px on
  hover) with `text-underline-offset: .18em`. Heading hash-links are excluded.
- **Navbar**: 1px bottom border, no shadow; Monoton wordmark; GitHub mark + stars.
- **Sidebar**: square links; hover tint; active = 12% primary tint, 3px primary
  inset bar, primary text in 600.
- **TOC**: an “On this page” mono label; resting links at 72% text; active =
  primary + a 2px primary inset bar.
- **Mobile TOC**: square, 1px border, 5% primary tint.
- **Code blocks**: dark card, 16px radius, 1px border, a terminal header with
  three dots for titled blocks, a copy button; 13px/1.6 code.
- **Inline code**: 4px radius, 1px primary-20% border, 7% primary tint.
- **Tables**: 1px border; header = 9% primary tint with mono uppercase labels;
  cells wrap (long values break instead of clipping); zebra = 3% primary; row
  hover = 6% primary; scrolls horizontally on narrow screens.
- **Admonitions**: square, 4px left border, no shadow.
- **Breadcrumbs**: mono 0.72rem; the current page is a square 10% primary pill.
- **Pagination**: square cards, 1px border, hover primary + 1px lift; mono
  sublabel and a Fraunces page title.
- **Doc footer**: 1px top rule; “Edit this page” and last updated in mono 0.72rem.
- **Landing sections**: engine chips (mono, 1px border), numbered steps, feature
  cards (index + arrow), a purple control-plane band with a flow list, and a
  closing CTA with a terminal snippet.

## Layout Principles

- 4px spacing base; section padding 5rem desktop, 3.5rem at ≤996px.
- Landing hero padding `clamp(2rem, 5vh, 4.5rem)`; logo
  `clamp(84px, 11vh, 116px)`.
- Docs grid: ~305px sidebar, ~815px prose column, TOC rail on the right.
- Prose measure 46rem; tables and code use the full column.
- One `.container` per section; left-aligned reading, centered landing hero/CTA.

## Depth & Elevation

- No shadows on controls, cards, navbar or sidebar.
- Code blocks are the only raised surface (`0 8px 30px rgba(0,0,0,.12/.4)`).
- Borders (1px, text 14%) and tints carry hierarchy.
- Background: a faint 32px dot grid behind the docs content (5% light / 4%
  dark); hero and control-plane bands have radial yellow/pink glows and a
  vignette.

## Do's and Don'ts

Do:
- Keep the Monoton wordmark and `static/img/baleia.svg` in every change.
- Use the brand tokens; square controls; underline inline links; mono for labels.
- Let tables and code use the full width; keep prose at 46rem.
- Check light and dark at 390, 768, 1024 and 1440.

Don't:
- Don't round controls or add shadows outside code blocks.
- Don't introduce a second accent hue; yellow is the only highlight.
- Don't put captions under images or use emoji as icons.
- Don't remove the underline from inline links.
- Don't let the hero exceed one screen; it should end cleanly at the fold.

## Responsive Behavior

- **996px**: hero padding shrinks, landing steps stack, the control-plane flow
  moves below the text; the docs TOC becomes the collapsible “On this page”.
- **640px**: hero facts wrap, buttons stack full width, the CTA code shrinks and
  wraps.
- **Touch**: aim for ≥44px nav controls; the mobile sidebar link is the known gap
  (32px today).
- Tables scroll horizontally and wrap values; there is no page-level horizontal
  scroll.
- The hero fits one screen at 390×844, 768×1024, 1024×768, 1366×768, 1440×900 and
  1920×1080.

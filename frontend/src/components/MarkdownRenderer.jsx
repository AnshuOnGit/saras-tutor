import { useEffect, useRef, useId } from "react";
import ReactMarkdown from "react-markdown";
import remarkMath from "remark-math";
import remarkGfm from "remark-gfm";
import rehypeKatex from "rehype-katex";
import mermaid from "mermaid";
import "katex/dist/katex.min.css";

const MERMAID_DIAGRAM_START =
  /^(flowchart|graph|sequenceDiagram|classDiagram|stateDiagram(?:-v2)?|erDiagram|journey|gantt|pie|mindmap|timeline|quadrantChart|xychart-beta|packet-beta|block-beta|architecture|requirementDiagram|gitGraph|kanban|sankey-beta|C4Context|C4Container|C4Component|C4Dynamic|C4Deployment)\b/;

// Initialise Mermaid once — dark-mode aware
mermaid.initialize({
  startOnLoad: false,
  theme: "default",
  securityLevel: "loose",
  fontFamily: "Inter, sans-serif",
});

/* =============================================================
 * preprocessMath
 *
 * Many LLMs (including OpenAI-compatible proxies) emit LaTeX-style
 * delimiters  \( … \)  and  \[ … \]  instead of the dollar-sign
 * delimiters that remark-math expects.
 *
 * This function converts *both* flavours so the pipeline can handle
 * whatever the model sends:
 *
 *   \( … \)   →   $ … $       (inline math)
 *   \[ … \]   →   $$ … $$     (display math)
 *
 * It deliberately avoids touching content inside fenced code blocks
 * (``` … ```) so that LaTeX examples in code are preserved verbatim.
 * ============================================================= */
function preprocessMath(text) {
  // Split by fenced code blocks to avoid mangling code content
  const parts = text.split(/(```[\s\S]*?```)/g);

  for (let i = 0; i < parts.length; i++) {
    // Odd indices are inside code fences — leave them alone
    if (i % 2 === 1) continue;

    let segment = parts[i];

    // ── Fix corrupted LaTeX from JSON-parsing ────────────────────────
    // When an LLM writes \frac in a JSON string without double-escaping,
    // \f becomes a form-feed char, \b becomes backspace, etc.
    // Restore them so KaTeX can render the commands.
    segment = segment.replace(/\f/g,   '\\f');    // form feed  → \f
    segment = segment.replace(/\x08/g, '\\b');    // backspace  → \b
    segment = segment.replace(/\r(?=[a-z])/g, '\\r');  // CR + letter → \r
    segment = segment.replace(/\t(?=[a-z])/g, '\\t');  // tab + letter → \t

    // Display math: \[ … \]  (may span multiple lines)
    segment = segment.replace(
      /\\\[([\s\S]*?)\\\]/g,
      (_, math) => `$$${math}$$`
    );

    // Inline math: \( … \)  (single line only, non-greedy)
    segment = segment.replace(
      /\\\((.+?)\\\)/g,
      (_, math) => `$${math}$`
    );

    parts[i] = segment;
  }

  return parts.join("");
}

function escapeHtml(text) {
  return text
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");
}

function normalizeMermaid(code) {
  const cleaned = code
    .replace(/\r\n/g, "\n")
    .replace(/^```mermaid\s*/i, "")
    .replace(/^```\s*/i, "")
    .replace(/```\s*$/i, "")
    .replace(/[“”]/g, '"')
    .replace(/[‘’]/g, "'")
    .replace(/[–—]/g, "-")
    .replace(/\t/g, "  ")
    .trim();

  const lines = cleaned.split("\n");
  const startIndex = lines.findIndex((line) =>
    MERMAID_DIAGRAM_START.test(line.trim())
  );

  if (startIndex === -1) {
    return cleaned;
  }

  return lines.slice(startIndex).join("\n").trim();
}

/**
 * MermaidBlock renders a ```mermaid code fence as an SVG diagram.
 * Each block gets a unique id so Mermaid can target it.
 */
function MermaidBlock({ code }) {
  const containerRef = useRef(null);
  const uniqueId = useId().replace(/:/g, "_"); // mermaid ids can't contain ':'

  useEffect(() => {
    let cancelled = false;
    const normalizedCode = normalizeMermaid(code);

    async function render() {
      if (!containerRef.current || !normalizedCode) return;
      try {
        await mermaid.parse(normalizedCode, { suppressErrors: false });
        const { svg } = await mermaid.render(`mermaid${uniqueId}`, normalizedCode);
        if (!cancelled && containerRef.current) {
          containerRef.current.innerHTML = svg;
        }
      } catch (err) {
        if (!cancelled && containerRef.current) {
          const message = err?.message || "Invalid Mermaid diagram";
          containerRef.current.innerHTML = `<div><pre class="mermaid-error">Diagram preview unavailable: ${escapeHtml(message)}</pre><pre class="language-mermaid"><code>${escapeHtml(normalizedCode)}</code></pre></div>`;
        }
      }
    }

    render();
    return () => { cancelled = true; };
  }, [code, uniqueId]);

  return <div ref={containerRef} className="mermaid-container" />;
}

/**
 * CodeBlock is the custom renderer for fenced code blocks.
 * - ```mermaid → renders as a diagram
 * - everything else → syntax-highlighted <pre><code>
 */
function CodeBlock({ className, children, ...props }) {
  const match = /language-(\w+)/.exec(className || "");
  const lang = match?.[1];
  const code = String(children).replace(/\n$/, "");

  if (lang === "mermaid") {
    return <MermaidBlock code={code} />;
  }

  return (
    <div className="code-block-wrapper">
      {lang && <span className="code-lang-badge">{lang}</span>}
      <pre className={className}>
        <code {...props}>{children}</code>
      </pre>
    </div>
  );
}

/**
 * MarkdownRenderer — the main export.
 * Renders a markdown string with:
 *   • GFM (tables, strikethrough, autolinks)
 *   • LaTeX math ($inline$ and $$display$$)
 *   • Mermaid diagrams (```mermaid)
 *   • styled code blocks
 */
export default function MarkdownRenderer({ content }) {
  if (!content) return null;

  const processed = preprocessMath(content);

  return (
    <div className="markdown-body">
      <ReactMarkdown
        remarkPlugins={[remarkGfm, remarkMath]}
        rehypePlugins={[rehypeKatex]}
        components={{
          // Route all code elements through CodeBlock
          code({ node, inline, className, children, ...props }) {
            if (inline) {
              return <code className="inline-code" {...props}>{children}</code>;
            }
            return <CodeBlock className={className} {...props}>{children}</CodeBlock>;
          },
        }}
      >
        {processed}
      </ReactMarkdown>
    </div>
  );
}

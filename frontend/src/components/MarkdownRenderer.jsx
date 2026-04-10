import { useEffect, useRef, useId } from "react";
import ReactMarkdown from "react-markdown";
import remarkMath from "remark-math";
import remarkGfm from "remark-gfm";
import rehypeKatex from "rehype-katex";
import mermaid from "mermaid";
import "katex/dist/katex.min.css";

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

/**
 * MermaidBlock renders a ```mermaid code fence as an SVG diagram.
 * Each block gets a unique id so Mermaid can target it.
 */
function MermaidBlock({ code }) {
  const containerRef = useRef(null);
  const uniqueId = useId().replace(/:/g, "_"); // mermaid ids can't contain ':'

  useEffect(() => {
    let cancelled = false;

    async function render() {
      if (!containerRef.current || !code.trim()) return;
      try {
        const { svg } = await mermaid.render(`mermaid${uniqueId}`, code.trim());
        if (!cancelled && containerRef.current) {
          containerRef.current.innerHTML = svg;
        }
      } catch (err) {
        if (!cancelled && containerRef.current) {
          containerRef.current.innerHTML = `<pre class="mermaid-error">Diagram error: ${err.message}</pre>`;
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

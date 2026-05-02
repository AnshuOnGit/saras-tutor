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
    segment = segment.replace(/\f/g,   '\\f');
    segment = segment.replace(/\x08/g, '\\b');
    segment = segment.replace(/\r(?=[a-z])/g, '\\r');
    segment = segment.replace(/\t(?=[a-z])/g, '\\t');

    // ── Convert Unicode math symbols to LaTeX (outside existing $ blocks) ──
    segment = convertUnicodeMath(segment);

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

    // ── Streaming safety: hide incomplete trailing delimiters ──
    // If a trailing $ or $$ has no closing pair, it will cause KaTeX parse
    // errors mid-stream. Strip an unmatched trailing delimiter.
    segment = segment.replace(/\$\$[^$]*$/, (m) => {
      // If there's no closing $$, treat as plain text
      return m.includes('$$') && m.lastIndexOf('$$') === 0 ? m.replace('$$', '＄＄') : m;
    });
    // Single trailing $ with no close
    const singleDollarParts = segment.split(/(?<!\$)\$(?!\$)/g);
    if (singleDollarParts.length % 2 === 0) {
      // Odd number of $ delimiters means one is unmatched at end — escape it
      const lastIdx = segment.lastIndexOf('$');
      if (lastIdx >= 0 && segment[lastIdx - 1] !== '$' && segment[lastIdx + 1] !== '$') {
        segment = segment.slice(0, lastIdx) + '＄' + segment.slice(lastIdx + 1);
      }
    }

    parts[i] = segment;
  }

  return parts.join("");
}

/* Convert common Unicode math characters to KaTeX equivalents when outside $ blocks */
function convertUnicodeMath(text) {
  // Split on existing math regions to avoid double-converting
  const mathRegions = /(\$\$[\s\S]*?\$\$|\$[^$\n]+?\$)/g;
  const pieces = text.split(mathRegions);

  for (let i = 0; i < pieces.length; i++) {
    if (i % 2 === 1) continue; // inside math — skip

    let s = pieces[i];

    // If this piece contains an unclosed $$ or $ at the end (streaming),
    // split it: only convert the safe prefix, leave the trailing math-in-progress alone.
    let trailingMath = '';
    const lastDD = s.lastIndexOf('$$');
    if (lastDD >= 0) {
      // Check if this $$ is unclosed (no matching $$ after it)
      const after = s.slice(lastDD + 2);
      if (!after.includes('$$')) {
        trailingMath = s.slice(lastDD);
        s = s.slice(0, lastDD);
      }
    } else {
      // Check for unclosed single $
      const lastD = s.lastIndexOf('$');
      if (lastD >= 0) {
        const after = s.slice(lastD + 1);
        if (!after.includes('$')) {
          trailingMath = s.slice(lastD);
          s = s.slice(0, lastD);
        }
      }
    }

    // Superscripts
    s = s.replace(/([a-zA-Z0-9])([⁰¹²³⁴⁵⁶⁷⁸⁹⁺⁻⁼⁽⁾ⁿⁱ]+)/g, (_, base, sups) => {
      const map = {'⁰':'0','¹':'1','²':'2','³':'3','⁴':'4','⁵':'5','⁶':'6','⁷':'7','⁸':'8','⁹':'9','⁺':'+','⁻':'-','⁼':'=','⁽':'(','⁾':')','ⁿ':'n','ⁱ':'i'};
      const converted = [...sups].map(c => map[c] || c).join('');
      return `$${base}^{${converted}}$`;
    });
    // Subscripts
    s = s.replace(/([a-zA-Z0-9])([₀₁₂₃₄₅₆₇₈₉₊₋₌₍₎]+)/g, (_, base, subs) => {
      const map = {'₀':'0','₁':'1','₂':'2','₃':'3','₄':'4','₅':'5','₆':'6','₇':'7','₈':'8','₉':'9','₊':'+','₋':'-','₌':'=','₍':'(','₎':')'};
      const converted = [...subs].map(c => map[c] || c).join('');
      return `$${base}_{${converted}}$`;
    });
    // Common symbols — only replace outside math context
    s = s.replace(/×/g, '$\\times$');
    s = s.replace(/÷/g, '$\\div$');
    s = s.replace(/±/g, '$\\pm$');
    s = s.replace(/∓/g, '$\\mp$');
    s = s.replace(/≠/g, '$\\neq$');
    s = s.replace(/≤/g, '$\\leq$');
    s = s.replace(/≥/g, '$\\geq$');
    s = s.replace(/∞/g, '$\\infty$');
    s = s.replace(/√/g, '$\\sqrt{}$');
    s = s.replace(/π/g, '$\\pi$');
    s = s.replace(/θ/g, '$\\theta$');
    s = s.replace(/α/g, '$\\alpha$');
    s = s.replace(/β/g, '$\\beta$');
    s = s.replace(/γ/g, '$\\gamma$');
    s = s.replace(/δ/g, '$\\delta$');
    s = s.replace(/λ/g, '$\\lambda$');
    s = s.replace(/μ/g, '$\\mu$');
    s = s.replace(/σ/g, '$\\sigma$');
    s = s.replace(/ω/g, '$\\omega$');
    s = s.replace(/Δ/g, '$\\Delta$');
    s = s.replace(/→/g, '$\\rightarrow$');
    s = s.replace(/←/g, '$\\leftarrow$');
    s = s.replace(/⇌/g, '$\\rightleftharpoons$');
    // Merge adjacent $ blocks: $X$$Y$ → $X Y$ (avoids ugly spacing)
    s = s.replace(/\$\s*\$\$/g, ' ');
    s = s.replace(/\$\s+\$/g, ' ');

    pieces[i] = s + trailingMath;
  }
  return pieces.join('');
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
        rehypePlugins={[[rehypeKatex, { throwOnError: false, errorColor: '#cc0000' }]]}
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

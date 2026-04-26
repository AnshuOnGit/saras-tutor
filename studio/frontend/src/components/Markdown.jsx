import ReactMarkdown from "react-markdown";
import remarkMath from "remark-math";
import remarkGfm from "remark-gfm";
import rehypeKatex from "rehype-katex";

// Detect bare LaTeX lines (not wrapped in $/$$ delimiters) and wrap them.
const LATEX_CMD = /\\(?:frac|sqrt|sum|prod|int|lim|infty|alpha|beta|gamma|delta|theta|phi|psi|omega|Rightarrow|Leftarrow|text|mathrm|mathbf|cos|sin|tan|log|ln|exp|boxed|begin|end|left|right|cdot|times|div|pm|mp|leq|geq|neq|approx|equiv|partial|nabla|vec|hat|bar|dot|ddot|overline|underline)/;

function preprocessLaTeX(md) {
  if (!md) return md;

  // ── Pass 0: Force line breaks before block-level markdown elements ──
  // LLMs sometimes emit "--- ## Heading" or "text - **bullet**" on one line.
  md = md
    .replace(/ ---/g, "\n\n---")                    // " ---" → newline + ---
    .replace(/--- (#)/g, "---\n\n$1")               // "--- #" → --- newline #
    .replace(/ (#{1,6} )/g, "\n\n$1")               // " ## Heading" → newline ## Heading
    .replace(/ (\*\*Answer:)/g, "\n\n$1")            // " **Answer:" → newline **Answer:
    .replace(/ - \*\*/g, "\n- **")                   // " - **bold**" → newline bullet
    .replace(/ - \$/g, "\n- $");                     // " - $math$" → newline bullet

  const lines = md.split("\n");
  const result = [];

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const trimmed = line.trim();

    // Skip empty or already-delimited lines
    if (!trimmed) { result.push(line); continue; }
    if (/^\$\$/.test(trimmed) || /\$\$$/.test(trimmed)) { result.push(line); continue; }
    if (/^[#>|\-*`]/.test(trimmed)) { result.push(line); continue; }

    // Line has LaTeX commands but NO dollar signs at all → wrap in $$
    if (LATEX_CMD.test(trimmed) && !trimmed.includes("$")) {
      result.push(`$$${trimmed}$$`);
      continue;
    }

    // Fix broken inline patterns: "text $expr$ more text" is fine,
    // but "text \frac{a}{b} more" with no $ is broken.
    // If line has SOME $ but also bare LaTeX outside them, try to fix.
    if (LATEX_CMD.test(trimmed)) {
      // Replace sequences of bare LaTeX between non-$ regions
      const fixed = trimmed.replace(
        /(?<![\$])\\(?:frac|sqrt|sum|prod|int|vec|overrightarrow|text|mathrm)\{[^}]*\}(?:\{[^}]*\})?(?:\^\{[^}]*\})?(?:_\{[^}]*\})?[^$\n]*/g,
        (match) => {
          // Only wrap if not already inside $ delimiters
          return `$${match}$`;
        }
      );
      result.push(fixed);
      continue;
    }

    result.push(line);
  }

  return result.join("\n");
}

export default function Markdown({ children }) {
  return (
    <ReactMarkdown
      remarkPlugins={[remarkMath, remarkGfm]}
      rehypePlugins={[rehypeKatex]}
    >
      {preprocessLaTeX(children)}
    </ReactMarkdown>
  );
}

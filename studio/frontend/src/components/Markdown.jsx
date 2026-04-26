import ReactMarkdown from "react-markdown";
import remarkMath from "remark-math";
import remarkGfm from "remark-gfm";
import rehypeKatex from "rehype-katex";

// Detect bare LaTeX lines (not wrapped in $/$$ delimiters) and wrap them.
const LATEX_CMD = /\\(?:frac|sqrt|sum|prod|int|lim|infty|alpha|beta|gamma|delta|theta|phi|psi|omega|Rightarrow|Leftarrow|text|mathrm|mathbf|cos|sin|tan|log|ln|exp|boxed|begin|end|left|right|cdot|times|div|pm|mp|leq|geq|neq|approx|equiv|partial|nabla|vec|hat|bar|dot|ddot|overline|underline)/;

// Matches bare math expressions: things like v_0^2, KE_B, \frac{a}{b}, 2mgl, etc.
// These are math-like tokens that appear without $ delimiters.
const BARE_MATH_LINE = /^[\s]*[A-Za-z0-9\\{}^_=+\-*/().,:;'"\s⇒→≥≤±∓×÷]+[\s]*$/;
const HAS_MATH_CHARS = /[_^{}\\]|⇒|→/;
const PURE_MATH_EXPR = /^[A-Za-z0-9_^{}()=+\-*/.,\s\\]+$/;

// Detect stacked single-symbol lines that LLMs produce when they break
// an expression across many lines (e.g. "m\nv\n2\n=\n5\ng\nl").
// We collapse runs of short (≤3 char) lines that look like math fragments.
function collapseStackedMath(lines) {
  const out = [];
  let i = 0;
  while (i < lines.length) {
    const t = lines[i].trim();
    // Start of a potential stacked run: short line that is a letter, digit,
    // operator, subscript marker, or Greek-looking word
    if (
      t.length <= 4 &&
      t.length >= 1 &&
      /^[A-Za-z0-9=+\-*/^_().,{}⇒→]+$/.test(t) &&
      // peek: next line is also short
      i + 1 < lines.length &&
      lines[i + 1].trim().length <= 4 &&
      lines[i + 1].trim().length >= 1
    ) {
      // Collect the full run
      const run = [t];
      let j = i + 1;
      while (
        j < lines.length &&
        lines[j].trim().length <= 4 &&
        lines[j].trim().length >= 1 &&
        /^[A-Za-z0-9=+\-*/^_().,{}⇒→]+$/.test(lines[j].trim())
      ) {
        run.push(lines[j].trim());
        j++;
      }
      if (run.length >= 3) {
        // This is a stacked expression — collapse and wrap
        const collapsed = run.join("");
        out.push(`$$${collapsed}$$`);
        i = j;
        continue;
      }
    }
    out.push(lines[i]);
    i++;
  }
  return out;
}

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

  // ── Pass 0.5: Collapse stacked single-symbol lines into expressions ──
  let lines = md.split("\n");
  lines = collapseStackedMath(lines);

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

    // Line looks like a bare math expression (has ^, _, {, }, \, ⇒)
    // and has NO dollar signs and NO plain English words (≥4 letter words)
    if (
      !trimmed.includes("$") &&
      HAS_MATH_CHARS.test(trimmed) &&
      PURE_MATH_EXPR.test(trimmed) &&
      !/[a-zA-Z]{4,}/.test(trimmed.replace(/\\[a-zA-Z]+/g, "")) // ignore LaTeX commands
    ) {
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
      rehypePlugins={[[rehypeKatex, { throwOnError: false, errorColor: "#cc6666" }]]}
    >
      {preprocessLaTeX(children)}
    </ReactMarkdown>
  );
}

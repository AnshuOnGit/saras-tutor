package studio

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"net/http"
	"saras-tutor/internal/logger"
	"strings"
	"time"

	"saras-tutor/internal/llm"

	"golang.org/x/image/draw"
)

// ─── Image Resize ─────────────────────────────────────────────────────

const maxImageDimension = 1568

// resizeImage decodes an image, down-scales if either dimension exceeds
// maxImageDimension, then re-encodes. Returns original bytes unchanged if
// already within limits.
func resizeImage(raw []byte, inputMime string) ([]byte, string, error) {
	reader := bytes.NewReader(raw)
	src, format, err := image.Decode(reader)
	if err != nil {
		return nil, "", fmt.Errorf("decode image: %w", err)
	}

	bounds := src.Bounds()
	origW, origH := bounds.Dx(), bounds.Dy()

	if origW <= maxImageDimension && origH <= maxImageDimension {
		return raw, inputMime, nil
	}

	scale := math.Min(
		float64(maxImageDimension)/float64(origW),
		float64(maxImageDimension)/float64(origH),
	)
	newW := int(math.Round(float64(origW) * scale))
	newH := int(math.Round(float64(origH) * scale))

	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, bounds, draw.Over, nil)

	var buf bytes.Buffer
	outMime := inputMime

	switch format {
	case "png":
		err = png.Encode(&buf, dst)
		outMime = "image/png"
	default:
		err = jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85})
		outMime = "image/jpeg"
	}
	if err != nil {
		return nil, "", fmt.Errorf("encode resized image: %w", err)
	}

	logger.Info().Str("orig", fmt.Sprintf("%dx%d", origW, origH)).
		Str("new", fmt.Sprintf("%dx%d", newW, newH)).
		Int("original_bytes", len(raw)).Int("resized_bytes", buf.Len()).
		Msg("resize: complete")

	return buf.Bytes(), outMime, nil
}

// ─── Vision Extraction ────────────────────────────────────────────────

const imageExtractionPrompt = `You are a VERBATIM TEXT TRANSCRIPTION system. Your ONLY job is to convert the image into text. You are NOT a tutor, solver, or assistant.

CRITICAL: You must NEVER solve, answer, explain, interpret, or reason about the content. You are a camera-to-text converter. NOTHING MORE.

RESPOND WITH ONLY THIS JSON (no code fences, no extra text):
{"confidence": 0.0-1.0, "content": "<markdown here>"}

TRANSCRIPTION RULES:
1. Reproduce the FULL text word-for-word EXACTLY as it appears in the image. Do NOT summarise, paraphrase, or add ANY of your own words.
2. Reproduce ALL answer options exactly: (A), (B), (C), (D) or (1), (2), (3), (4).
3. For math use KaTeX-safe LaTeX ONLY:
   - Inline: single $ delimiters — $F = ma$
   - Display: double $$ on own line — $$\frac{1}{2}mv^2$$
   - NEVER use \( \), \[ \], \begin{align}, \begin{equation}, \begin{cases}, \begin{array}, or any environment block.
   - Use ONLY standard commands: \frac{}{}, \sqrt{}, ^{}, _{}, \vec{}, \hat{}, \overrightarrow{}, \int, \sum, \prod, \sin, \cos, \tan, \log, \ln, \lim, \text{}.
   - Balance ALL braces {} and parentheses ().
   - Prefer ASCII operators (- not Unicode −, >= not ≥) unless inside $ delimiters.
   - For piecewise expressions, use separate $$ per branch in a bullet list.
   - EVERY mathematical expression MUST be fully enclosed in a SINGLE $ or $$ pair. NEVER split one expression across multiple $ pairs or leave partial math outside delimiters.
   - WRONG: f(x) = 7 tan⁸x + 7 tan⁶x    (Unicode superscripts, no LaTeX)
   - WRONG: f(x)=7tan 8 x+7tan 6 x         (exponents rendered as separate text)
   - WRONG: $f(x) = 7\tan^8 x$ + $7\tan^6 x$  (one expression split across multiple $ pairs)
   - CORRECT: $f(x) = 7\tan^8 x + 7\tan^6 x - 3\tan^4 x - 3\tan^2 x$
   - WRONG: $\mathrm{I}_1 = \int_0^{\pi/4} f(x) \mathrm{d} x$ and $\mathrm{I}_2 = \int_0^{\pi/4} x f(x) $\mathrm{d} x   (unbalanced $, \mathrm outside delimiters)
   - CORRECT: $I_1 = \int_0^{\pi/4} f(x)\,dx$ and $I_2 = \int_0^{\pi/4} x\,f(x)\,dx$
4. NEVER nest dollar signs, place math inside backtick code blocks, or use \tag/\label/\newcommand.
5. NEVER leave Unicode math symbols (⁰¹²³⁴⁵⁶⁷⁸⁹, ∫, ∑, √, etc.) outside $ delimiters — convert them to LaTeX.
6. For diagrams/figures: describe under "## Diagram" with all labels, vertices, segments, angles, and measurements visible.
7. For tables: use Markdown tables.
8. Keep output concise — no preamble like "The problem statement is" or "The extracted content is".

ABSOLUTE PROHIBITIONS — violating ANY of these makes your output INVALID:
❌ NEVER write steps, solutions, derivations, or working.
❌ NEVER write "Step 1", "Step 2", "Solution", "Answer", "Therefore", "We can see", "Let us", "To find", "We need to", "The final answer is".
❌ NEVER compute, calculate, or derive anything.
❌ NEVER write meta-notes like "No diagram is provided" or "The question is incomplete".
❌ If there is no diagram in the image, simply OMIT the "## Diagram" section.
✅ Output ONLY what literally appears in the image, transcribed faithfully. NOTHING ELSE.

DOUBLE-CHECK PASS — re-examine the image carefully:
- Every variable: check for a coefficient (2α, 3β), a preceding fraction (½α → $\frac{1}{2}\alpha$), or sign (+/−).
- Every \frac: verify numerator and denominator are complete (e.g. $\frac{mv^2}{2}$ not $\frac{mv}{2}$).
- Every superscript: distinguish x², x³, x⁻¹, x^{1/2}.
- Every subscript: distinguish S₁ vs S, v₀ vs v.
- Every option (A)–(D): capture the full expression including trailing terms and units.
- Vectors: use $\vec{SP}$ or $\overrightarrow{SP}$ as shown in the image.
Apply any corrections directly. Do NOT add commentary about corrections.

REMEMBER: You are a TRANSCRIPTION tool. If your output contains ANY solving, reasoning, or explanation, it is WRONG.`

type extractionResponse struct {
	Confidence float64 `json:"confidence"`
	Content    string  `json:"content"`
}

// httpFetchClient is used to download remote image URLs.
var httpFetchClient = &http.Client{
	Timeout: 20 * time.Second,
}

// fetchImageAsDataURI normalises an image reference so it can be handed to
// any NVIDIA NIM vision model:
//   - data: URIs returned verbatim
//   - http/https URLs fetched, base64-encoded, wrapped as data: URI
func fetchImageAsDataURI(ctx context.Context, url string) (string, error) {
	if url == "" {
		return "", fmt.Errorf("empty image url")
	}
	if strings.HasPrefix(url, "data:") {
		return url, nil
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return "", fmt.Errorf("unsupported image url scheme: %s", truncate(url, 40))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build fetch request: %w", err)
	}
	resp, err := httpFetchClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch image: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("fetch image: status %d", resp.StatusCode)
	}

	const maxImageBytes = 20 * 1024 * 1024
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return "", fmt.Errorf("read image body: %w", err)
	}
	if len(body) > maxImageBytes {
		return "", fmt.Errorf("image too large (>%d bytes)", maxImageBytes)
	}

	mime := resp.Header.Get("Content-Type")
	if mime == "" || !strings.HasPrefix(mime, "image/") {
		sniff := body
		if len(sniff) > 512 {
			sniff = sniff[:512]
		}
		mime = http.DetectContentType(sniff)
	}
	if !strings.HasPrefix(mime, "image/") {
		return "", fmt.Errorf("resource is not an image (content-type=%s)", mime)
	}

	b64 := base64.StdEncoding.EncodeToString(body)
	return fmt.Sprintf("data:%s;base64,%s", mime, b64), nil
}

// extractTextFromImage calls a vision LLM to extract text from an image URL.
// Returns the extracted markdown text.
func extractTextFromImage(ctx context.Context, cfg extractConfig, imageURL string) (string, error) {
	// Normalise to data URI
	inlineURL, err := fetchImageAsDataURI(ctx, imageURL)
	if err != nil {
		return "", fmt.Errorf("fetch image: %w", err)
	}

	visionClient := llm.NewClient(cfg.apiKey, cfg.modelID, cfg.baseURL, cfg.userID)
	visionClient.MaxTokens = 4096

	messages := []llm.ChatMessage{
		{Role: "system", Content: "You are a verbatim image-to-text transcription tool. You ONLY transcribe text from images. You NEVER solve, explain, interpret, or reason about the content. You NEVER produce steps, solutions, or answers. Output exactly what appears in the image, nothing more."},
		{Role: "user", Content: []llm.ContentPart{
			{Type: "text", Text: imageExtractionPrompt},
			{Type: "image_url", ImageURL: &llm.ImageURL{URL: inlineURL, Detail: "high"}},
		}},
	}

	llmStart := time.Now()
	raw, err := visionClient.Complete(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("vision LLM call failed: %w", err)
	}
	logger.Info().Str("model", raw.Model).
		Int64("elapsed_ms", time.Since(llmStart).Milliseconds()).
		Int("tokens", raw.Usage.TotalTokens).
		Msg("extraction: LLM responded")

	extracted := strings.TrimSpace(raw.Content)

	// Strip code fences
	if strings.HasPrefix(extracted, "```") {
		lines := strings.Split(extracted, "\n")
		if len(lines) >= 3 {
			extracted = strings.Join(lines[1:len(lines)-1], "\n")
			extracted = strings.TrimSpace(extracted)
		}
	}

	// Sanitize and parse JSON
	extracted = extractJSONObject(extracted)

	var resp extractionResponse
	if err := json.Unmarshal([]byte(extracted), &resp); err != nil {
		logger.Warn().Err(err).Msg("extraction: JSON parse failed, using raw text")
		return extracted, nil
	}

	if resp.Content != "" {
		resp.Content = fixLaTeXInMarkdown(resp.Content)
	}

	content := strings.TrimSpace(resp.Content)
	if content == "" {
		return "", fmt.Errorf("extraction returned no text")
	}
	return content, nil
}

type extractConfig struct {
	apiKey  string
	modelID string
	baseURL string
	userID  string
}

// ─── JSON / LaTeX Utilities ───────────────────────────────────────────

// fixLaTeXInMarkdown repairs LaTeX commands whose leading backslash was
// converted to a control character by json.Unmarshal.
func fixLaTeXInMarkdown(s string) string {
	// Phase 1: fix common escape sequence issues
	var b strings.Builder
	b.Grow(len(s))
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		switch {
		case ch == '\x0c':
			b.WriteString("\\f")
		case ch == '\x08':
			b.WriteString("\\b")
		case ch == '\r' && i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z':
			b.WriteString("\\r")
		case ch == '\t' && i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z':
			b.WriteString("\\t")
		default:
			b.WriteRune(ch)
		}
	}
	s = b.String()

	// Phase 2: replace Unicode superscript/subscript digits outside $ delimiters
	s = fixUnicodeMathOutsideDollars(s)

	// Phase 3: balance unmatched dollar signs
	s = balanceDollarSigns(s)

	return s
}

// unicodeSuperscripts maps Unicode superscript chars to their LaTeX equivalent.
var unicodeSuperscripts = map[rune]string{
	'⁰': "^{0}", '¹': "^{1}", '²': "^{2}", '³': "^{3}", '⁴': "^{4}",
	'⁵': "^{5}", '⁶': "^{6}", '⁷': "^{7}", '⁸': "^{8}", '⁹': "^{9}",
	'⁺': "^{+}", '⁻': "^{-}", '⁼': "^{=}", 'ⁿ': "^{n}", 'ⁱ': "^{i}",
}

var unicodeSubscripts = map[rune]string{
	'₀': "_{0}", '₁': "_{1}", '₂': "_{2}", '₃': "_{3}", '₄': "_{4}",
	'₅': "_{5}", '₆': "_{6}", '₇': "_{7}", '₈': "_{8}", '₉': "_{9}",
	'₊': "_{+}", '₋': "_{-}", '₌': "_{=}", 'ₙ': "_{n}", 'ₓ': "_{x}",
}

var unicodeMathSymbols = map[rune]string{
	'∫': "\\int ", '∑': "\\sum ", '∏': "\\prod ", '√': "\\sqrt ",
	'∞': "\\infty ", '≤': "\\leq ", '≥': "\\geq ", '≠': "\\neq ",
	'±': "\\pm ", '∓': "\\mp ", '×': "\\times ", '÷': "\\div ",
	'→': "\\to ", '←': "\\leftarrow ", '↔': "\\leftrightarrow ",
	'α': "\\alpha ", 'β': "\\beta ", 'γ': "\\gamma ", 'δ': "\\delta ",
	'θ': "\\theta ", 'λ': "\\lambda ", 'μ': "\\mu ", 'π': "\\pi ",
	'σ': "\\sigma ", 'φ': "\\phi ", 'ω': "\\omega ", 'ε': "\\epsilon ",
	'Δ': "\\Delta ", 'Σ': "\\Sigma ", 'Π': "\\Pi ", 'Ω': "\\Omega ",
	'−': "-",
}

// fixUnicodeMathOutsideDollars replaces Unicode math characters that appear
// outside of $ delimiters with their LaTeX equivalents wrapped in $.
func fixUnicodeMathOutsideDollars(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 128)
	inDollar := false
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]

		// Track $ state
		if ch == '$' {
			b.WriteRune(ch)
			inDollar = !inDollar
			// Handle $$
			if i+1 < len(runes) && runes[i+1] == '$' {
				b.WriteRune('$')
				i++
			}
			continue
		}

		if inDollar {
			b.WriteRune(ch)
			continue
		}

		// Outside $ — replace Unicode math chars
		if rep, ok := unicodeSuperscripts[ch]; ok {
			b.WriteString(rep)
		} else if rep, ok := unicodeSubscripts[ch]; ok {
			b.WriteString(rep)
		} else if rep, ok := unicodeMathSymbols[ch]; ok {
			b.WriteString(rep)
		} else {
			b.WriteRune(ch)
		}
	}
	return b.String()
}

// balanceDollarSigns ensures every $ has a closing pair on the same line.
func balanceDollarSigns(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		count := 0
		for _, ch := range line {
			if ch == '$' {
				count++
			}
		}
		// Odd number of $ means one is unmatched — append a closing $
		if count%2 != 0 {
			lines[i] = line + "$"
		}
	}
	return strings.Join(lines, "\n")
}

// extractJSONObject finds the first JSON object in raw text and sanitizes
// invalid escape sequences.
func extractJSONObject(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start >= 0 && end > start {
		raw = strings.TrimSpace(raw[start : end+1])
	}
	return sanitizeJSONString(raw)
}

func sanitizeJSONString(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 64)

	inString := false
	runes := []rune(s)

	for i := 0; i < len(runes); i++ {
		ch := runes[i]

		if !inString {
			if ch == '"' {
				inString = true
			}
			b.WriteRune(ch)
			continue
		}

		switch ch {
		case '"':
			inString = false
			b.WriteRune(ch)
		case '\\':
			if i+1 >= len(runes) {
				b.WriteString("\\\\")
				continue
			}
			next := runes[i+1]
			switch next {
			case '"', '\\', '/':
				b.WriteRune(ch)
				b.WriteRune(next)
				i++
			case 'b', 'f', 'n', 'r', 't':
				isLaTeX := false
				if i+2 < len(runes) {
					after := runes[i+2]
					if after >= 'a' && after <= 'z' {
						isLaTeX = true
					}
				}
				if isLaTeX {
					b.WriteString("\\\\")
					b.WriteRune(next)
					i++
				} else {
					b.WriteRune(ch)
					b.WriteRune(next)
					i++
				}
			case 'u':
				if i+5 < len(runes) {
					hex := string(runes[i+2 : i+6])
					valid := true
					for _, h := range hex {
						if !((h >= '0' && h <= '9') || (h >= 'a' && h <= 'f') || (h >= 'A' && h <= 'F')) {
							valid = false
							break
						}
					}
					if valid {
						b.WriteRune(ch)
						b.WriteRune(next)
						i++
						continue
					}
				}
				b.WriteString("\\\\u")
				i++
			default:
				b.WriteString("\\\\")
				b.WriteRune(next)
				i++
			}
		case '\n':
			b.WriteString("\\n")
		case '\r':
			b.WriteString("\\r")
		case '\t':
			b.WriteString("\\t")
		default:
			b.WriteRune(ch)
		}
	}

	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

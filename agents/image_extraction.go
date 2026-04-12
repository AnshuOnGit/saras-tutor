package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"saras-tutor/a2a"
	"saras-tutor/db"
	"saras-tutor/llm"
)

// ImageExtractionAgent uses a vision-capable LLM to extract question text
// from an image (photo of a textbook page, whiteboard, etc.).
type ImageExtractionAgent struct {
	llmClient *llm.Client
	store     *db.Store
}

// NewImageExtractionAgent creates the agent with a vision-capable LLM client.
// It sets MaxTokens = 4096 on the vision client to avoid truncation on complex diagrams.
func NewImageExtractionAgent(llmClient *llm.Client, store *db.Store) *ImageExtractionAgent {
	llmClient.MaxTokens = 4096
	return &ImageExtractionAgent{llmClient: llmClient, store: store}
}

// Card returns agent metadata.
func (a *ImageExtractionAgent) Card() a2a.AgentCard {
	return a2a.AgentCard{
		ID:          "image_extraction",
		Name:        "Image Extraction Agent",
		Description: "Extracts question text from images using a vision model.",
		Skills:      []string{"ocr", "image_to_text"},
	}
}

/*const imageExtractionPrompt = "You are an expert academic content extraction assistant with deep knowledge of mathematics, physics, chemistry, and engineering.\n\n" +
"TASK: Analyse the provided image and extract ALL content — both text AND graphical elements — as well-formatted Markdown.\n\n" +
"CONTENT TYPES YOU MUST HANDLE:\n" +
"• Text & equations — OCR the text faithfully.\n" +
"• Graphs / plots — describe axes, labels, curves, key points, intercepts, asymptotes. Reproduce the function if identifiable.\n" +
"• Geometric figures — describe the shape, label vertices/sides/angles, note given measurements.\n" +
"• Circuit diagrams — list components (resistors, capacitors, voltage sources) and their connections. Represent as a Mermaid flowchart.\n" +
"• Free-body / force diagrams — list forces, directions, and the body they act on.\n" +
"• Flowcharts / block diagrams — represent as a ```mermaid block.\n" +
"• Tables / matrices / matching columns — use Markdown tables or LaTeX matrices.\n\n" +
"FORMATTING RULES — you MUST follow these:\n" +
"1. Reproduce the full question/problem text faithfully — do NOT solve it.\n" +
"2. For ALL mathematical expressions use LaTeX with dollar-sign delimiters:\n" +
"   • Inline: $...$ (e.g. $x^2 + 3x + 2 = 0$)\n" +
"   • Display: $$...$$ on its own line, e.g. $$\\int_0^1 \\frac{dx}{\\sqrt{1-x^2}}$$\n" +
"3. Use proper LaTeX: \\frac, \\sqrt, \\int, \\sum, \\lim, \\vec, \\hat, \\overrightarrow, \\pi, \\theta, \\Omega, etc.\n" +
"4. For integrals include limits: $\\int_{a}^{b}$. For fractions: $\\frac{num}{den}$.\n" +
"5. Preserve structure: headings (##), bullet/numbered lists, or tables as appropriate.\n" +
"6. For diagrams that can be represented structurally, use a ```mermaid fenced block.\n" +
"7. For diagrams that CANNOT be represented in Mermaid (complex graphs, plots, geometric figures), provide a detailed textual description under a heading **## Diagram Description** including:\n" +
"   - Type of diagram (e.g. \"velocity-time graph\", \"right triangle\", \"RC circuit\")\n" +
"   - All labels, values, directions, and units visible\n" +
"   - Relationships or connections between elements\n" +
"8. For matrices: $$\\begin{pmatrix} a & b \\\\ c & d \\end{pmatrix}$$\n" +
"9. NEVER use \\\\( \\\\) or \\\\[ \\\\] delimiters — always use $ and $$ only.\n" +
"10. Do NOT solve or explain — only extract and format.\n\n" +
"RESPONSE FORMAT — you MUST respond with ONLY a JSON object (no code fences, no extra text):\n" +
"{\"confidence\": <0.0-1.0>, \"content\": \"<your extracted markdown here>\"}\n" +
"- confidence: how certain you are that you extracted ALL content correctly (1.0 = fully certain, lower if image is unclear or you may have missed something).\n" +
"- content: your full extracted markdown (escape inner quotes as needed)."*/

const imageExtractionPrompt = `
You are an academic content extraction system. Extract content from the image WITHOUT solving.

Return ONLY valid JSON (no markdown fences, no extra text). Do not invent unreadable values; use null and explain in "issues".

GOALS:
1) Faithfully transcribe the problem statement and options.
2) Extract diagrams/graphs into structured data usable for downstream solving.

OUTPUT JSON SCHEMA:
{
  "confidence": 0.0-1.0,
  "issues": [ { "type": "unreadable|cropped|ambiguous|low_contrast|overlap", "detail": "..." } ],
  "text_markdown": "Problem text and options in Markdown with LaTeX ($ and $$ only). No solving.",
  "regions": [
    { "id": "r1", "kind": "question_text|options|diagram|table|other", "bbox_norm": [x1,y1,x2,y2] }
  ],
  "diagrams": [
    {
      "id": "d1",
      "type": "graph|geometry|circuit|free_body|reaction_scheme|flowchart|table|unknown",
      "title": "string or null",
      "bbox_norm": [x1,y1,x2,y2],
      "labels": [
        { "text": "string", "bbox_norm": [x1,y1,x2,y2], "confidence": 0.0-1.0 }
      ],

      "graph": {
        "x_axis": { "label": "string|null", "unit": "string|null", "scale": "linear|log|unknown" },
        "y_axis": { "label": "string|null", "unit": "string|null", "scale": "linear|log|unknown" },
        "ticks": {
          "x": [ { "value": "string|null", "pos_norm": 0.0-1.0 } ],
          "y": [ { "value": "string|null", "pos_norm": 0.0-1.0 } ]
        },
        "curves": [
          {
            "name": "string|null",
            "style": "solid|dashed|points|unknown",
            "points_norm": [ [x,y], [x,y] ],
            "key_features": {
              "intercepts": [ { "x": "string|null", "y": "string|null" } ],
              "asymptotes": [ "string" ],
              "turning_points": [ { "x": "string|null", "y": "string|null" } ]
            },
            "function_hypothesis": { "latex": "string|null", "confidence": 0.0-1.0 }
          }
        ]
      },

      "geometry": {
        "points": [ { "name": "A", "pos_norm": [x,y] } ],
        "segments": [ { "from": "A", "to": "B", "length": "string|null" } ],
        "angles": [ { "at": "A", "between": ["AB","AC"], "value": "string|null" } ],
        "constraints": [ "string" ]
      },

      "circuit": {
        "nodes": [ "n1", "n2" ],
        "components": [
          { "ref": "R1", "type": "resistor|capacitor|inductor|source|diode|unknown", "value": "string|null", "pins": { "1": "n1", "2": "n2" } }
        ],
        "notes": [ "string" ]
      },

      "free_body": {
        "body": "string|null",
        "forces": [ { "label": "string|null", "direction": "up|down|left|right|angle|unknown", "angle_deg": "string|null" } ]
      },

      "flowchart_mermaid": "string|null"
    }
  ]
}

RULES:
- Do NOT solve. Only extract.
- For math use LaTeX with $...$ and $$...$$ only.
- If a diagram type is present but uncertain, set type="unknown" and add an issue.
- Include at least one diagram entry if any non-text figure is visible.
- If multiple diagrams exist, create multiple entries.
`

// ---------- Structured response types ----------

type extractionResponse struct {
	Confidence   float64             `json:"confidence"`
	Issues       []extractionIssue   `json:"issues"`
	TextMarkdown string              `json:"text_markdown"`
	Regions      []json.RawMessage   `json:"regions"`
	Diagrams     []extractionDiagram `json:"diagrams"`
}

type extractionIssue struct {
	Type   string `json:"type"`
	Detail string `json:"detail"`
}

type extractionDiagram struct {
	ID               string           `json:"id"`
	Type             string           `json:"type"`
	Title            string           `json:"title"`
	Graph            *diagramGraph    `json:"graph"`
	Geometry         *diagramGeometry `json:"geometry"`
	Circuit          *diagramCircuit  `json:"circuit"`
	FreeBody         *diagramFreeBody `json:"free_body"`
	FlowchartMermaid string           `json:"flowchart_mermaid"`
	Labels           []diagramLabel   `json:"labels"`
}

type diagramLabel struct {
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
}

type diagramGraph struct {
	XAxis  *axisInfo   `json:"x_axis"`
	YAxis  *axisInfo   `json:"y_axis"`
	Curves []curveInfo `json:"curves"`
}

type axisInfo struct {
	Label string `json:"label"`
	Unit  string `json:"unit"`
	Scale string `json:"scale"`
}

type curveInfo struct {
	Name               string          `json:"name"`
	Style              string          `json:"style"`
	KeyFeatures        *curveFeatures  `json:"key_features"`
	FunctionHypothesis *funcHypothesis `json:"function_hypothesis"`
}

type curveFeatures struct {
	Intercepts    []map[string]string `json:"intercepts"`
	Asymptotes    []string            `json:"asymptotes"`
	TurningPoints []map[string]string `json:"turning_points"`
}

type funcHypothesis struct {
	LaTeX      string  `json:"latex"`
	Confidence float64 `json:"confidence"`
}

type diagramGeometry struct {
	Points      []geoPoint   `json:"points"`
	Segments    []geoSegment `json:"segments"`
	Angles      []geoAngle   `json:"angles"`
	Constraints []string     `json:"constraints"`
}

type geoPoint struct {
	Name string `json:"name"`
}

type geoSegment struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Length string `json:"length"`
}

type geoAngle struct {
	At      string   `json:"at"`
	Between []string `json:"between"`
	Value   string   `json:"value"`
}

type diagramCircuit struct {
	Nodes      []string           `json:"nodes"`
	Components []circuitComponent `json:"components"`
	Notes      []string           `json:"notes"`
}

type circuitComponent struct {
	Ref   string            `json:"ref"`
	Type  string            `json:"type"`
	Value string            `json:"value"`
	Pins  map[string]string `json:"pins"`
}

type diagramFreeBody struct {
	Body   string    `json:"body"`
	Forces []fbForce `json:"forces"`
}

type fbForce struct {
	Label     string `json:"label"`
	Direction string `json:"direction"`
	AngleDeg  string `json:"angle_deg"`
}

// ---------- Convert structured extraction to presentable markdown ----------

func buildMarkdownFromExtraction(resp *extractionResponse) string {
	var sb strings.Builder

	// Text portion
	if resp.TextMarkdown != "" {
		sb.WriteString(resp.TextMarkdown)
		sb.WriteString("\n\n")
	}

	// Issues / warnings
	if len(resp.Issues) > 0 {
		sb.WriteString("## ⚠ Extraction Notes\n\n")
		for _, iss := range resp.Issues {
			sb.WriteString(fmt.Sprintf("- **%s**: %s\n", iss.Type, iss.Detail))
		}
		sb.WriteString("\n")
	}

	// Diagrams
	for _, d := range resp.Diagrams {
		title := d.Title
		if title == "" {
			title = fmt.Sprintf("Diagram %s (%s)", d.ID, d.Type)
		}
		sb.WriteString(fmt.Sprintf("## %s\n\n", title))

		// Labels
		if len(d.Labels) > 0 {
			sb.WriteString("**Labels:** ")
			var lbls []string
			for _, l := range d.Labels {
				lbls = append(lbls, l.Text)
			}
			sb.WriteString(strings.Join(lbls, ", "))
			sb.WriteString("\n\n")
		}

		// Graph
		if d.Graph != nil {
			g := d.Graph
			if g.XAxis != nil {
				label := g.XAxis.Label
				if g.XAxis.Unit != "" {
					label += " (" + g.XAxis.Unit + ")"
				}
				sb.WriteString(fmt.Sprintf("- **X-axis**: %s", label))
				if g.XAxis.Scale != "" && g.XAxis.Scale != "linear" {
					sb.WriteString(fmt.Sprintf(" [%s]", g.XAxis.Scale))
				}
				sb.WriteString("\n")
			}
			if g.YAxis != nil {
				label := g.YAxis.Label
				if g.YAxis.Unit != "" {
					label += " (" + g.YAxis.Unit + ")"
				}
				sb.WriteString(fmt.Sprintf("- **Y-axis**: %s", label))
				if g.YAxis.Scale != "" && g.YAxis.Scale != "linear" {
					sb.WriteString(fmt.Sprintf(" [%s]", g.YAxis.Scale))
				}
				sb.WriteString("\n")
			}
			for i, c := range g.Curves {
				name := c.Name
				if name == "" {
					name = fmt.Sprintf("Curve %d", i+1)
				}
				sb.WriteString(fmt.Sprintf("\n### %s", name))
				if c.Style != "" {
					sb.WriteString(fmt.Sprintf(" (%s)", c.Style))
				}
				sb.WriteString("\n")
				if c.FunctionHypothesis != nil && c.FunctionHypothesis.LaTeX != "" {
					sb.WriteString(fmt.Sprintf("$$%s$$\n", c.FunctionHypothesis.LaTeX))
				}
				if c.KeyFeatures != nil {
					if len(c.KeyFeatures.Intercepts) > 0 {
						sb.WriteString("- **Intercepts**: ")
						var parts []string
						for _, ic := range c.KeyFeatures.Intercepts {
							if ic["x"] != "" && ic["y"] != "" {
								parts = append(parts, fmt.Sprintf("(%s, %s)", ic["x"], ic["y"]))
							} else if ic["x"] != "" {
								parts = append(parts, fmt.Sprintf("x = %s", ic["x"]))
							} else if ic["y"] != "" {
								parts = append(parts, fmt.Sprintf("y = %s", ic["y"]))
							}
						}
						sb.WriteString(strings.Join(parts, ", "))
						sb.WriteString("\n")
					}
					if len(c.KeyFeatures.Asymptotes) > 0 {
						sb.WriteString("- **Asymptotes**: " + strings.Join(c.KeyFeatures.Asymptotes, ", ") + "\n")
					}
					if len(c.KeyFeatures.TurningPoints) > 0 {
						sb.WriteString("- **Turning points**: ")
						var parts []string
						for _, tp := range c.KeyFeatures.TurningPoints {
							parts = append(parts, fmt.Sprintf("(%s, %s)", tp["x"], tp["y"]))
						}
						sb.WriteString(strings.Join(parts, ", "))
						sb.WriteString("\n")
					}
				}
			}
			sb.WriteString("\n")
		}

		// Geometry
		if d.Geometry != nil {
			geo := d.Geometry
			if len(geo.Points) > 0 {
				var names []string
				for _, p := range geo.Points {
					names = append(names, p.Name)
				}
				sb.WriteString("- **Points**: " + strings.Join(names, ", ") + "\n")
			}
			if len(geo.Segments) > 0 {
				for _, s := range geo.Segments {
					line := fmt.Sprintf("- Segment %s→%s", s.From, s.To)
					if s.Length != "" {
						line += fmt.Sprintf(" = %s", s.Length)
					}
					sb.WriteString(line + "\n")
				}
			}
			if len(geo.Angles) > 0 {
				for _, a := range geo.Angles {
					line := fmt.Sprintf("- Angle at %s", a.At)
					if a.Value != "" {
						line += fmt.Sprintf(" = %s", a.Value)
					}
					sb.WriteString(line + "\n")
				}
			}
			if len(geo.Constraints) > 0 {
				sb.WriteString("- **Constraints**: " + strings.Join(geo.Constraints, "; ") + "\n")
			}
			sb.WriteString("\n")
		}

		// Circuit
		if d.Circuit != nil {
			cir := d.Circuit
			if len(cir.Components) > 0 {
				sb.WriteString("| Ref | Type | Value | Pins |\n")
				sb.WriteString("|-----|------|-------|------|\n")
				for _, comp := range cir.Components {
					var pins []string
					for k, v := range comp.Pins {
						pins = append(pins, fmt.Sprintf("%s→%s", k, v))
					}
					sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n",
						comp.Ref, comp.Type, comp.Value, strings.Join(pins, ", ")))
				}
			}
			if len(cir.Notes) > 0 {
				for _, n := range cir.Notes {
					sb.WriteString(fmt.Sprintf("- %s\n", n))
				}
			}
			sb.WriteString("\n")
		}

		// Free-body diagram
		if d.FreeBody != nil {
			fb := d.FreeBody
			if fb.Body != "" {
				sb.WriteString(fmt.Sprintf("**Body**: %s\n\n", fb.Body))
			}
			if len(fb.Forces) > 0 {
				sb.WriteString("| Force | Direction | Angle |\n")
				sb.WriteString("|-------|-----------|-------|\n")
				for _, f := range fb.Forces {
					sb.WriteString(fmt.Sprintf("| %s | %s | %s |\n", f.Label, f.Direction, f.AngleDeg))
				}
			}
			sb.WriteString("\n")
		}

		// Mermaid flowchart
		if d.FlowchartMermaid != "" {
			sb.WriteString("```mermaid\n")
			sb.WriteString(d.FlowchartMermaid)
			sb.WriteString("\n```\n\n")
		}
	}

	return strings.TrimSpace(sb.String())
}

// fixLaTeXInMarkdown repairs LaTeX commands whose leading backslash was
// silently converted to a control character by json.Unmarshal.
//
// For example, if the LLM writes \frac in a JSON string without double-escaping,
// Go parses \f as form-feed (U+000C) + "rac" → this function restores it to \frac.
// Form-feed and backspace NEVER appear in valid Markdown, so replacing is safe.
// For tab and carriage-return we only replace when followed by a lowercase letter
// (heuristic: \theta, \text, \rho, \right etc.).
func fixLaTeXInMarkdown(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		switch {
		case ch == '\x0c': // form feed → \f  (\frac, \forall, \flat …)
			b.WriteString("\\f")
		case ch == '\x08': // backspace → \b  (\beta, \bar, \begin, \binom …)
			b.WriteString("\\b")
		case ch == '\r' && i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z':
			b.WriteString("\\r") // CR + letter → \rho, \right …
		case ch == '\t' && i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z':
			b.WriteString("\\t") // tab + letter → \theta, \text, \times …
		default:
			b.WriteRune(ch)
		}
	}
	return b.String()
}

// Handle processes an image extraction task synchronously.
func (a *ImageExtractionAgent) Handle(ctx context.Context, task *a2a.Task) (*a2a.Task, error) {
	// Build multi-modal message with image parts
	var contentParts []llm.ContentPart
	contentParts = append(contentParts, llm.ContentPart{Type: "text", Text: imageExtractionPrompt})

	for _, p := range task.Input.Parts {
		if p.Type == "image" && p.ImageURL != "" {
			contentParts = append(contentParts, llm.ContentPart{
				Type:     "image_url",
				ImageURL: &llm.ImageURL{URL: p.ImageURL, Detail: "high"},
			})
		}
	}

	messages := []llm.ChatMessage{
		{Role: "user", Content: contentParts},
	}

	raw, err := a.llmClient.Complete(ctx, messages)
	if err != nil {
		task.State = a2a.TaskStateFailed
		return task, fmt.Errorf("image extraction: %w", err)
	}

	// Strip code fences if present
	extracted := raw.Content
	extracted = strings.TrimSpace(extracted)
	if strings.HasPrefix(extracted, "```") {
		lines := strings.Split(extracted, "\n")
		if len(lines) >= 3 {
			extracted = strings.Join(lines[1:len(lines)-1], "\n")
			extracted = strings.TrimSpace(extracted)
		}
	}

	// Sanitize JSON — LLMs emit LaTeX backslash commands (\frac, \theta …)
	// without the double-escaping JSON requires, corrupting them into control
	// characters on json.Unmarshal.  extractJSONObject fixes this.
	extracted = extractJSONObject(extracted)

	// Parse structured JSON
	var resp extractionResponse
	if err := json.Unmarshal([]byte(extracted), &resp); err != nil {
		slog.Warn("image_extraction: JSON parse failed, using raw text", "error", err)
		// Fallback: use raw text directly
		task.State = a2a.TaskStateCompleted
		task.Output = &a2a.Message{
			Role:  "agent",
			Parts: []a2a.Part{a2a.TextPart(extracted)},
		}
		return task, nil
	}

	slog.Info("image_extraction result",
		"confidence", resp.Confidence, "issues", len(resp.Issues), "diagrams", len(resp.Diagrams))

	// Repair any LaTeX commands whose backslash was interpreted as a
	// JSON control character (e.g. form-feed from \frac, tab from \theta).
	if resp.TextMarkdown != "" {
		resp.TextMarkdown = fixLaTeXInMarkdown(resp.TextMarkdown)
	}

	// Check confidence threshold
	if resp.Confidence < 0.6 {
		task.State = a2a.TaskStateFailed
		return task, &llm.ErrLowConfidence{
			Got:       resp.Confidence,
			Threshold: llm.MinConfidence,
			Agent:     "image_extraction",
		}
	}

	// Build presentable markdown from structured data
	markdown := buildMarkdownFromExtraction(&resp)
	if markdown == "" {
		markdown = "(No content extracted from image)"
	}

	task.State = a2a.TaskStateCompleted
	task.Output = &a2a.Message{
		Role:  "agent",
		Parts: []a2a.Part{a2a.TextPart(markdown)},
	}
	return task, nil
}

// HandleStream delegates to Handle since extraction is a single-shot operation.
func (a *ImageExtractionAgent) HandleStream(ctx context.Context, task *a2a.Task, out chan<- a2a.StreamEvent) {
	out <- a2a.StreamEvent{Type: "status", State: a2a.TaskStateWorking}

	result, err := a.Handle(ctx, task)
	if err != nil {
		out <- a2a.StreamEvent{Type: "error", Error: err.Error()}
		return
	}

	if result.Output != nil {
		out <- a2a.StreamEvent{Type: "artifact", Message: result.Output}
	}
	out <- a2a.StreamEvent{Type: "status", State: a2a.TaskStateCompleted}
}

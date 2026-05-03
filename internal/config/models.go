package config

import "sort"

// ModelCategory groups models by their role in the pipeline.
type ModelCategory string

const (
	// CategoryVision is for image extraction (heavy VLM).
	CategoryVision ModelCategory = "Vision"

	// CategorySolverLevel1 — general / theory-heavy subjects (biology, chemistry, NCERT-level).
	CategorySolverLevel1 ModelCategory = "Solver:Level1"

	// CategorySolverLevel2 — math / physics reasoning (JEE Main – Advanced).
	CategorySolverLevel2 ModelCategory = "Solver:Level2"

	// CategorySolverLevel3 — olympiad / multi-step proofs (extreme).
	CategorySolverLevel3 ModelCategory = "Solver:Level3"

	// CategoryHintGenerator is for generating pedagogical hints at every difficulty.
	CategoryHintGenerator ModelCategory = "HintGenerator"

	// CategoryRouter is for intent detection, validation, parsing, verification — fast & cheap.
	CategoryRouter ModelCategory = "Router"
)

// AllCategories returns every category in display order (used by the UI picker).
func AllCategories() []ModelCategory {
	return []ModelCategory{
		CategoryVision,
		CategorySolverLevel1,
		CategorySolverLevel2,
		CategorySolverLevel3,
		CategoryHintGenerator,
		CategoryRouter,
	}
}

// IsSolverCategory returns true if the category is any Solver sub-category.
func IsSolverCategory(c ModelCategory) bool {
	return c == CategorySolverLevel1 || c == CategorySolverLevel2 || c == CategorySolverLevel3
}

// ModelExpert describes a single NVIDIA NIM model and when to use it.
type ModelExpert struct {
	// ID is the NVIDIA NIM model identifier, e.g. "meta/llama-3.2-90b-vision-instruct".
	ID string `json:"id"`

	// DisplayName is the human-friendly label shown in the frontend.
	DisplayName string `json:"display_name"`

	// Category indicates the model's role.
	Category ModelCategory `json:"category"`

	// Priority: lower number = higher priority. 1 is the default pick for the category.
	// The frontend lists models by ascending priority; ties are broken by registry order.
	Priority int `json:"priority"`

	// Notes is a short human-friendly hint about why/when to pick this model.
	Notes string `json:"notes,omitempty"`
}

// ModelRegistry is the global catalogue of available models grouped by category.
// Within a category, the model with the lowest Priority is the default.
// To switch defaults, just reorder priorities — no code changes needed.
var ModelRegistry = []ModelExpert{
	// ──────────────────────────────────────────────────────────────────
	// Vision — image extraction (heavy VLM)
	// ──────────────────────────────────────────────────────────────────
	{
		ID:          "meta/llama-3.2-90b-vision-instruct",
		DisplayName: "Llama 3.2 90B Vision",
		Category:    CategoryVision,
		Priority:    1,
		Notes:       "Strong OCR + diagram understanding. Default.",
	},
	{
		ID:          "meta/llama-4-maverick-17b-128e-instruct",
		DisplayName: "Llama 4 Maverick 17B",
		Category:    CategoryVision,
		Priority:    2,
		Notes:       "Newer multimodal; faster, good for handwritten scans.",
	},
	{
		ID:          "meta/llama-3.2-11b-vision-instruct",
		DisplayName: "Llama 3.2 11B Vision",
		Category:    CategoryVision,
		Priority:    3,
		Notes:       "Lightweight vision model — use when 90B is throttled.",
	},
	{
		ID:          "nvidia/llama-3.1-nemotron-nano-vl-8b-v1",
		DisplayName: "Nemotron Nano VL 8B",
		Category:    CategoryVision,
		Priority:    4,
		Notes:       "Tiny VLM for low-latency simple images.",
	},
	{
		ID:          "microsoft/phi-3.5-vision-instruct",
		DisplayName: "Phi-3.5 Vision",
		Category:    CategoryVision,
		Priority:    5,
		Notes:       "Compact Microsoft VLM.",
	},

	// ──────────────────────────────────────────────────────────────────
	// Solver:Level1 — theory / general (biology, chemistry, NCERT recall)
	// ──────────────────────────────────────────────────────────────────
	// NOTE: mistral-small-3.1-24b-instruct-2503 removed — EOL 2026-04-15.
	{
		ID:          "moonshotai/kimi-k2-thinking",
		DisplayName: "Kimi K2 Thinking",
		Category:    CategorySolverLevel1,
		Priority:    1,
		Notes:       "Moonshot reasoning model. Default.",
	},
	{
		ID:          "deepseek-ai/deepseek-v4-pro",
		DisplayName: "DeepSeek V4 Pro",
		Category:    CategorySolverLevel1,
		Priority:    2,
		Notes:       "Frontier-class DeepSeek reasoning.",
	},
	{
		ID:          "google/gemma-3-27b-it",
		DisplayName: "Gemma 3 27B",
		Category:    CategorySolverLevel1,
		Priority:    3,
		Notes:       "Strong at structured explanations.",
	},
	{
		ID:          "meta/llama-3.3-70b-instruct",
		DisplayName: "Llama 3.3 70B",
		Category:    CategorySolverLevel1,
		Priority:    4,
		Notes:       "Balanced general-purpose instruction model.",
	},
	{
		ID:          "mistralai/ministral-14b-instruct-2512",
		DisplayName: "Ministral 14B",
		Category:    CategorySolverLevel1,
		Priority:    5,
		Notes:       "Smaller & cheaper Mistral alternative.",
	},
	{
		ID:          "nvidia/llama-3.3-nemotron-super-49b-v1.5",
		DisplayName: "Nemotron Super 49B v1.5",
		Category:    CategorySolverLevel1,
		Priority:    6,
		Notes:       "Nemotron tuning on Llama 3.3.",
	},

	// ──────────────────────────────────────────────────────────────────
	// Solver:Level2 — math / physics reasoning (JEE Main – Advanced)
	// ──────────────────────────────────────────────────────────────────
	// NOTE: deepseek-r1-distill-qwen-32b/14b, mathstral-7b-v0.1 and qwen/qwq-32b
	// removed — all EOL 2026-04-15.
	{
		ID:          "moonshotai/kimi-k2-thinking",
		DisplayName: "Kimi K2 Thinking",
		Category:    CategorySolverLevel2,
		Priority:    1,
		Notes:       "Moonshot reasoning model. Default.",
	},
	{
		ID:          "deepseek-ai/deepseek-v4-pro",
		DisplayName: "DeepSeek V4 Pro",
		Category:    CategorySolverLevel2,
		Priority:    2,
		Notes:       "Frontier-class DeepSeek reasoning.",
	},
	{
		ID:          "qwen/qwen3-next-80b-a3b-thinking",
		DisplayName: "Qwen3-Next 80B Thinking",
		Category:    CategorySolverLevel2,
		Priority:    3,
		Notes:       "MoE reasoning model, long context.",
	},
	{
		ID:          "deepseek-ai/deepseek-v3.2",
		DisplayName: "DeepSeek V3.2",
		Category:    CategorySolverLevel2,
		Priority:    4,
		Notes:       "Non-reasoning but very capable.",
	},
	{
		ID:          "mistralai/magistral-small-2506",
		DisplayName: "Magistral Small",
		Category:    CategorySolverLevel2,
		Priority:    5,
		Notes:       "Mistral's reasoning model.",
	},

	// ──────────────────────────────────────────────────────────────────
	// Solver:Level3 — olympiad / multi-step proofs (extreme)
	// ──────────────────────────────────────────────────────────────────
	{
		ID:          "moonshotai/kimi-k2-thinking",
		DisplayName: "Kimi K2 Thinking",
		Category:    CategorySolverLevel3,
		Priority:    1,
		Notes:       "Moonshot reasoning model. Default.",
	},
	{
		ID:          "deepseek-ai/deepseek-v4-pro",
		DisplayName: "DeepSeek V4 Pro",
		Category:    CategorySolverLevel3,
		Priority:    2,
		Notes:       "Frontier-class DeepSeek reasoning.",
	},
	{
		ID:          "meta/llama-3.1-405b-instruct",
		DisplayName: "Llama 3.1 405B",
		Category:    CategorySolverLevel3,
		Priority:    3,
		Notes:       "Top-tier breadth.",
	},
	{
		ID:          "mistralai/mistral-large-3-675b-instruct-2512",
		DisplayName: "Mistral Large 3 675B",
		Category:    CategorySolverLevel3,
		Priority:    4,
		Notes:       "Frontier-class Mistral model.",
	},
	{
		ID:          "nvidia/llama-3.1-nemotron-ultra-253b-v1",
		DisplayName: "Nemotron Ultra 253B",
		Category:    CategorySolverLevel3,
		Priority:    5,
		Notes:       "NVIDIA's heavyweight reasoning model.",
	},
	{
		ID:          "qwen/qwen3.5-397b-a17b",
		DisplayName: "Qwen 3.5 397B",
		Category:    CategorySolverLevel3,
		Priority:    6,
		Notes:       "MoE heavyweight, strong at math.",
	},
	{
		ID:          "nvidia/nemotron-3-super-120b-a12b",
		DisplayName: "Nemotron 3 Super 120B",
		Category:    CategorySolverLevel3,
		Priority:    7,
		Notes:       "Alternative large MoE.",
	},
	{
		ID:          "openai/gpt-oss-120b",
		DisplayName: "GPT-OSS 120B",
		Category:    CategorySolverLevel3,
		Priority:    8,
		Notes:       "OpenAI open-weights heavyweight.",
	},

	// ──────────────────────────────────────────────────────────────────
	// HintGenerator — pedagogical hints (must NOT solve the problem)
	// Use instruction-following models. Reasoning models (R1, QwQ) dump
	// their chain-of-thought and give away the full solution.
	// NOTE: mistral-small-3.1-24b-instruct-2503 removed — EOL 2026-04-15.
	// ──────────────────────────────────────────────────────────────────
	{
		ID:          "google/gemma-3-27b-it",
		DisplayName: "Gemma 3 27B",
		Category:    CategoryHintGenerator,
		Priority:    1,
		Notes:       "Good pedagogical tone, respects 'do not solve'. Default.",
	},
	{
		ID:          "meta/llama-3.3-70b-instruct",
		DisplayName: "Llama 3.3 70B",
		Category:    CategoryHintGenerator,
		Priority:    2,
		Notes:       "Reliable instruction following.",
	},
	{
		ID:          "mistralai/ministral-14b-instruct-2512",
		DisplayName: "Ministral 14B",
		Category:    CategoryHintGenerator,
		Priority:    3,
		Notes:       "Cheaper hint generation.",
	},
	{
		ID:          "microsoft/phi-4-mini-instruct",
		DisplayName: "Phi-4 Mini",
		Category:    CategoryHintGenerator,
		Priority:    5,
		Notes:       "Small and fast hint model.",
	},

	// ──────────────────────────────────────────────────────────────────
	// Router — intent detection, validation, parsing, verification
	// Fast & cheap, JSON-friendly instruction models.
	// NOTE: mistral-small-3.1-24b-instruct-2503 removed — EOL 2026-04-15.
	// ──────────────────────────────────────────────────────────────────
	{
		ID:          "meta/llama-3.3-70b-instruct",
		DisplayName: "Llama 3.3 70B",
		Category:    CategoryRouter,
		Priority:    1,
		Notes:       "Strong JSON output + validation. Default.",
	},
	{
		ID:          "meta/llama-3.1-8b-instruct",
		DisplayName: "Llama 3.1 8B",
		Category:    CategoryRouter,
		Priority:    2,
		Notes:       "Very fast for simple classification.",
	},
	{
		ID:          "google/gemma-3-27b-it",
		DisplayName: "Gemma 3 27B",
		Category:    CategoryRouter,
		Priority:    3,
		Notes:       "Structured output alternative.",
	},
	{
		ID:          "google/gemma-3-4b-it",
		DisplayName: "Gemma 3 4B",
		Category:    CategoryRouter,
		Priority:    4,
		Notes:       "Tiny + cheap utility model.",
	},
	{
		ID:          "nvidia/llama-3.1-nemotron-nano-8b-v1",
		DisplayName: "Nemotron Nano 8B",
		Category:    CategoryRouter,
		Priority:    4,
		Notes:       "NVIDIA fine-tune of Llama 8B.",
	},
	{
		ID:          "microsoft/phi-4-mini-instruct",
		DisplayName: "Phi-4 Mini",
		Category:    CategoryRouter,
		Priority:    5,
		Notes:       "Microsoft's compact instruction model.",
	},
}

// ── Lookup helpers ───────────────────────────────────────────────────

// GetModelsByCategory returns all models in a category, sorted by priority
// ascending (highest-priority/default model first).
func GetModelsByCategory(category ModelCategory) []ModelExpert {
	var out []ModelExpert
	for _, m := range ModelRegistry {
		if m.Category == category {
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	return out
}

// GetDefault returns the highest-priority (lowest-Priority-number) model
// for a category, or nil if the category has no entries.
func GetDefault(category ModelCategory) *ModelExpert {
	var best *ModelExpert
	for i := range ModelRegistry {
		m := &ModelRegistry[i]
		if m.Category != category {
			continue
		}
		if best == nil || m.Priority < best.Priority {
			best = m
		}
	}
	return best
}

// CategoryListing groups models by category for UI pickers.
type CategoryListing struct {
	Category ModelCategory `json:"category"`
	Default  string        `json:"default"` // ID of the highest-priority model
	Models   []ModelExpert `json:"models"`  // all models in this category, sorted by priority
}

// ListByCategory returns every category with its models sorted by priority.
// Use this to render a per-category picker in the frontend.
func ListByCategory() []CategoryListing {
	cats := AllCategories()
	out := make([]CategoryListing, 0, len(cats))
	for _, cat := range cats {
		models := GetModelsByCategory(cat)
		def := ""
		if len(models) > 0 {
			def = models[0].ID
		}
		out = append(out, CategoryListing{
			Category: cat,
			Default:  def,
			Models:   models,
		})
	}
	return out
}

// ── Default model accessors (used at startup to build LLM clients) ───

// DefaultVisionModel returns the highest-priority vision model ID.
func DefaultVisionModel() string {
	if m := GetDefault(CategoryVision); m != nil {
		return m.ID
	}
	return "meta/llama-3.2-90b-vision-instruct"
}

// DefaultSolverModel returns the highest-priority Solver:Level2 model ID.
// Used for solution agents at startup.
func DefaultSolverModel() string {
	if m := GetDefault(CategorySolverLevel2); m != nil {
		return m.ID
	}
	return "qwen/qwen3-next-80b-a3b-thinking"
}

// DefaultHintModel returns the highest-priority HintGenerator model ID.
// Uses an instruction-following model (not a reasoning model) so hints
// stay concise and don't dump full solutions.
func DefaultHintModel() string {
	if m := GetDefault(CategoryHintGenerator); m != nil {
		return m.ID
	}
	return "google/gemma-3-27b-it"
}

// DefaultRouterModel returns the highest-priority Router model ID
// for validation, parsing, and verification.
func DefaultRouterModel() string {
	if m := GetDefault(CategoryRouter); m != nil {
		return m.ID
	}
	return "meta/llama-3.3-70b-instruct"
}

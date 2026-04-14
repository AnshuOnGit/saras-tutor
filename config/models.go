package config

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

	// Category indicates the model's role and strength.
	Category ModelCategory `json:"category"`

	// RecommendedDifficulty is 1-4 where:
	//   1 = easy / recall (NCERT-level)
	//   2 = moderate (JEE Main level)
	//   3 = hard (JEE Advanced level)
	//   4 = extreme (olympiad / multi-step proof)
	RecommendedDifficulty int `json:"recommended_difficulty"`

	// Recommended is true if this model is the default pick for its category.
	Recommended bool `json:"recommended"`
}

// ModelRegistry is the global ordered list of available models.
// Order matters: first match in SelectModelForTask wins within a category.
var ModelRegistry = []ModelExpert{
	// ── Vision ──
	{
		ID:                    "meta/llama-3.2-90b-vision-instruct",
		DisplayName:           "Llama 3.2 90B Vision",
		Category:              CategoryVision,
		RecommendedDifficulty: 1,
		Recommended:           true,
	},

	// ── Solver:Level2 (math / physics reasoning) ──
	{
		ID:                    "deepseek-ai/deepseek-r1-distill-qwen-32b",
		DisplayName:           "DeepSeek-R1 Qwen 32B",
		Category:              CategorySolverLevel2,
		RecommendedDifficulty: 3,
		Recommended:           true,
	},
	{
		ID:                    "qwen/qwq-32b",
		DisplayName:           "QwQ 32B",
		Category:              CategorySolverLevel2,
		RecommendedDifficulty: 2,
		Recommended:           false,
	},

	// ── Solver:Level1 (theory / general) ──
	{
		ID:                    "mistralai/mistral-small-3.1-24b-instruct-2503",
		DisplayName:           "Mistral Small 3.1 24B",
		Category:              CategorySolverLevel1,
		RecommendedDifficulty: 2,
		Recommended:           true,
	},

	// ── Solver:Level3 (olympiad / extreme) ──
	{
		ID:                    "meta/llama-3.1-405b-instruct",
		DisplayName:           "Llama 3.1 405B",
		Category:              CategorySolverLevel3,
		RecommendedDifficulty: 4,
		Recommended:           true,
	},

	// ── HintGenerator (pedagogical hints at every level) ──
	// Mistral Small follows hint constraints tightly (reasoning models like
	// DeepSeek-R1 dump their full chain-of-thought and solve the problem).
	{
		ID:                    "mistralai/mistral-small-3.1-24b-instruct-2503",
		DisplayName:           "Mistral Small 3.1 24B (Hint)",
		Category:              CategoryHintGenerator,
		RecommendedDifficulty: 2,
		Recommended:           true,
	},

	// ── Router (intent detection, NLP, validation, parsing, verification) ──
	{
		ID:                    "mistralai/mistral-small-3.1-24b-instruct-2503",
		DisplayName:           "Mistral Small 3.1 24B (Router)",
		Category:              CategoryRouter,
		RecommendedDifficulty: 1,
		Recommended:           true,
	},
}

// ── Lookup helpers ───────────────────────────────────────────────────

// GetModelsByCategory returns all models belonging to the given category.
func GetModelsByCategory(category ModelCategory) []ModelExpert {
	var out []ModelExpert
	for _, m := range ModelRegistry {
		if m.Category == category {
			out = append(out, m)
		}
	}
	return out
}

// GetAllSolverModels returns models from all Solver sub-categories.
func GetAllSolverModels() []ModelExpert {
	var out []ModelExpert
	for _, m := range ModelRegistry {
		if IsSolverCategory(m.Category) {
			out = append(out, m)
		}
	}
	return out
}

// GetModelByID returns a model by its NVIDIA NIM ID, or nil if not found.
func GetModelByID(id string) *ModelExpert {
	for i := range ModelRegistry {
		if ModelRegistry[i].ID == id {
			return &ModelRegistry[i]
		}
	}
	return nil
}

// GetRecommended returns the first recommended model for a category.
func GetRecommended(category ModelCategory) *ModelExpert {
	for i := range ModelRegistry {
		if ModelRegistry[i].Category == category && ModelRegistry[i].Recommended {
			return &ModelRegistry[i]
		}
	}
	return nil
}

// ── Task-based selection ─────────────────────────────────────────────

// SelectModelForTask picks the best solver model ID given a difficulty and subject.
// Returns the NVIDIA NIM model ID string ready for llm.NewClient().
//
// Logic:
//
//	isVision                 → Vision model
//	difficulty >= 4          → Solver:Level3 (405B)
//	difficulty >= 3          → Solver:Level2 recommended (DeepSeek-R1)
//	subject is biology/chem  → Solver:Level1 (Mistral Small)
//	default                  → Solver:Level2 recommended
func SelectModelForTask(difficulty int, subject string, isVision bool) string {
	if isVision {
		if m := GetRecommended(CategoryVision); m != nil {
			return m.ID
		}
	}

	if difficulty >= 4 {
		if m := GetRecommended(CategorySolverLevel3); m != nil {
			return m.ID
		}
	}

	if difficulty >= 3 {
		if m := GetRecommended(CategorySolverLevel2); m != nil {
			return m.ID
		}
	}

	// Theory subjects prefer the Level1 solver
	switch subject {
	case "biology", "Biology", "chemistry", "Chemistry":
		if m := GetRecommended(CategorySolverLevel1); m != nil {
			return m.ID
		}
	}

	// Default: Level2 solver
	if m := GetRecommended(CategorySolverLevel2); m != nil {
		return m.ID
	}
	return ModelRegistry[0].ID
}

// ── Default model accessors (used at startup to build LLM clients) ───

// DefaultVisionModel returns the recommended vision model ID.
func DefaultVisionModel() string {
	if m := GetRecommended(CategoryVision); m != nil {
		return m.ID
	}
	return "meta/llama-3.2-90b-vision-instruct"
}

// DefaultSolverModel returns the recommended Level2 solver model ID.
// Used for solution agents at startup.
func DefaultSolverModel() string {
	if m := GetRecommended(CategorySolverLevel2); m != nil {
		return m.ID
	}
	return "deepseek-ai/deepseek-r1-distill-qwen-32b"
}

// DefaultHintModel returns the recommended model for hint generation.
// Uses an instruction-following model (not a reasoning model) so hints
// stay concise and don't dump full solutions.
func DefaultHintModel() string {
	if m := GetRecommended(CategoryHintGenerator); m != nil {
		return m.ID
	}
	return "mistralai/mistral-small-3.1-24b-instruct-2503"
}

// DefaultRouterModel returns the lightweight model for intent detection,
// validation, parsing, and verification.
func DefaultRouterModel() string {
	if m := GetRecommended(CategoryRouter); m != nil {
		return m.ID
	}
	return "mistralai/mistral-small-3.1-24b-instruct-2503"
}

// RetryModelIDs returns the IDs of all solver models for the retry picker.
func RetryModelIDs() []string {
	var ids []string
	for _, m := range ModelRegistry {
		if IsSolverCategory(m.Category) {
			ids = append(ids, m.ID)
		}
	}
	return ids
}

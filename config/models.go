package config

// ModelCategory classifies what a model is best suited for.
type ModelCategory string

const (
	CategoryVision  ModelCategory = "Vision"
	CategoryMath    ModelCategory = "Math"
	CategoryTheory  ModelCategory = "Theory"
	CategoryExtreme ModelCategory = "Extreme"
)

// ModelExpert describes a single NVIDIA NIM model available for the tutor.
type ModelExpert struct {
	ID                    string        `json:"id"`                     // NVIDIA NIM model ID
	DisplayName           string        `json:"display_name"`           // Human-friendly label
	Category              ModelCategory `json:"category"`               // Vision | Math | Theory | Extreme
	RecommendedDifficulty int           `json:"recommended_difficulty"` // 1 (easy) → 4 (olympiad)
}

// ModelRegistry is the global list of models the app can offer.
var ModelRegistry = []ModelExpert{
	// ── Vision ──
	{
		ID:                    "meta/llama-3.2-90b-vision-instruct",
		DisplayName:           "Llama 3.2 90B Vision",
		Category:              CategoryVision,
		RecommendedDifficulty: 2,
	},

	// ── Math / Logic ──
	{
		ID:                    "deepseek-ai/deepseek-r1-distill-qwen-32b",
		DisplayName:           "DeepSeek-R1 Distill Qwen 32B",
		Category:              CategoryMath,
		RecommendedDifficulty: 3,
	},
	{
		ID:                    "qwen/qwq-32b",
		DisplayName:           "QwQ 32B",
		Category:              CategoryMath,
		RecommendedDifficulty: 3,
	},

	// ── Theory ──
	{
		ID:                    "mistralai/mistral-small-3.1-24b-instruct-2503",
		DisplayName:           "Mistral Small 3.1 24B",
		Category:              CategoryTheory,
		RecommendedDifficulty: 1,
	},

	// ── Extreme ──
	{
		ID:                    "meta/llama-3.1-405b-instruct",
		DisplayName:           "Llama 3.1 405B",
		Category:              CategoryExtreme,
		RecommendedDifficulty: 4,
	},
}

// GetModelsByCategory returns every model matching the given category.
// Returns nil if no models match.
func GetModelsByCategory(category ModelCategory) []ModelExpert {
	var out []ModelExpert
	for _, m := range ModelRegistry {
		if m.Category == category {
			out = append(out, m)
		}
	}
	return out
}

// GetModelByID looks up a single model by its NVIDIA NIM ID.
// Returns nil if not found.
func GetModelByID(id string) *ModelExpert {
	for i := range ModelRegistry {
		if ModelRegistry[i].ID == id {
			return &ModelRegistry[i]
		}
	}
	return nil
}

// SelectModelForTask picks the best model given the task context.
//
// Priority (first match wins):
//  1. Vision extraction → always use the VLM.
//  2. Difficulty 4 (IIT Advanced / Olympiad) → largest model.
//  3. Math or Physics at difficulty 3 → deep-reasoning model.
//  4. Biology or Chemistry → theory-optimised model.
//  5. Everything else → the fast general-purpose router model.
func SelectModelForTask(difficulty int, subject string, isInitialExtraction bool) string {
	if isInitialExtraction {
		return DefaultVisionModel()
	}
	if difficulty == 4 {
		return DefaultExtremeModel()
	}
	if (subject == "Mathematics" || subject == "Physics") && difficulty >= 3 {
		return DefaultSolverModel()
	}
	if subject == "Biology" || subject == "Chemistry" {
		return DefaultTheoryModel()
	}
	return DefaultRouterModel()
}

// ── Default model selectors (single source of truth) ──
// Each returns the first model in the registry matching the requested category.

// DefaultVisionModel returns the ID of the vision VLM (image extraction).
func DefaultVisionModel() string {
	if m := GetModelsByCategory(CategoryVision); len(m) > 0 {
		return m[0].ID
	}
	return "meta/llama-3.2-90b-vision-instruct" // hard fallback
}

// DefaultSolverModel returns the ID of the primary math/logic solver.
func DefaultSolverModel() string {
	if m := GetModelsByCategory(CategoryMath); len(m) > 0 {
		return m[0].ID
	}
	return "deepseek-ai/deepseek-r1-distill-qwen-32b"
}

// DefaultTheoryModel returns the ID of the theory/concept model.
func DefaultTheoryModel() string {
	if m := GetModelsByCategory(CategoryTheory); len(m) > 0 {
		return m[0].ID
	}
	return "mistralai/mistral-small-3.1-24b-instruct-2503"
}

// DefaultExtremeModel returns the ID of the largest model for olympiad-level.
func DefaultExtremeModel() string {
	if m := GetModelsByCategory(CategoryExtreme); len(m) > 0 {
		return m[0].ID
	}
	return "meta/llama-3.1-405b-instruct"
}

// DefaultRouterModel returns the ID of the fast/cheap model for validation, parsing, etc.
func DefaultRouterModel() string {
	// Router model is a lightweight general model — not in the registry
	// as a category, but we return a sensible default.
	return "meta/llama-3.1-70b-instruct"
}

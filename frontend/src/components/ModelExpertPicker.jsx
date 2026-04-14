import { useState, useEffect } from "react";

/**
 * Category display labels and icons.
 */
const CATEGORY_META = {
  Vision:  { label: "Visual Accuracy", icon: "👁️", description: "Re-extract text from the image" },
  Math:    { label: "Math & Logic",    icon: "🧮", description: "Deep reasoning for hard problems" },
  Theory:  { label: "Theory",          icon: "📚", description: "Concept explanations & theory" },
  Extreme: { label: "Extreme",         icon: "🚀", description: "Largest model for olympiad-level" },
};

/** Ordered list of categories for consistent display. */
const CATEGORY_ORDER = ["Vision", "Math", "Theory", "Extreme"];

/**
 * ModelExpertPicker — shown when a student is unsatisfied with the response.
 *
 * Fetches the expert list from GET /experts?difficulty=X&subject=Y,
 * groups them by category, and highlights the "Saras Recommended" model.
 */
export default function ModelExpertPicker({
  onPickModel,
  onDismiss,
  difficulty = 0,
  subject = "",
}) {
  const [experts, setExperts] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);

    const params = new URLSearchParams();
    if (difficulty) params.set("difficulty", String(difficulty));
    if (subject) params.set("subject", subject);

    fetch(`/experts?${params}`)
      .then((res) => {
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        return res.json();
      })
      .then((data) => {
        if (!cancelled) setExperts(data);
      })
      .catch((err) => {
        if (!cancelled) setError(err.message);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });

    return () => { cancelled = true; };
  }, [difficulty, subject]);

  // Group experts by category
  const grouped = {};
  for (const cat of CATEGORY_ORDER) {
    grouped[cat] = [];
  }
  for (const expert of experts) {
    const cat = expert.category || "Theory";
    if (!grouped[cat]) grouped[cat] = [];
    grouped[cat].push(expert);
  }

  if (loading) {
    return (
      <div className="message expert-picker">
        <div className="expert-picker-header">🔄 Loading models…</div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="message expert-picker">
        <div className="expert-picker-header">❌ Failed to load models</div>
        <p className="expert-picker-error">{error}</p>
        <button className="btn-model-dismiss" onClick={onDismiss}>
          Dismiss
        </button>
      </div>
    );
  }

  return (
    <div className="message expert-picker">
      <div className="expert-picker-header">
        🔄 Not satisfied? Try a different expert model
      </div>

      <div className="expert-picker-groups">
        {CATEGORY_ORDER.map((cat) => {
          const items = grouped[cat];
          if (!items || items.length === 0) return null;
          const meta = CATEGORY_META[cat] || { label: cat, icon: "🤖", description: "" };

          return (
            <div key={cat} className="expert-group">
              <div className="expert-group-label">
                <span className="expert-group-icon">{meta.icon}</span>
                <span className="expert-group-name">{meta.label}</span>
                <span className="expert-group-desc">{meta.description}</span>
              </div>

              <div className="expert-cards">
                {items.map((expert) => (
                  <button
                    key={expert.id}
                    className={`expert-card ${expert.recommended ? "recommended" : ""}`}
                    onClick={() => onPickModel(expert.id)}
                    title={expert.id}
                  >
                    <span className="expert-card-name">{expert.display_name}</span>
                    {expert.recommended && (
                      <span className="expert-badge">⭐ Saras Recommended</span>
                    )}
                    <span className="expert-card-diff">
                      {"●".repeat(expert.recommended_difficulty)}
                      {"○".repeat(4 - expert.recommended_difficulty)}
                    </span>
                  </button>
                ))}
              </div>
            </div>
          );
        })}
      </div>

      <button className="btn-model-dismiss expert-dismiss" onClick={onDismiss}>
        ✗ Keep current answer
      </button>
    </div>
  );
}

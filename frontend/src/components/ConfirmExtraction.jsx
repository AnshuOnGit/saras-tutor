import { useState, useEffect } from "react";
import MarkdownRenderer from "./MarkdownRenderer";

/**
 * ConfirmExtraction — shown after image extraction so the student can
 * verify the OCR result before the system proceeds to solve.
 *
 * Three actions:
 *   ✓ Looks correct → onConfirm()   → sends confirm_extraction
 *   🔄 Re-extract   → onReExtract(modelId) → sends retry_model with a vision model
 *   ✕ Cancel        → onCancel()    → sends close
 */
export default function ConfirmExtraction({ text, onConfirm, onReExtract, onCancel }) {
  const [showModels, setShowModels] = useState(false);
  const [experts, setExperts] = useState([]);
  const [loadingModels, setLoadingModels] = useState(false);

  // Fetch all models when the re-extract picker is opened
  useEffect(() => {
    if (!showModels) return;
    let cancelled = false;
    setLoadingModels(true);

    fetch("/experts")
      .then((res) => res.json())
      .then((data) => {
        if (!cancelled) setExperts(data);
      })
      .catch(() => {})
      .finally(() => {
        if (!cancelled) setLoadingModels(false);
      });

    return () => { cancelled = true; };
  }, [showModels]);

  const visionModels = experts.filter((m) => m.category === "Vision");
  const otherModels = experts.filter((m) => m.category !== "Vision");
  const ICONS = { Vision: "👁️", Math: "🧮", Theory: "📚", Extreme: "🚀" };

  return (
    <div className="message confirm-extraction">
      <div className="confirm-header">📷 Extracted Question</div>
      <div className="confirm-body">
        <MarkdownRenderer content={text} />
      </div>
      <div className="confirm-prompt">
        Does this look correct? If the extraction is wrong (missing terms, wrong
        symbols, etc.) you can re-extract with a different vision model.
      </div>
      <div className="confirm-actions">
        <button className="btn-confirm" onClick={() => onConfirm(text)}>
          ✓ Yes, solve this
        </button>
        <button
          className="btn-reextract"
          onClick={() => setShowModels((v) => !v)}
        >
          {showModels ? "▲ Hide models" : "🔄 Re-extract with different model"}
        </button>
        <button className="btn-cancel" onClick={onCancel}>
          ✕ Cancel
        </button>
      </div>

      {showModels && (
        <div className="reextract-picker">
          {loadingModels ? (
            <div className="reextract-loading">Loading models…</div>
          ) : (
            <>
              {visionModels.length > 0 && (
                <>
                  <div className="reextract-picker-label">
                    👁️ Vision models — re-extract the image:
                  </div>
                  <div className="reextract-cards">
                    {visionModels.map((m) => (
                      <button
                        key={m.id}
                        className="reextract-card vision"
                        onClick={() => onReExtract(m.id)}
                        title={m.id}
                      >
                        👁️ {m.display_name}
                      </button>
                    ))}
                  </div>
                </>
              )}

              {otherModels.length > 0 && (
                <>
                  <div className="reextract-picker-label reextract-other-label">
                    Or try a different solver model:
                  </div>
                  <div className="reextract-cards">
                    {otherModels.map((m) => (
                      <button
                        key={m.id}
                        className="reextract-card"
                        onClick={() => onReExtract(m.id)}
                        title={m.id}
                      >
                        {ICONS[m.category] || "🤖"} {m.display_name}
                      </button>
                    ))}
                  </div>
                </>
              )}
            </>
          )}
        </div>
      )}
    </div>
  );
}

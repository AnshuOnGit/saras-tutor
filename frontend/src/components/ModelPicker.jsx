/**
 * ModelPicker — offers the student alternative models for a just-finished task.
 *
 * Three modes, keyed off the `proceedAction` and `optional` props:
 *
 *  1. Gated proceed (proceedAction set, e.g. "extract_proceed"):
 *       Primary "✓ Proceed" button fires onProceed(proceedAction).
 *       Alternative model buttons fire onPickModel(id, category) to retry.
 *       No dismiss button — the user MUST either proceed or retry.
 *
 *  2. Optional (optional=true, no proceedAction):
 *       Soft "Try another model, or continue" bar.
 *       Dismiss button labelled "✓ Continue" keeps current answer.
 *
 *  3. Mandatory (optional=false, no proceedAction — legacy verifier reject):
 *       "Pick a different model" — dismiss is labelled "Keep current answer".
 *
 * `models` is a list of { id, display_name, notes, priority } objects, or
 * plain strings for back-compat.
 */
export default function ModelPicker({
  models,
  category,
  current,
  optional,
  reason,
  proceedAction,
  onPickModel,
  onProceed,
  onDismiss,
}) {
  const hasAlts = Array.isArray(models) && models.length > 0;
  const isGated = !!proceedAction;

  // Nothing to show
  if (!hasAlts && !isGated) return null;

  const normalised = (models || []).map((m) =>
    typeof m === "string" ? { id: m, display_name: m, notes: "" } : m
  );

  let title;
  if (isGated) {
    title = "👀 Review the output";
  } else if (optional) {
    title = "🔄 Not satisfied? Try another model";
  } else {
    title = "🔄 Try with a different model?";
  }

  const dismissLabel = optional ? "✓ Continue" : "✗ Keep current answer";
  const variant = isGated ? "gated" : optional ? "optional" : "mandatory";

  return (
    <div className={`message model-picker ${variant}`}>
      <div className="model-picker-header">{title}</div>
      {reason && <div className="model-picker-reason">{reason}</div>}
      {category && current && (
        <div className="model-picker-current">
          <span className="tag">{category}</span>
          <span className="current-model">current: {current}</span>
        </div>
      )}
      <div className="model-picker-buttons">
        {isGated && (
          <button
            className="btn-model-proceed"
            onClick={() => onProceed && onProceed(proceedAction)}
          >
            <div className="btn-model-name">✓ Proceed</div>
            <div className="btn-model-notes">Accept and continue</div>
          </button>
        )}
        {normalised.map((m) => (
          <button
            key={m.id}
            className="btn-model"
            title={m.notes || m.id}
            onClick={() => onPickModel(m.id, category)}
          >
            <div className="btn-model-name">{m.display_name || m.id}</div>
            {m.notes && <div className="btn-model-notes">{m.notes}</div>}
          </button>
        ))}
        {!isGated && (
          <button className="btn-model-dismiss" onClick={onDismiss}>
            {dismissLabel}
          </button>
        )}
      </div>
    </div>
  );
}
